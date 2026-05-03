package memory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regitxx/Daimon/internal/identity"
)

// stubEmbedder produces deterministic, low-dimensional embeddings derived from
// the SHA-256 of the input. Two inputs containing the same lowercase tokens
// will not collide, but the dot product is a sane proxy for "similar text".
// Used to exercise the cosine search path without requiring Ollama.
type stubEmbedder struct {
	dim int
}

func (s stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	dim := s.dim
	if dim == 0 {
		dim = 8
	}
	out := make([]float32, dim)
	// Token-bag embedding: hash each lowercase token, accumulate into a
	// fixed-dimension vector. Similar token sets → similar vectors.
	for _, tok := range strings.Fields(strings.ToLower(text)) {
		h := sha256.Sum256([]byte(tok))
		for i := 0; i < dim; i++ {
			b := binary.LittleEndian.Uint32(h[(i*4)%32:])
			out[i] += float32(int32(b)) / 1e9
		}
	}
	return out, nil
}

func (s stubEmbedder) Dimensions() int {
	d := s.dim
	if d == 0 {
		return 8
	}
	return d
}
func (stubEmbedder) Name() string { return "stub" }

func newTestStore(t *testing.T, embedder Embedder) (*Store, *identity.Identity) {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	store, err := Open(dbPath, id, embedder)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, id
}

