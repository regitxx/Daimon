package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/regitxx/Daimon/internal/identity"
)

func newTestLog(t *testing.T) (*Log, *identity.Identity, string) {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "activity.log")
	log, err := Open(path, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return log, id, path
}

func TestOpenEmptyLog(t *testing.T) {
	log, _, path := newTestLog(t)
	if got, want := log.LastHash(), ZeroHash(); got != want {
		t.Errorf("LastHash on empty log: got %q want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode: got %o want 0600", info.Mode().Perm())
	}
}

func TestAppendGenesisAndChain(t *testing.T) {
	log, _, _ := newTestLog(t)
	ctx := context.Background()

	first, err := log.Append(ctx, KindDaimonCreated, map[string]any{
		"version": "v0.1.0-dev",
	})
	if err != nil {
		t.Fatalf("Append #1: %v", err)
	}
	if first.PrevHash != ZeroHash() {
		t.Errorf("genesis prev_hash: got %q want %q", first.PrevHash, ZeroHash())
	}
	if !strings.HasPrefix(first.Hash, HashPrefix) {
		t.Errorf("hash missing prefix: %q", first.Hash)
	}
	if len(first.Signature) == 0 {
		t.Error("genesis entry has no signature")
	}
	if log.LastHash() != first.Hash {
		t.Errorf("LastHash mismatch after Append: got %q want %q", log.LastHash(), first.Hash)
	}

	second, err := log.Append(ctx, KindMemoryWrite, map[string]any{"id": "mem-1"})
	if err != nil {
		t.Fatalf("Append #2: %v", err)
	}
	if second.PrevHash != first.Hash {
		t.Errorf("chain link broken: prev_hash=%q want %q", second.PrevHash, first.Hash)
	}
	if second.Hash == first.Hash {
		t.Error("two entries produced identical hash")
	}
}

