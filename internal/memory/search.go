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
// fails to embed, Search degrades to a keyword-based fallback (see
// searchKeyword) — case-insensitive token overlap with stopword filtering
// and a recency tiebreak. This is intentionally weaker than cosine but
// dramatically better than the old whole-string substring match, which
// produced matched=0 for any non-literal query like "What kind of code
// should I write today?" against "I like type-safe Go" — even though
// both contain the token "code". (Surfaced in the 2026-05-25 dogfood
// session: the killer "provider-portable memory" demo silently failed
// for everyone without Ollama running.)
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
		return s.searchKeyword(ctx, query, opts.Kind, limit)
	}
	qVec, err := s.e.Embed(ctx, query)
	if err != nil || len(qVec) == 0 {
		return s.searchKeyword(ctx, query, opts.Kind, limit)
	}
	return s.searchCosine(ctx, qVec, opts.Kind, limit)
}

// searchKeyword loads candidate rows (filtered by kind) and scores each by
// token overlap with the query. With application-level row encryption
// (SPEC §5.1), SQL LIKE against the content column would match ciphertext
// bytes — useless — so all scoring happens in Go after decrypt. The cost is
// one full-table scan + decrypt per query when no embedder is configured;
// for v0.1 single-user scale this is well under 10ms for thousands of rows.
//
// Scoring model (intentionally simple — cosine via Ollama is still the
// recommended path; this just makes things WORK without it):
//
//  1. Tokenize the query: split on whitespace + punctuation, lowercase,
//     drop a short stopword list ("what", "the", "i", "is", "am", etc.).
//  2. For each candidate, tokenize the content the same way.
//  3. Score = fraction of query tokens present in content tokens.
//     Substring containment counts too ("write" matches "writing").
//  4. Return rows with score > 0, sorted by score descending, recency tiebreak.
//
// If the query has zero useful tokens after stopword removal (e.g. "Who am
// I?" or "What's up?"), return the most recent rows so the LLM at least
// sees the daimon's existing knowledge — far better than matched=0.
func (s *Store) searchKeyword(ctx context.Context, query string, kind Kind, limit int) ([]SearchResult, error) {
	q := selectColumns
	args := []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, string(kind))
	}
	q += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search keyword: %w", err)
	}
	defer rows.Close()

	queryTokens := tokenize(query)
	// "Who am I?"-style empty-after-stopwords query: surface recent facts
	// instead of nothing. The LLM is much better off seeing 10 recent
	// memories than seeing zero context.
	allTokensEmpty := len(queryTokens) == 0

	var (
		out     []SearchResult
		sigErrs []error
	)
	for rows.Next() {
		mem, err := s.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if !s.id.Verify(mem.SigningInput(), mem.Signature) {
			sigErrs = append(sigErrs, fmt.Errorf("%w: id=%s", ErrSignatureFailed, mem.ID))
			continue
		}

		var score float64
		if allTokensEmpty {
			// Recency-only score; we'll re-sort by created_at via the
			// SQL ORDER BY DESC and just take the first `limit` rows.
			// Score 1.0 so the context-layer recency boost dominates
			// without us double-counting.
			score = 1.0
		} else {
			score = keywordScore(queryTokens, mem.Content)
			if score == 0 {
				continue
			}
		}
		out = append(out, SearchResult{Memory: mem, Score: score})
		if allTokensEmpty && len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Keyword path: sort by score descending (rows are already created_at DESC
	// from SQL, which preserves recency as a stable tiebreak via sort.Slice).
	if !allTokensEmpty {
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Score > out[j].Score
		})
		if len(out) > limit {
			out = out[:limit]
		}
	}

	if len(sigErrs) > 0 {
		return out, errors.Join(sigErrs...)
	}
	return out, nil
}

// tokenize splits a string on whitespace + ASCII punctuation, lowercases each
// token, and drops common English stopwords + tokens of length <= 1. The
// stopword list is deliberately small: aggressive filtering would hurt more
// than it helps for the kind of short personal facts daimon stores.
func tokenize(s string) []string {
	if s == "" {
		return nil
	}
	// Replace common punctuation with spaces so Fields splits cleanly.
	mapper := func(r rune) rune {
		switch r {
		case '.', ',', ';', ':', '!', '?', '"', '\'', '(', ')', '[', ']', '{', '}', '/', '\\', '-':
			return ' '
		}
		return r
	}
	cleaned := strings.Map(mapper, strings.ToLower(s))
	raw := strings.Fields(cleaned)
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if len(t) <= 1 {
			continue
		}
		if _, stop := stopwords[t]; stop {
			continue
		}
		out = append(out, t)
	}
	return out
}

// keywordScore returns the fraction of queryTokens that appear in content
// (either as exact tokens or as substrings of tokens). Substring matching
// makes "write" hit "writing" and "code" hit "coding" without needing a
// full stemmer. Score is in [0, 1].
func keywordScore(queryTokens []string, content string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	contentTokens := tokenize(content)
	if len(contentTokens) == 0 {
		return 0
	}
	matched := 0
	for _, q := range queryTokens {
		for _, c := range contentTokens {
			if c == q || strings.Contains(c, q) || strings.Contains(q, c) {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(queryTokens))
}

// stopwords is a small list of English function-words filtered out before
// keyword scoring. Kept short on purpose — for personal facts ("I prefer
// concise explanations"), aggressive stopword removal hurts more than helps.
// The list covers the obvious "Who am I?" / "What is X?" template words.
var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {}, "if": {},
	"of": {}, "to": {}, "in": {}, "on": {}, "at": {}, "for": {}, "by": {},
	"with": {}, "as": {}, "from": {},
	"is": {}, "am": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"do": {}, "does": {}, "did": {}, "have": {}, "has": {}, "had": {},
	"will": {}, "would": {}, "should": {}, "could": {}, "can": {}, "may": {}, "might": {},
	"i": {}, "me": {}, "my": {}, "mine": {}, "you": {}, "your": {}, "yours": {},
	"he": {}, "she": {}, "it": {}, "we": {}, "they": {}, "them": {}, "their": {},
	"this": {}, "that": {}, "these": {}, "those": {},
	"what": {}, "who": {}, "whom": {}, "whose": {}, "which": {}, "where": {}, "when": {}, "why": {}, "how": {},
	"so": {}, "too": {}, "very": {}, "just": {}, "now": {}, "then": {}, "than": {},
	"no": {}, "not": {}, "yes": {}, "all": {}, "any": {}, "some": {}, "few": {}, "many": {},
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