func TestWriteReadRoundtrip(t *testing.T) {
	store, _ := newTestStore(t, nil) // NullEmbedder
	ctx := context.Background()

	mem, err := store.Write(ctx, WriteRequest{
		Kind:    KindFact,
		Content: "huckgod prefers Go over Rust for daimon-core",
		Metadata: map[string]any{
			"confidence": 0.9,
			"tags":       []any{"language", "preference"},
		},
		Source: "test",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if mem.ID == "" {
		t.Fatal("Write did not assign an ID")
	}
	if len(mem.Signature) == 0 {
		t.Fatal("Write did not produce a signature")
	}

	got, err := store.Read(ctx, mem.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Content != mem.Content {
		t.Errorf("content mismatch: got %q, want %q", got.Content, mem.Content)
	}
	if got.Kind != mem.Kind {
		t.Errorf("kind mismatch: got %q, want %q", got.Kind, mem.Kind)
	}
	if got.Source != "test" {
		t.Errorf("source mismatch: got %q", got.Source)
	}
	// Metadata bytes must be byte-identical to support signature verification.
	if string(got.Metadata) != string(mem.Metadata) {
		t.Errorf("metadata not byte-stable:\n got:  %s\n want: %s", got.Metadata, mem.Metadata)
	}
}

func TestWriteRejectsInvalidKind(t *testing.T) {
	store, _ := newTestStore(t, nil)
	_, err := store.Write(context.Background(), WriteRequest{Kind: "nonsense", Content: "x"})
	if !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("expected ErrInvalidKind, got %v", err)
	}
}

func TestWriteRejectsEmptyContent(t *testing.T) {
	store, _ := newTestStore(t, nil)
	_, err := store.Write(context.Background(), WriteRequest{Kind: KindFact, Content: ""})
	if !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("expected ErrEmptyContent, got %v", err)
	}
}

func TestRowsAreEncryptedAtRest(t *testing.T) {
	// SPEC §5.1: content, metadata, and source must not be readable in the
	// database file without the principal's identity. This test asserts the
	// load-bearing property by reading the columns directly via SQL and
	// checking they don't contain the plaintext.
	store, _ := newTestStore(t, nil)
	ctx := context.Background()
	plaintext := "highly distinctive secret content string xyz123"
	plaintextSource := "distinctive-source-marker"
	plaintextMeta := "distinctive-metadata-tag"
	mem, err := store.Write(ctx, WriteRequest{
		Kind:     KindFact,
		Content:  plaintext,
		Metadata: map[string]any{"tag": plaintextMeta},
		Source:   plaintextSource,
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		contentBlob []byte
		metaBlob    []byte
		sourceBlob  []byte
	)
	row := store.db.QueryRowContext(ctx, `SELECT content, metadata, source FROM memories WHERE id = ?`, mem.ID)
	if err := row.Scan(&contentBlob, &metaBlob, &sourceBlob); err != nil {
		t.Fatalf("raw scan: %v", err)
	}

	if strings.Contains(string(contentBlob), plaintext) {
		t.Error("content blob contains plaintext at rest")
	}
	if strings.Contains(string(metaBlob), plaintextMeta) {
		t.Error("metadata blob contains plaintext tag at rest")
	}
	if strings.Contains(string(sourceBlob), plaintextSource) {
		t.Error("source blob contains plaintext at rest")
	}
	if len(contentBlob) == 0 || contentBlob[0] != rowCryptoVersion {
		t.Errorf("content blob missing v1 framing byte (got first byte 0x%02x, want 0x%02x)",
			func() byte {
				if len(contentBlob) == 0 {
					return 0
				}
				return contentBlob[0]
			}(),
			rowCryptoVersion)
	}
}

func TestForeignIdentityCannotDecrypt(t *testing.T) {
	// A second daimon's identity must not be able to read a store written by
	// the first. This is the disk-theft / backup-exfiltration property: even
	// with full read access to the SQLite file, you need the principal's
	// passphrase (or the unlocked private key) to recover content.
	dbPath := filepath.Join(t.TempDir(), "shared.db")
	srcID, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	src, err := Open(dbPath, srcID, nil)
	if err != nil {
		t.Fatalf("Open src: %v", err)
	}
	ctx := context.Background()
	written, err := src.Write(ctx, WriteRequest{Kind: KindFact, Content: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	_ = src.Close()

	// Open the same file with a fresh (foreign) identity and try to read.
	foreignID, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := Open(dbPath, foreignID, nil)
	if err != nil {
		t.Fatalf("Open foreign: %v", err)
	}
	t.Cleanup(func() { _ = foreign.Close() })
	_, err = foreign.Read(ctx, written.ID)
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected foreign-identity Read to fail with ErrInvalidCiphertext, got %v", err)
	}

	// Same identity reopens the same file successfully — round-trips work
	// across process restarts because the key is deterministic in the seed.
	src2, err := Open(dbPath, srcID, nil)
	if err != nil {
		t.Fatalf("Open src2: %v", err)
	}
	t.Cleanup(func() { _ = src2.Close() })
	got, err := src2.Read(ctx, written.ID)
	if err != nil {
		t.Fatalf("reopened-store Read: %v", err)
	}
	if got.Content != "secret" {
		t.Errorf("reopened-store Content = %q, want %q", got.Content, "secret")
	}
}

func TestReadDetectsContentTampering(t *testing.T) {
	// Out-of-band write to the encrypted content column. With application-level
	// row encryption (SPEC §5.1), this is caught by AEAD authentication before
	// signature verification ever runs — surfaced as ErrInvalidCiphertext.
	store, _ := newTestStore(t, nil)
	ctx := context.Background()
	mem, err := store.Write(ctx, WriteRequest{Kind: KindFact, Content: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE memories SET content = ? WHERE id = ?`, "tampered", mem.ID); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, mem.ID)
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on tampered ciphertext, got %v", err)
	}
}

func TestReadDetectsSignatureTampering(t *testing.T) {
	// Tamper with a clear column the signature covers indirectly. The signature
	// column itself is in clear; flipping it must be caught by signature
	// verification after the row decrypts cleanly.
	store, _ := newTestStore(t, nil)
	ctx := context.Background()
	mem, err := store.Write(ctx, WriteRequest{Kind: KindFact, Content: "original"})
	if err != nil {
		t.Fatal(err)
	}
	tamperedSig := append([]byte(nil), mem.Signature...)
	tamperedSig[0] ^= 0x01
	if _, err := store.db.ExecContext(ctx, `UPDATE memories SET signature = ? WHERE id = ?`, tamperedSig, mem.ID); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, mem.ID)
	if !errors.Is(err, ErrSignatureFailed) {
		t.Fatalf("expected ErrSignatureFailed on tampered signature, got %v", err)
	}
}

func TestReadNotFound(t *testing.T) {
	store, _ := newTestStore(t, nil)
	_, err := store.Read(context.Background(), "01HXYZNOPE")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	store, _ := newTestStore(t, nil)
	ctx := context.Background()
	mem, err := store.Write(ctx, WriteRequest{Kind: KindObservation, Content: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.Delete(ctx, mem.ID)
	if err != nil || !ok {
		t.Fatalf("Delete: ok=%v err=%v", ok, err)
	}
	if _, err := store.Read(ctx, mem.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	// Second delete is a no-op (returns false, no error).
	ok, err = store.Delete(ctx, mem.ID)
	if err != nil {
		t.Fatalf("repeat delete err: %v", err)
	}
	if ok {
		t.Fatal("expected second Delete to report false")
	}
}

func TestListByKindAndLimit(t *testing.T) {
	store, _ := newTestStore(t, nil)
	ctx := context.Background()
	for i, c := range []struct {
		kind    Kind
		content string
	}{
		{KindFact, "first fact"},
		{KindFact, "second fact"},
		{KindPreference, "a preference"},
	} {
		if _, err := store.Write(ctx, WriteRequest{Kind: c.kind, Content: c.content}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	all, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("List all: got %d, want 3", len(all))
	}
	facts, err := store.List(ctx, ListOptions{Kind: KindFact})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Errorf("List facts: got %d, want 2", len(facts))
	}
	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Errorf("List limit=1: got %d", len(limited))
	}
}

func TestSearchSubstringFallback(t *testing.T) {
	store, _ := newTestStore(t, nil) // NullEmbedder → substring path
	ctx := context.Background()
	for _, c := range []string{
		"daimon prefers signed memory writes",
		"the principal owns their own activity log",
		"providers cannot see the full context",
	} {
		if _, err := store.Write(ctx, WriteRequest{Kind: KindFact, Content: c}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := store.Search(ctx, "principal", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'principal', got %d", len(results))
	}
	if !strings.Contains(results[0].Memory.Content, "principal") {
		t.Errorf("unexpected match: %q", results[0].Memory.Content)
	}
}

func TestSearchCosine(t *testing.T) {
	store, _ := newTestStore(t, stubEmbedder{dim: 32})
	ctx := context.Background()
	corpus := []string{
		"the daimon holds memory for the principal",
		"signed export documents enable migration",
		"vector search uses cosine similarity",
		"ed25519 keys provide identity",
	}
	for _, c := range corpus {
		if _, err := store.Write(ctx, WriteRequest{Kind: KindFact, Content: c}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := store.Search(ctx, "vector cosine similarity search", SearchOptions{Limit: 4})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}
	// Top hit should be the cosine-similarity sentence.
	if !strings.Contains(results[0].Memory.Content, "cosine similarity") {
		t.Errorf("expected cosine-similarity sentence on top, got %q with score %.4f",
			results[0].Memory.Content, results[0].Score)
	}
	// Results must be sorted by descending score.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted desc: %.4f then %.4f", results[i-1].Score, results[i].Score)
		}
	}
}

func TestExportImportRoundtrip(t *testing.T) {
	src, srcID := newTestStore(t, stubEmbedder{dim: 16})
	ctx := context.Background()

	for _, c := range []WriteRequest{
		{Kind: KindFact, Content: "daimon is the protocol", Metadata: map[string]any{"weight": 1}},
		{Kind: KindPreference, Content: "prefer go over rust for daimon-core"},
		{Kind: KindObservation, Content: "huckgod ships every session", Source: "self"},
	} {
		if _, err := src.Write(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	doc, err := src.Export(ctx)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if doc.Format != ExportFormat {
		t.Errorf("format: got %q", doc.Format)
	}
	if doc.DID != srcID.DID() {
		t.Errorf("DID: got %q want %q", doc.DID, srcID.DID())
	}
	if len(doc.Memories) != 3 {
		t.Errorf("memories count: got %d want 3", len(doc.Memories))
	}
	if len(doc.Signature) == 0 {
		t.Error("export has no signature")
	}

	// Roundtrip through JSON to simulate disk/wire.
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc2 ExportDocument
	if err := json.Unmarshal(raw, &doc2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Import into a fresh store with a different identity.
	dst, _ := newTestStore(t, NullEmbedder{})
	res, err := dst.Import(ctx, &doc2, ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Imported != 3 || res.Skipped != 0 {
		t.Errorf("ImportResult: %+v", res)
	}

	// Re-importing the same document should be idempotent (3 skipped).
	res2, err := dst.Import(ctx, &doc2, ImportOptions{})
	if err != nil {
		t.Fatalf("Import (re-run): %v", err)
	}
	if res2.Imported != 0 || res2.Skipped != 3 {
		t.Errorf("re-import not idempotent: %+v", res2)
	}
}

func TestImportRejectsTamperedDocument(t *testing.T) {
	src, _ := newTestStore(t, NullEmbedder{})
	ctx := context.Background()
	if _, err := src.Write(ctx, WriteRequest{Kind: KindFact, Content: "original"}); err != nil {
		t.Fatal(err)
	}
	doc, err := src.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the export's exported_at after signing.
	doc.ExportedAt += 1
	dst, _ := newTestStore(t, NullEmbedder{})
	_, err = dst.Import(ctx, doc, ImportOptions{})
	if !errors.Is(err, ErrSignatureFailed) {
		t.Fatalf("expected ErrSignatureFailed, got %v", err)
	}
}

func TestImportRejectsTamperedMemory(t *testing.T) {
	src, _ := newTestStore(t, NullEmbedder{})
	ctx := context.Background()
	if _, err := src.Write(ctx, WriteRequest{Kind: KindFact, Content: "original"}); err != nil {
		t.Fatal(err)
	}
	doc, err := src.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate one memory's content in the export. Doc-level signature still
	// verifies (because we recompute it below), but the per-memory signature
	// will fail.
	doc.Memories[0].Content = "tampered"
	// Re-sign the document so the doc-level check passes — proves the
	// per-memory signature is what catches the tamper.
	doc.Signature = nil
	signed, err := src.id.Sign(canonicalDocBytes(doc))
	if err != nil {
		t.Fatal(err)
	}
	doc.Signature = signed

	dst, _ := newTestStore(t, NullEmbedder{})
	_, err = dst.Import(ctx, doc, ImportOptions{})
	if !errors.Is(err, ErrSignatureFailed) {
		t.Fatalf("expected ErrSignatureFailed, got %v", err)
	}
}

func TestImportSkipVerificationOpensTamperedDoc(t *testing.T) {
	src, _ := newTestStore(t, NullEmbedder{})
	ctx := context.Background()
	if _, err := src.Write(ctx, WriteRequest{Kind: KindFact, Content: "original"}); err != nil {
		t.Fatal(err)
	}
	doc, err := src.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doc.Memories[0].Content = "tampered"
	dst, _ := newTestStore(t, NullEmbedder{})
	res, err := dst.Import(ctx, doc, ImportOptions{SkipVerification: true})
	if err != nil {
		t.Fatalf("expected SkipVerification to succeed, got %v", err)
	}
	if res.Imported != 1 {
		t.Errorf("imported %d, want 1", res.Imported)
	}
}

func TestImportRejectsUnknownFormat(t *testing.T) {
	src, _ := newTestStore(t, NullEmbedder{})
	ctx := context.Background()
	doc, err := src.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doc.Format = "daimon-export-v999"
	dst, _ := newTestStore(t, NullEmbedder{})
	_, err = dst.Import(ctx, doc, ImportOptions{})
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("expected ErrUnknownFormat, got %v", err)
	}
}

func TestSigningInputCanonicalForm(t *testing.T) {
	// Two memories with identical id/content/metadata must produce identical
	// SigningInput regardless of how Memory was constructed.
	a := &Memory{ID: "X", Content: "hello", Metadata: json.RawMessage(`{"a":1}`)}
	b := &Memory{ID: "X", Content: "hello", Metadata: json.RawMessage(`{"a":1}`)}
	if string(a.SigningInput()) != string(b.SigningInput()) {
		t.Fatal("SigningInput is not deterministic for equal inputs")
	}
	// Different content must change the input.
	c := &Memory{ID: "X", Content: "HELLO", Metadata: json.RawMessage(`{"a":1}`)}
	if string(a.SigningInput()) == string(c.SigningInput()) {
		t.Fatal("SigningInput failed to distinguish content case")
	}
}