func TestVerifyCleanChain(t *testing.T) {
	log, _, _ := newTestLog(t)
	ctx := context.Background()
	for i, k := range []Kind{KindDaimonCreated, KindMemoryWrite, KindMemoryWrite, KindMemoryExport} {
		if _, err := log.Append(ctx, k, map[string]any{"i": i}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	n, err := log.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if n != 4 {
		t.Errorf("Verify count: got %d want 4", n)
	}
}

// seedLog opens a log, appends n entries, closes it, and returns the path
// plus the identity used to sign them so the caller can reopen with the same key.
func seedLog(t *testing.T, n int) (string, *identity.Identity) {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "activity.log")
	log, err := Open(path, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := log.Append(context.Background(), KindMemoryWrite, map[string]any{"i": i}); err != nil {
			t.Fatalf("seed Append %d: %v", i, err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}
	return path, id
}

func TestVerifyDetectsTamperedPayload(t *testing.T) {
	path, id := seedLog(t, 3)

	// Replace entry #1's payload with arbitrary plaintext-shaped JSON,
	// leaving Hash and Signature intact. Under encryption, the on-disk
	// payload field MUST be a JSON string of base64-encoded ciphertext;
	// supplying a JSON object instead surfaces as ErrInvalidCiphertext
	// before the chain check ever runs. (This is strictly stronger than
	// pre-encryption ErrHashMismatch — AEAD authentication catches the
	// tamper one layer earlier.)
	lines := readLines(t, path)
	var e Entry
	if err := json.Unmarshal(lines[1], &e); err != nil {
		t.Fatal(err)
	}
	e.Payload = json.RawMessage(`{"i":99}`)
	tampered, err := json.Marshal(&e)
	if err != nil {
		t.Fatal(err)
	}
	lines[1] = tampered
	writeLines(t, path, lines)

	log2, err := Open(path, id) // same identity
	if err != nil {
		t.Fatal(err)
	}
	defer log2.Close()
	n, err := log2.Verify(context.Background())
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext, got %v (n=%d)", err, n)
	}
	if n != 1 {
		t.Errorf("Verify count before failure: got %d want 1", n)
	}
}

func TestVerifyDetectsBrokenChain(t *testing.T) {
	path, id := seedLog(t, 3)

	// Splice out the middle entry. The third entry's prev_hash now points
	// to the (deleted) middle entry's hash, not to entry #0.
	lines := readLines(t, path)
	lines = append(lines[:1], lines[2:]...)
	writeLines(t, path, lines)

	log2, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer log2.Close()
	_, err = log2.Verify(context.Background())
	if !errors.Is(err, ErrChainBroken) {
		t.Fatalf("expected ErrChainBroken, got %v", err)
	}
}

func TestVerifyDetectsBadSignature(t *testing.T) {
	// Two distinct identities; a chain signed by id1 but verified under id2
	// must fail signature checks.
	id1, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "activity.log")
	log1, err := Open(path, id1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log1.Append(context.Background(), KindDaimonCreated, nil); err != nil {
		t.Fatal(err)
	}
	if err := log1.Close(); err != nil {
		t.Fatal(err)
	}

	id2, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	log2, err := Open(path, id2)
	if err != nil {
		t.Fatal(err)
	}
	defer log2.Close()
	_, err = log2.Verify(context.Background())
	if !errors.Is(err, ErrSignatureFailed) {
		t.Fatalf("expected ErrSignatureFailed, got %v", err)
	}
}

func TestQueryFilters(t *testing.T) {
	log, _, _ := newTestLog(t)
	ctx := context.Background()
	emitted := []*Entry{}
	for i, k := range []Kind{KindDaimonCreated, KindMemoryWrite, KindMemoryWrite, KindMemoryExport, KindActivityQueried} {
		e, err := log.Append(ctx, k, map[string]any{"i": i})
		if err != nil {
			t.Fatal(err)
		}
		emitted = append(emitted, e)
	}

	all, err := log.Query(ctx, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Errorf("Query all: got %d want 5", len(all))
	}

	writes, err := log.Query(ctx, QueryOptions{Kind: KindMemoryWrite})
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) != 2 {
		t.Errorf("Query memory.write: got %d want 2", len(writes))
	}

	limited, err := log.Query(ctx, QueryOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Errorf("Query limit=2: got %d", len(limited))
	}

	since, err := log.Query(ctx, QueryOptions{Since: emitted[2].Timestamp})
	if err != nil {
		t.Fatal(err)
	}
	if len(since) < 3 {
		t.Errorf("Query since: got %d, want at least 3", len(since))
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "activity.log")

	log1, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	a, err := log1.Append(context.Background(), KindDaimonCreated, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := log1.Close(); err != nil {
		t.Fatal(err)
	}

	log2, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer log2.Close()
	if log2.LastHash() != a.Hash {
		t.Errorf("LastHash after reopen: got %q want %q", log2.LastHash(), a.Hash)
	}
	b, err := log2.Append(context.Background(), KindMemoryWrite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.PrevHash != a.Hash {
		t.Errorf("chain didn't continue: prev_hash=%q want %q", b.PrevHash, a.Hash)
	}
	n, err := log2.Verify(context.Background())
	if err != nil || n != 2 {
		t.Errorf("Verify after reopen: n=%d err=%v", n, err)
	}
}

func TestAppendAfterCloseFails(t *testing.T) {
	log, _, _ := newTestLog(t)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := log.Append(context.Background(), KindMemoryWrite, nil)
	if !errors.Is(err, ErrLogClosed) {
		t.Fatalf("expected ErrLogClosed, got %v", err)
	}
}

func TestAppendRejectsEmptyKind(t *testing.T) {
	log, _, _ := newTestLog(t)
	_, err := log.Append(context.Background(), "", nil)
	if !errors.Is(err, ErrEmptyKind) {
		t.Fatalf("expected ErrEmptyKind, got %v", err)
	}
}

func TestConcurrentAppends(t *testing.T) {
	log, _, _ := newTestLog(t)
	ctx := context.Background()
	const workers = 8
	const perWorker = 25

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := log.Append(ctx, KindMemoryWrite, map[string]any{
					"w": w, "i": i,
				}); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Chain must remain intact under concurrent appends.
	n, err := log.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify after concurrent appends: %v", err)
	}
	if n != workers*perWorker {
		t.Errorf("Verify count: got %d want %d", n, workers*perWorker)
	}
}

// --- encryption ---

// TestEncryptedPayloadOnDisk asserts that the on-disk JSONL line stores the
// payload as a JSON string (base64 of AEAD envelope) — not as a JSON object.
// The plaintext payload bytes MUST NOT appear anywhere in the line. This is
// the structural property that makes disk theft harmless for the kinds of
// data that narrate what the user did across providers.
func TestEncryptedPayloadOnDisk(t *testing.T) {
	log, _, path := newTestLog(t)
	secret := "switch-claude-to-openai-mid-task"
	if _, err := log.Append(context.Background(), KindProviderInvoke, map[string]any{
		"model":  "claude-opus-4-7",
		"secret": secret,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Errorf("plaintext leaked to disk: %q present in %q", secret, raw)
	}
	if bytes.Contains(raw, []byte("claude-opus-4-7")) {
		t.Errorf("plaintext model id leaked to disk: %q", raw)
	}
	// payload field must be a JSON string ("..."), not a JSON object ({...})
	var probe struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &probe); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(probe.Payload) == 0 || probe.Payload[0] != '"' {
		t.Errorf("payload field is not a JSON string: %q", probe.Payload)
	}
}

// TestEncryptionRoundtripQuery asserts that Append → Query returns the same
// plaintext payload, exercising the encrypt-on-write / decrypt-on-read path
// against a realistic provider.invoke shape with mixed types.
func TestEncryptionRoundtripQuery(t *testing.T) {
	log, _, _ := newTestLog(t)
	ctx := context.Background()

	want := map[string]any{
		"model":         "gpt-5-mini",
		"provider":      "openai",
		"input_tokens":  float64(42),
		"output_tokens": float64(128),
		"streamed":      true,
	}
	if _, err := log.Append(ctx, KindProviderInvoke, want); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := log.Query(ctx, QueryOptions{Kind: KindProviderInvoke})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	var got map[string]any
	if err := json.Unmarshal(entries[0].Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v (raw=%q)", err, entries[0].Payload)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("roundtrip mismatch:\n got:  %+v\n want: %+v", got, want)
	}
}

// TestEncryptionAADBindingDetectsCiphertextSwap asserts that the AAD binding
// to entry_id prevents an attacker from moving a stolen ciphertext from one
// entry onto another. Verify must reject the swap with ErrInvalidCiphertext.
func TestEncryptionAADBindingDetectsCiphertextSwap(t *testing.T) {
	path, id := seedLog(t, 2)

	// Pull the ciphertext payload field from entry #0 and splice it onto
	// entry #1's line, leaving entry #1's id/ts/kind/hash/signature intact.
	// AAD = (entry_id || "payload"); entry #0's ciphertext authenticates
	// under entry #0's id, not #1's, so AEAD.Open MUST fail on Verify.
	lines := readLines(t, path)
	var e0, e1 Entry
	if err := json.Unmarshal(lines[0], &e0); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lines[1], &e1); err != nil {
		t.Fatal(err)
	}
	if len(e0.Payload) == 0 || len(e1.Payload) == 0 {
		t.Fatalf("seed produced empty payloads (e0=%q e1=%q)", e0.Payload, e1.Payload)
	}
	e1.Payload = e0.Payload // swap entry #0's ciphertext onto entry #1
	tampered, err := json.Marshal(&e1)
	if err != nil {
		t.Fatal(err)
	}
	lines[1] = tampered
	writeLines(t, path, lines)

	log2, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer log2.Close()
	_, err = log2.Verify(context.Background())
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on cross-entry ciphertext swap, got %v", err)
	}
}

