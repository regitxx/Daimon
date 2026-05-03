package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/regitxx/Daimon/internal/identity"

	_ "modernc.org/sqlite" // pure-Go driver (CGO-free)
)

// Store is the local memory database for a single principal. It binds an
// SQLite database file to an Identity that signs every write.
//
// At-rest confidentiality (SPEC §5.1) is provided by application-level row
// encryption: content, metadata, and source columns are AES-256-GCM ciphertext
// with per-row random nonces and AAD bound to (memoryID, fieldName). The
// 32-byte AES key is derived from the bound identity's Ed25519 seed via
// HKDF-SHA256 with label MemoryEncryptionKeyLabel — see crypto.go. The id,
// timestamps, kind, embedding, and signature columns remain in clear (the
// signature is over plaintext id||content||metadata; embeddings are a
// one-way function of plaintext content; ids/timestamps/kinds are needed for
// indexing without unlock). Pure-Go SQLite (modernc.org/sqlite) is preserved.
//
// SQLCipher remains a v0.2+ option if page-level guarantees ever become
// load-bearing; the seam is `Open`, identical to the path SQLCipher would use.
type Store struct {
	db  *sql.DB
	id  *identity.Identity
	e   Embedder
	key []byte // 32-byte AES key derived from the bound identity; nil disables encryption
}

const schema = `
CREATE TABLE IF NOT EXISTS memories (
    id          TEXT PRIMARY KEY,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    kind        TEXT NOT NULL,
    content     TEXT NOT NULL,
    metadata    TEXT,
    embedding   BLOB,
    source      TEXT,
    signature   BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memories_kind    ON memories(kind);
CREATE INDEX IF NOT EXISTS idx_memories_created ON memories(created_at);
`

