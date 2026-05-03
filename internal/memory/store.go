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
// In v0.1 the file is plain SQLite (CGO-free, fast iteration). SQLCipher slots
// in here without changing the public API: swap the driver and pass the
// passkey-derived key in via the connection string. The schema, write path,
// and signature semantics stay identical.
type Store struct {
	db *sql.DB
	id *identity.Identity
	e  Embedder
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
func Open(path string, id *identity.Identity, embedder Embedder) (*Store, error) {
	if id == nil {
		return nil, errors.New("memory: identity is required")
	}
	if embedder == nil {
		embedder = NullEmbedder{}
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
	return &Store{db: db, id: id, e: embedder}, nil
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

	if _, err := s.db.ExecContext(ctx, `
        INSERT INTO memories (id, created_at, updated_at, kind, content, metadata, embedding, source, signature)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, mem.CreatedAt, mem.UpdatedAt, string(mem.Kind),
		mem.Content, nullableBytes(metaBytes), nullableBytes(mem.Embedding),
		nullableString(mem.Source), mem.Signature,
	); err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}
	return mem, nil
}

// Read returns the memory with the given ID. The signature is verified against
// the bound identity's public key; ErrSignatureFailed is returned on tamper.
func (s *Store) Read(ctx context.Context, id string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE id = ?`, id)
	mem, err := scanMemory(row)
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
		mem, err := scanMemory(rows)
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

func scanMemory(r scanRow) (*Memory, error) {
	var (
		mem     Memory
		kindStr string
		meta    []byte
		embed   []byte
		source  sql.NullString
	)
	err := r.Scan(
		&mem.ID, &mem.CreatedAt, &mem.UpdatedAt, &kindStr,
		&mem.Content, &meta, &embed, &source, &mem.Signature,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan memory: %w", err)
	}
	mem.Kind = Kind(kindStr)
	if len(meta) > 0 {
		mem.Metadata = meta
	}
	if len(embed) > 0 {
		mem.Embedding = embed
	}
	if source.Valid {
		mem.Source = source.String
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