// TestEncryptionVerifyAfterReopen asserts that the chain still verifies after
// close + reopen with the same identity — i.e., the HKDF derivation is
// deterministic and decryption succeeds across process boundaries, and the
// hash chain (computed over plaintext) is preserved across the encryption
// boundary.
func TestEncryptionVerifyAfterReopen(t *testing.T) {
	path, id := seedLog(t, 5)
	log, err := Open(path, id) // same identity → same key
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	n, err := log.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify after reopen: %v", err)
	}
	if n != 5 {
		t.Errorf("Verify count: got %d want 5", n)
	}
}

// TestEncryptionForeignKeyFailsCleanly asserts that opening an existing log
// under a different identity (different HKDF subkey) surfaces a clear error
// from Query / Verify rather than silently returning corrupted data. This is
// the disk-theft-on-top-of-FDE scenario the encryption layer is for: an
// attacker who copies the JSONL file off the box gets ciphertext that won't
// decrypt under any other principal's seed.
func TestEncryptionForeignKeyFailsCleanly(t *testing.T) {
	path, _ := seedLog(t, 2)

	other, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	log, err := Open(path, other)
	if err != nil {
		t.Fatalf("Open under foreign identity: %v", err)
	}
	defer log.Close()

	_, qerr := log.Query(context.Background(), QueryOptions{})
	if !errors.Is(qerr, ErrInvalidCiphertext) {
		t.Errorf("Query under foreign key: expected ErrInvalidCiphertext, got %v", qerr)
	}

	_, verr := log.Verify(context.Background())
	if !errors.Is(verr, ErrInvalidCiphertext) {
		t.Errorf("Verify under foreign key: expected ErrInvalidCiphertext, got %v", verr)
	}
}

// TestEncryptionDeterministicKeyAcrossOpens asserts that the per-Log key
// derived from a fixed identity is stable — the canonical property that lets
// session N read entries written in session N-1 without storing the key on
// disk.
func TestEncryptionDeterministicKeyAcrossOpens(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "activity.log")

	log1, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"step": "before-close"}
	if _, err := log1.Append(context.Background(), KindMemoryWrite, want); err != nil {
		t.Fatal(err)
	}
	if err := log1.Close(); err != nil {
		t.Fatal(err)
	}

	log2, err := Open(path, id) // same identity → same HKDF subkey
	if err != nil {
		t.Fatal(err)
	}
	defer log2.Close()
	entries, err := log2.Query(context.Background(), QueryOptions{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	var got map[string]any
	if err := json.Unmarshal(entries[0].Payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload mismatch across reopens:\n got:  %+v\n want: %+v", got, want)
	}
}

// --- helpers ---

func readLines(t *testing.T, path string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		out = append(out, []byte(line))
	}
	return out
}

func writeLines(t *testing.T, path string, lines [][]byte) {
	t.Helper()
	var buf strings.Builder
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}
