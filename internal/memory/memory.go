// Package memory implements the Daimon memory primitive.
//
// Memory is a signed, encrypted store of facts, preferences, observations,
// and task state belonging to the principal. Every write is signed by the
// daimon's identity key. See SPEC.md §5.
//
// v0.1 scope of this package:
//
//   - Schema per SPEC §5.2 (id / created_at / updated_at / kind / content /
//     metadata / embedding / source / signature)
//   - Per-row Ed25519 signatures, verified on every read
//   - Cosine-similarity search over locally-stored embeddings
//   - Signed export/import roundtrip per SPEC §5.4
//
// Deliberately deferred:
//
//   - SQLCipher at-rest encryption (CGO; will swap in via the same Open path)
//   - sqlite-vec extension (using O(n) cosine in Go; fine for v0.1 scale)
//   - Real Ollama embedder (Embedder interface is in place; NullEmbedder ships)
package memory

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Kind enumerates valid memory kinds (SPEC §5.2).
type Kind string

const (
	KindFact        Kind = "fact"
	KindPreference  Kind = "preference"
	KindTask        Kind = "task"
	KindObservation Kind = "observation"
)

func (k Kind) Valid() bool {
	switch k {
	case KindFact, KindPreference, KindTask, KindObservation:
		return true
	}
	return false
}

// Common errors.
var (
	ErrInvalidKind     = errors.New("memory: invalid kind")
	ErrEmptyContent    = errors.New("memory: empty content")
	ErrNotFound        = errors.New("memory: not found")
	ErrSignatureFailed = errors.New("memory: signature verification failed")
	ErrUnknownFormat   = errors.New("memory: unknown export format")
	ErrDIDMismatch     = errors.New("memory: export DID does not match enclosed key")
)

// Memory is a single record. Field tags govern its on-the-wire form for
// export/import. The DB column layout matches SPEC §5.2 one-to-one.
type Memory struct {
	ID        string          `json:"id"`
	CreatedAt int64           `json:"created_at"` // unix milliseconds
	UpdatedAt int64           `json:"updated_at"`
	Kind      Kind            `json:"kind"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`  // canonical JSON bytes
	Embedding []byte          `json:"embedding,omitempty"` // little-endian float32s
	Source    string          `json:"source,omitempty"`
	Signature []byte          `json:"signature"` // Ed25519 over SigningInput()
}

// SigningInput is the canonical byte sequence covered by Signature.
//
// Format (v0.1, domain-separated to prevent collision with other Daimon
// signature surfaces):
//
//	"daimon-memory-v1\x00" || id || "\x00" || content || "\x00" || metadata
//
// Where metadata is the canonical JSON bytes (sorted keys, no whitespace) that
// were stored at write time. If no metadata was provided, this segment is empty
// while the preceding separator is preserved so the encoding is unambiguous.
func (m *Memory) SigningInput() []byte {
	var buf bytes.Buffer
	buf.Grow(len("daimon-memory-v1") + 1 + len(m.ID) + 1 + len(m.Content) + 1 + len(m.Metadata))
	buf.WriteString("daimon-memory-v1")
	buf.WriteByte(0x00)
	buf.WriteString(m.ID)
	buf.WriteByte(0x00)
	buf.WriteString(m.Content)
	buf.WriteByte(0x00)
	buf.Write(m.Metadata)
	return buf.Bytes()
}

// Vector decodes Embedding into a []float32 for similarity math. Returns nil
// if no embedding is present.
func (m *Memory) Vector() []float32 {
	return decodeVector(m.Embedding)
}

func encodeVector(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	if len(b) < 4 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// canonicalizeMetadata produces the byte-stable JSON form of arbitrary
// user-supplied metadata. Go's encoding/json sorts map keys, which gives us a
// deterministic encoding sufficient for v0.1 (Go-to-Go interop). Cross-language
// SDKs will need a stricter canonicalization (e.g. RFC 8785 JCS) — tracked.
func canonicalizeMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("canonicalize metadata: %w", err)
	}
	return b, nil
}
