package memory

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/regitxx/Daimon/internal/identity"
)

// ExportFormat is the format tag emitted in every export. Future format
// changes bump the version and this package gains a compatibility shim.
const ExportFormat = "daimon-export-v1"

// ExportDocument is the on-the-wire form of a memory export per SPEC §5.4.
//
// Two signatures are at play:
//
//   - Per-memory signatures inside Memories (as stored at write time, by the
//     daimon that originally wrote them).
//   - A document-level Signature over the canonical JSON of this struct with
//     its own Signature field cleared. This binds the memory set to the DID
//     and the export timestamp.
//
// Note (v0.1 limitation): the canonical form is "Go's encoding/json applied to
// this struct with Signature nil". Stable enough for Go-to-Go round trips, not
// stable enough for cross-language SDK interop. A move to RFC 8785 JCS is
// tracked for v0.1.x — pending the TS/Python SDKs that would need it.
type ExportDocument struct {
	Format     string    `json:"format"`
	DID        string    `json:"did"`
	ExportedAt int64     `json:"exported_at"`
	Memories   []*Memory `json:"memories"`
	Activity   []any     `json:"activity"` // empty in v0.1; activity-log primitive lands next
	Signature  []byte    `json:"signature,omitempty"`
}

// Export gathers all memories into a signed ExportDocument.
func (s *Store) Export(ctx context.Context) (*ExportDocument, error) {
	memories, err := s.List(ctx, ListOptions{Limit: 1_000_000})
	if err != nil {
		return nil, fmt.Errorf("export: list memories: %w", err)
	}

	doc := &ExportDocument{
		Format:     ExportFormat,
		DID:        s.id.DID(),
		ExportedAt: time.Now().UnixMilli(),
		Memories:   memories,
		Activity:   []any{},
	}
	signed, err := s.id.Sign(canonicalDocBytes(doc))
	if err != nil {
		return nil, fmt.Errorf("export: sign document: %w", err)
	}
	doc.Signature = signed
	return doc, nil
}

// ImportOptions controls Import behavior.
//
// Field names are inverted from "default behavior" so the zero value of
// ImportOptions does the safe, idempotent thing: verify all signatures, skip
// rows whose IDs already exist.
type ImportOptions struct {
	// SkipVerification disables signature checks. Unsafe; intended only for
	// migration tooling that has independently established trust.
	SkipVerification bool

	// FailOnDuplicate makes a duplicate ID an error instead of a silent skip.
	FailOnDuplicate bool
}

// ImportResult reports per-memory outcome.
type ImportResult struct {
	Imported int
	Skipped  int
}

// Import ingests an ExportDocument. Signatures are verified against the DID
// embedded in the document (cross-principal imports are permitted by SPEC §5.4
// when same-principal verification fails; v0.1 simply accepts any export whose
// signatures verify and lets policy live above this layer).
func (s *Store) Import(ctx context.Context, doc *ExportDocument, opts ImportOptions) (ImportResult, error) {
	verify := !opts.SkipVerification
	skipExisting := !opts.FailOnDuplicate

	if doc.Format != ExportFormat {
		return ImportResult{}, fmt.Errorf("%w: %q", ErrUnknownFormat, doc.Format)
	}
	pub, err := identity.DecodeDIDKey(doc.DID)
	if err != nil {
		return ImportResult{}, fmt.Errorf("decode export DID: %w", err)
	}

	if verify {
		if err := verifyDocSignature(doc, pub); err != nil {
			return ImportResult{}, err
		}
	}

	res := ImportResult{}
	for _, mem := range doc.Memories {
		if mem == nil {
			continue
		}
		if verify {
			if !ed25519.Verify(pub, mem.SigningInput(), mem.Signature) {
				return res, fmt.Errorf("%w: id=%s", ErrSignatureFailed, mem.ID)
			}
		}
		ok, err := s.insertImported(ctx, mem, skipExisting)
		if err != nil {
			return res, err
		}
		if ok {
			res.Imported++
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

func (s *Store) insertImported(ctx context.Context, mem *Memory, skipExisting bool) (bool, error) {
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO memories (id, created_at, updated_at, kind, content, metadata, embedding, source, signature)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, mem.CreatedAt, mem.UpdatedAt, string(mem.Kind),
		mem.Content, nullableBytes(mem.Metadata), nullableBytes(mem.Embedding),
		nullableString(mem.Source), mem.Signature,
	)
	if err == nil {
		return true, nil
	}
	if skipExisting && isUniqueViolation(err) {
		return false, nil
	}
	return false, fmt.Errorf("import insert: %w", err)
}

func isUniqueViolation(err error) bool {
	// modernc.org/sqlite returns errors whose Error() text contains
	// "UNIQUE constraint failed" — sufficient for v0.1 without taking
	// a typed dependency on the driver's error codes.
	return err != nil && containsUnique(err.Error())
}

func containsUnique(s string) bool {
	const needle = "UNIQUE constraint failed"
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// canonicalDocBytes returns the canonical JSON encoding of doc with Signature
// cleared. This is the byte sequence that doc.Signature signs.
func canonicalDocBytes(doc *ExportDocument) []byte {
	cp := *doc
	cp.Signature = nil
	b, err := json.Marshal(&cp)
	if err != nil {
		// Marshaling a struct of plain types should not fail; if it does
		// we'd rather crash loudly than silently sign garbage.
		panic(fmt.Sprintf("memory: canonicalize export document: %v", err))
	}
	return b
}

func verifyDocSignature(doc *ExportDocument, pub ed25519.PublicKey) error {
	if len(doc.Signature) == 0 {
		return errors.New("export: missing document signature")
	}
	signed := doc.Signature
	if !ed25519.Verify(pub, canonicalDocBytes(doc), signed) {
		return fmt.Errorf("%w: document", ErrSignatureFailed)
	}
	return nil
}