// Open opens (or creates) the memory store at path, bound to the given identity.
// If embedder is nil, NullEmbedder is used. The path may be ":memory:" for tests.
//
// The row-encryption key is derived from the identity's Ed25519 seed via HKDF
// (MemoryEncryptionKeyLabel) and held in process memory for the life of the
// Store. The same identity will rederive the same key on subsequent opens,
// allowing rows written in one process to be read in the next.
func Open(path string, id *identity.Identity, embedder Embedder) (*Store, error) {
	if id == nil {
		return nil, errors.New("memory: identity is required")
	}
	if embedder == nil {
		embedder = NullEmbedder{}
	}
	key, err := id.DeriveSubkey(MemoryEncryptionKeyLabel, rowKeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive memory key: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open memory store: %w", err)
	}
	// Single connection avoids contention on small WAL-less stores; for
	// in-memory DBs it also keeps the schema alive for the life of the Store.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db, id: id, e: embedder, key: key}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// DID returns the DID of the bound identity.
func (s *Store) DID() string { return s.id.DID() }

// WriteRequest is the input to Write. Metadata is canonicalized to deterministic
// JSON before signing and storage.
type WriteRequest struct {
	Kind     Kind
	Content  string
	Metadata map[string]any
	Source   string
}

// Write signs a new memory and persists it. The returned Memory includes the
// generated ID, timestamps, canonical metadata bytes, embedding, and signature.
func (s *Store) Write(ctx context.Context, req WriteRequest) (*Memory, error) {
	if !req.Kind.Valid() {
		return nil, ErrInvalidKind
	}
	if req.Content == "" {
		return nil, ErrEmptyContent
	}

	metaBytes, err := canonicalizeMetadata(req.Metadata)
	if err != nil {
		return nil, err
	}

	vec, err := s.e.Embed(ctx, req.Content)
	if err != nil {
		return nil, fmt.Errorf("embed content: %w", err)
	}

	now := time.Now().UnixMilli()
	mem := &Memory{
		ID:        newULID(now),
		CreatedAt: now,
		UpdatedAt: now,
		Kind:      req.Kind,
		Content:   req.Content,
		Metadata:  metaBytes,
		Embedding: encodeVector(vec),
		Source:    req.Source,
	}
	sig, err := s.id.Sign(mem.SigningInput())
	if err != nil {
		return nil, fmt.Errorf("sign memory: %w", err)
	}
	mem.Signature = sig

	contentCT, err := encryptField(s.key, []byte(mem.Content), mem.ID, "content")
	if err != nil {
		return nil, fmt.Errorf("encrypt content: %w", err)
	}
	metaCT, err := encryptField(s.key, metaBytes, mem.ID, "metadata")
	if err != nil {
		return nil, fmt.Errorf("encrypt metadata: %w", err)
	}
	sourceCT, err := encryptField(s.key, []byte(mem.Source), mem.ID, "source")
	if err != nil {
		return nil, fmt.Errorf("encrypt source: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `
        INSERT INTO memories (id, created_at, updated_at, kind, content, metadata, embedding, source, signature)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, mem.CreatedAt, mem.UpdatedAt, string(mem.Kind),
		nullableBytes(contentCT), nullableBytes(metaCT), nullableBytes(mem.Embedding),
		nullableBytes(sourceCT), mem.Signature,
	); err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}
	return mem, nil
}

// Read returns the memory with the given ID. The signature is verified against
// the bound identity's public key; ErrSignatureFailed is returned on tamper.
func (s *Store) Read(ctx context.Context, id string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE id = ?`, id)
	mem, err := s.scanMemory(row)
	if err != nil {
		return nil, err
	}
	if !s.id.Verify(mem.SigningInput(), mem.Signature) {
		return nil, ErrSignatureFailed
	}
	return mem, nil
}

// Delete removes the memory with the given ID. Returns whether a row was deleted.
func (s *Store) Delete(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete memory: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListOptions filters and pages a List call.
type ListOptions struct {
	Kind  Kind // empty = all kinds
	Limit int  // 0 = default 100
}

// List returns memories ordered by created_at DESC. Signatures are verified;
// any row that fails verification is returned in the slice along with a
// joined error so the caller can decide what to do.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]*Memory, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	q := selectColumns
	args := []any{}
	if opts.Kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, string(opts.Kind))
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	var out []*Memory
	var sigErrs []error
	for rows.Next() {
		mem, err := s.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if !s.id.Verify(mem.SigningInput(), mem.Signature) {
			sigErrs = append(sigErrs, fmt.Errorf("%w: id=%s", ErrSignatureFailed, mem.ID))
			continue
		}
		out = append(out, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(sigErrs) > 0 {
		return out, errors.Join(sigErrs...)
	}
	return out, nil
}

// --- internal helpers ---

const selectColumns = `SELECT id, created_at, updated_at, kind, content, metadata, embedding, source, signature FROM memories`

// scanRow is satisfied by both *sql.Row and *sql.Rows.
type scanRow interface {
	Scan(dest ...any) error
}

// scanMemory reads one row and decrypts content / metadata / source in place
// using the Store's row-encryption key. The returned Memory has plaintext
// fields ready for signature verification and consumption by callers.
//
// A row that fails to decrypt under the bound key surfaces ErrInvalidCiphertext;
// the caller treats this the same as a tamper or foreign-store mismatch.
func (s *Store) scanMemory(r scanRow) (*Memory, error) {
	var (
		mem        Memory
		kindStr    string
		contentCT  []byte
		metaCT     []byte
		embed      []byte
		sourceCT   []byte
		nullSource sql.RawBytes // unused; kept for column-count parity
	)
	_ = nullSource
	err := r.Scan(
		&mem.ID, &mem.CreatedAt, &mem.UpdatedAt, &kindStr,
		&contentCT, &metaCT, &embed, &sourceCT, &mem.Signature,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan memory: %w", err)
	}
	mem.Kind = Kind(kindStr)

	contentPT, err := decryptField(s.key, contentCT, mem.ID, "content")
	if err != nil {
		return nil, fmt.Errorf("decrypt content for id=%s: %w", mem.ID, err)
	}
	mem.Content = string(contentPT)

	metaPT, err := decryptField(s.key, metaCT, mem.ID, "metadata")
	if err != nil {
		return nil, fmt.Errorf("decrypt metadata for id=%s: %w", mem.ID, err)
	}
	if len(metaPT) > 0 {
		mem.Metadata = metaPT
	}

	sourcePT, err := decryptField(s.key, sourceCT, mem.ID, "source")
	if err != nil {
		return nil, fmt.Errorf("decrypt source for id=%s: %w", mem.ID, err)
	}
	mem.Source = string(sourcePT)

	if len(embed) > 0 {
		mem.Embedding = embed
	}
	return &mem, nil
}

func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newULID(unixMillis int64) string {
	return ulid.MustNew(uint64(unixMillis), rand.Reader).String()
}
