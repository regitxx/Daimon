package activity

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/secretbox"
)

// Log is the principal's append-only activity record. It binds a file path to
// the identity that signs every entry. Concurrent Append calls are serialized
// internally; Query and Verify open the file separately for read and do not
// block writers (writers fsync after each append, so reads see committed
// entries up to the moment they open the file).
//
// At-rest confidentiality (SPEC §8.1) is provided by application-level payload
// encryption: each entry's `payload` field is stored as base64(AES-256-GCM)
// with a per-entry random nonce and AAD bound to (entry_id, "payload"). The
// id, ts, kind, prev_hash, hash, and signature columns remain in clear so
// Query filtering, LastHash recovery on Open, and chain continuity all work
// without unlock. The hash chain and signature commit to the canonical
// *plaintext* entry, so chain integrity is preserved across the encryption
// boundary — see crypto.go.
type Log struct {
	path string
	id   *identity.Identity
	key  []byte // 32-byte AES key derived from the bound identity; nil disables encryption

	mu       sync.Mutex
	f        *os.File
	lastHash string
	closed   bool
}

// Open opens (or creates) the activity log at path, bound to id. The file is
// opened in append mode with mode 0600. The last hash in the file is recovered
// so chain continuity survives across restarts.
//
// Open does NOT verify the existing chain; for that, call Verify after Open.
// This keeps startup O(1) for daimons with long histories.
//
// The payload-encryption key is derived from the identity's Ed25519 seed via
// HKDF (ActivityEncryptionKeyLabel) and held in process memory for the life
// of the Log. The same identity will rederive the same key on subsequent
// opens, allowing entries written in one process to be read in the next. The
// key never touches disk.
func Open(path string, id *identity.Identity) (*Log, error) {
	if id == nil {
		return nil, errors.New("activity: identity is required")
	}
	key, err := id.DeriveSubkey(ActivityEncryptionKeyLabel, secretbox.KeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive activity key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open activity log: %w", err)
	}

	last, err := scanLastHash(path)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	return &Log{
		path:     path,
		id:       id,
		key:      key,
		f:        f,
		lastHash: last,
	}, nil
}

// Close releases the file handle. After Close, Append returns ErrLogClosed.
// Query and Verify continue to work (they open the file independently).
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Path returns the on-disk path. Useful for tests and tooling.
func (l *Log) Path() string { return l.path }

// LastHash returns the most recent committed hash, or ZeroHash() if the log
// is empty. Safe to call concurrently.
func (l *Log) LastHash() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastHash
}

// Append writes a new signed entry chained to the last committed entry.
// The returned Entry has ID, Timestamp, Hash, and Signature populated and
// carries the *plaintext* payload — the on-disk representation is encrypted
// per SPEC §8.1 (see crypto.go), but callers see the same plaintext shape as
// before the encryption layer landed.
//
// payload may be nil; if non-nil it is canonicalized via json.Marshal (sorted
// map keys) and embedded in the entry. The hash commits to all fields except
// Hash and Signature themselves, and is computed over the canonical
// *plaintext* form so chain integrity is preserved across the encryption
// boundary.
func (l *Log) Append(_ context.Context, kind Kind, payload map[string]any) (*Entry, error) {
	if kind == "" {
		return nil, ErrEmptyKind
	}

	var payloadBytes json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		payloadBytes = b
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed || l.f == nil {
		return nil, ErrLogClosed
	}

	now := time.Now().UnixMilli()
	id, err := ulid.New(uint64(now), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("new ulid: %w", err)
	}
	e := &Entry{
		ID:        id.String(),
		Timestamp: now,
		Kind:      kind,
		Payload:   payloadBytes,
		PrevHash:  l.lastHash,
	}
	hashStr, hashBytes, err := e.computeHash()
	if err != nil {
		return nil, fmt.Errorf("compute hash: %w", err)
	}
	e.Hash = hashStr

	sig, err := l.id.Sign(hashBytes)
	if err != nil {
		return nil, fmt.Errorf("sign entry: %w", err)
	}
	e.Signature = sig

	// Build the on-disk shape: same struct, payload replaced with the
	// encrypted-and-base64 wire form. Hash and Signature are already populated
	// over the plaintext canonical bytes, so the chain commits to plaintext
	// and verification works after decryption.
	diskPayload, err := encodePayloadForDisk(l.key, payloadBytes, e.ID)
	if err != nil {
		return nil, fmt.Errorf("encrypt payload: %w", err)
	}
	disk := *e
	disk.Payload = diskPayload

	line, err := json.Marshal(&disk)
	if err != nil {
		return nil, fmt.Errorf("marshal entry: %w", err)
	}
	line = append(line, '\n')

	if _, err := l.f.Write(line); err != nil {
		return nil, fmt.Errorf("write entry: %w", err)
	}
	if err := l.f.Sync(); err != nil {
		return nil, fmt.Errorf("fsync: %w", err)
	}
	l.lastHash = e.Hash
	return e, nil
}

// QueryOptions controls Query filtering.
type QueryOptions struct {
	Since int64 // unix ms inclusive lower bound; 0 = no lower bound
	Until int64 // unix ms inclusive upper bound; 0 = no upper bound
	Kind  Kind  // empty = all kinds
	Limit int   // 0 = unlimited
}

