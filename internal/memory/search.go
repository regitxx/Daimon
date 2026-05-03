package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// SearchOptions controls Search behavior.
type SearchOptions struct {
	Kind  Kind // empty = all kinds
	Limit int  // 0 = default 10
}

// SearchResult pairs a memory with its similarity score (in [0,1] for the
// cosine path; coarse 1.0/0.5/0.0 for the substring fallback).
type SearchResult struct {
	Memory *Memory
	Score  float64
}

// Search returns the top-K most relevant memories for the given query.
//
// If the bound embedder produces vectors and the query embeds successfully,
// Search uses cosine similarity over rows whose stored embedding has matching
// dimensions. Rows with no embedding or mismatched dimensions are skipped from
// the cosine path.
//
// If the embedder is text-only (NullEmbedder, Dimensions()==0) or the query
// fails to embed, Search degrades to a case-insensitive substring match.
//
// The recency-weighted retrieval policy from SPEC §11
// (0.7·cosine + 0.3·exp(-age/30d)) is the responsibility of a higher-level
// context layer. This function exposes raw cosine.
func (s *Store) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	if s.e.Dimensions() == 0 {
		return s.searchSubstring(ctx, query, opts.Kind, limit)
	}
	qVec, err := s.e.Embed(ctx, query)
	if err != nil || len(qVec) == 0 {
		return s.searchSubstring(ctx, query, opts.Kind, limit)
	}
	return s.searchCosine(ctx, qVec, opts.Kind, limit)
}

// searchSubstring loads candidate rows (filtered by kind) and substring-matches
// the query against decrypted content in Go. With application-level row
// encryption (SPEC §5.1), SQL LIKE against the content column would match
// ciphertext bytes — useless. The cost is one full-table scan + decrypt per
// query when no embedder is configured; for v0.1 single-user scale this is
// well under 10ms even for thousands of rows. The cosine path remains the
// recommended retrieval whenever an embedder is available.
func (s *Store) searchSubstring(ctx context.Context, query string, kind Kind, limit int) ([]SearchResult, error) {
	q := selectColumns
	args := []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, string(kind))
	}
	q += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search substring: %w", err)
	}
	defer rows.Close()

	needle := strings.ToLower(query)
	var out []SearchResult
	var sigErrs []error
	for rows.Next() {
		mem, err := s.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(strings.ToLower(mem.Content), needle) {
			continue
		}
		if !s.id.Verify(mem.SigningInput(), mem.Signature) {
			sigErrs = append(sigErrs, fmt.Errorf("%w: id=%s", ErrSignatureFailed, mem.ID))
			continue
		}
		// Coarse score: 1.0 if the query appears as a whole word, 0.5 otherwise.
		score := 0.5
		if containsWord(mem.Content, query) {
			score = 1.0
		}
		out = append(out, SearchResult{Memory: mem, Score: score})
		if len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(sigErrs) > 0 {
		return out, errors.Join(sigErrs...)
	}
	return out, nil
}

func (s *Store) searchCosine(ctx context.Context, qVec []float32, kind Kind, limit int) ([]SearchResult, error) {
	q := selectColumns + ` WHERE embedding IS NOT NULL`
	args := []any{}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, string(kind))
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search cosine: %w", err)
	}
	defer rows.Close()

	var (
		results []SearchResult
		sigErrs []error
		qNorm   = norm(qVec)
	)
	for rows.Next() {
		mem, err := s.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		v := mem.Vector()
		if len(v) != len(qVec) {
			// Different embedding dimension — produced by a different model.
			// Skip from cosine search; SPEC §5.3 requires per-row tolerance.
			continue
		}
		if !s.id.Verify(mem.SigningInput(), mem.Signature) {
			sigErrs = append(sigErrs, fmt.Errorf("%w: id=%s", ErrSignatureFailed, mem.ID))
			continue
		}
		score := cosine(qVec, v, qNorm)
		results = append(results, SearchResult{Memory: mem, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	if len(sigErrs) > 0 {
		return results, errors.Join(sigErrs...)
	}
	return results, nil
}

func cosine(a, b []float32, aNorm float64) float64 {
	if aNorm == 0 {
		return 0
	}
	bNorm := norm(b)
	if bNorm == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot / (aNorm * bNorm)
}

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func containsWord(content, query string) bool {
	c := strings.ToLower(content)
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	for _, tok := range strings.Fields(c) {
		if strings.Trim(tok, ".,;:!?\"'()[]{}") == q {
			return true
		}
	}
	return false
}