// Query returns entries matching opts in chain order (oldest first).
// Signatures and chain links are NOT verified — callers who need integrity
// should call Verify. This separation keeps Query cheap.
//
// Each returned Entry carries the *plaintext* payload after AEAD decryption.
// Filters that don't depend on payload (timestamp range, kind, limit) are
// applied before decryption so non-matching entries don't pay the AEAD cost.
func (l *Log) Query(_ context.Context, opts QueryOptions) ([]*Entry, error) {
	rf, err := os.Open(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open for query: %w", err)
	}
	defer rf.Close()

	var out []*Entry
	sc := newLineScanner(rf)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return out, fmt.Errorf("%w: %v", ErrCorruptLog, err)
		}
		if opts.Since > 0 && e.Timestamp < opts.Since {
			continue
		}
		if opts.Until > 0 && e.Timestamp > opts.Until {
			continue
		}
		if opts.Kind != "" && e.Kind != opts.Kind {
			continue
		}
		pt, err := decodePayloadFromDisk(l.key, e.Payload, e.ID)
		if err != nil {
			return out, fmt.Errorf("decrypt payload: %w", err)
		}
		e.Payload = pt
		out = append(out, &e)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// Verify walks the log from genesis to the end, checking three properties for
// every entry: (1) PrevHash matches the previous entry's Hash, (2) the stored
// Hash matches a freshly computed BLAKE3 of canonical bytes, (3) Signature
// verifies under the bound identity's public key.
//
// Returns the number of entries verified. On the first failure it returns the
// count up to (but not including) the bad entry along with the error.
func (l *Log) Verify(_ context.Context) (int, error) {
	rf, err := os.Open(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("open for verify: %w", err)
	}
	defer rf.Close()

	pub := l.id.PublicKey()
	expected := ZeroHash()
	n := 0

	sc := newLineScanner(rf)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return n, fmt.Errorf("%w: %v", ErrCorruptLog, err)
		}
		if e.PrevHash != expected {
			return n, fmt.Errorf("%w: id=%s expected_prev=%s got_prev=%s",
				ErrChainBroken, e.ID, expected, e.PrevHash)
		}
		// Decrypt the payload before recomputing the hash — the chain commits
		// to plaintext canonical bytes (see crypto.go), so a wrong key or a
		// tampered ciphertext surfaces as ErrInvalidCiphertext here, before
		// the hash check runs.
		pt, err := decodePayloadFromDisk(l.key, e.Payload, e.ID)
		if err != nil {
			return n, fmt.Errorf("decrypt payload: %w", err)
		}
		e.Payload = pt
		hashStr, hashBytes, err := e.computeHash()
		if err != nil {
			return n, fmt.Errorf("hash: %w", err)
		}
		if hashStr != e.Hash {
			return n, fmt.Errorf("%w: id=%s", ErrHashMismatch, e.ID)
		}
		// Re-derive the raw hash bytes from the stored hex too; if they
		// disagree with the canonical recomputation above, ErrHashMismatch
		// already fired. Use the canonical bytes for the signature check.
		if !ed25519.Verify(pub, hashBytes, e.Signature) {
			return n, fmt.Errorf("%w: id=%s", ErrSignatureFailed, e.ID)
		}
		expected = e.Hash
		n++
	}
	if err := sc.Err(); err != nil {
		return n, err
	}
	return n, nil
}

// scanLastHash walks the file once, returning the Hash of the last well-formed
// entry. Empty file → ZeroHash. A corrupt line returns an error so we don't
// silently chain off a partial write.
func scanLastHash(path string) (string, error) {
	last, _, err := scanLog(path)
	return last, err
}

// ScanLastHash walks the activity log file at path once, returning the most
// recent committed hash and the number of entries scanned. Identity-free; the
// id, ts, kind, prev_hash, and hash columns remain in clear (SPEC §8.1) so
// this works without unlock and without the payload-decryption key — useful
// for tooling like `daimon doctor` that wants to surface audit-chain stats
// without holding the identity.
//
// Returns (ZeroHash, 0, nil) when path does not exist (treats absence as
// "log not yet initialised", not an error). Wraps ErrCorruptLog when a line
// is unparseable, so callers can errors.Is the result.
func ScanLastHash(path string) (lastHash string, entries int, err error) {
	return scanLog(path)
}

func scanLog(path string) (string, int, error) {
	rf, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ZeroHash(), 0, nil
		}
		return "", 0, fmt.Errorf("scan log: %w", err)
	}
	defer rf.Close()

	last := ZeroHash()
	count := 0
	sc := newLineScanner(rf)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return "", count, fmt.Errorf("%w: %v", ErrCorruptLog, err)
		}
		if e.Hash != "" {
			last = e.Hash
			count++
		}
	}
	if err := sc.Err(); err != nil {
		return "", count, err
	}
	return last, count, nil
}

// newLineScanner returns a scanner with a generous buffer so large entry
// payloads (rare) don't trip bufio's default 64KiB cap.
func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return sc
}
