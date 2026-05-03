package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/memory/ollama"
)

// capturedReq records what the server saw for one request.
type capturedReq struct {
	Method  string
	Path    string
	Headers http.Header
	Body    embedReqBody
}

type embedReqBody struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// stubServer returns an httptest.Server that mimics Ollama's /api/embed
// endpoint. The supplied vectorize function maps an input string to a vector;
// returning nil from vectorize causes the server to emit `embeddings: [[]]`,
// which exercises the empty-embedding error path.
func stubServer(t *testing.T, vectorize func(input string) []float32, captures *[]capturedReq) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body embedReqBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if captures != nil {
			mu.Lock()
			*captures = append(*captures, capturedReq{
				Method:  r.Method,
				Path:    r.URL.Path,
				Headers: r.Header.Clone(),
				Body:    body,
			})
			mu.Unlock()
		}
		vec := vectorize(body.Input)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Use a manual encoder so [[]] (empty inner array) is emitted faithfully
		// when vec is nil.
		if vec == nil {
			_, _ = io.WriteString(w, `{"model":"`+body.Model+`","embeddings":[[]]}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":      body.Model,
			"embeddings": [][]float32{vec},
		})
	}))
}

// constantVec returns a vectorize function that emits the same vector for
// every input.
func constantVec(v []float32) func(string) []float32 {
	return func(string) []float32 { return v }
}

// oneHotVec encodes whichever substring matches first into a 3-dim one-hot.
// Used by the integration test to produce deterministic, orthogonal vectors
// without coupling test logic to the actual embedding model.
func oneHotVec(input string) []float32 {
	switch {
	case strings.Contains(input, "alpha"):
		return []float32{1, 0, 0}
	case strings.Contains(input, "beta"):
		return []float32{0, 1, 0}
	case strings.Contains(input, "gamma"):
		return []float32{0, 0, 1}
	default:
		// Probe text and any other content: stable but distinct from the
		// content vectors, so cosine ranks the content vectors above the
		// neutral one.
		return []float32{0.5, 0.5, 0.5}
	}
}

func TestNew_DefaultsAndProbe(t *testing.T) {
	var captures []capturedReq
	srv := stubServer(t, constantVec([]float32{0.1, 0.2, 0.3, 0.4}), &captures)
	defer srv.Close()

	e, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if e.Name() != ollama.DefaultModel {
		t.Errorf("Name() = %q, want %q", e.Name(), ollama.DefaultModel)
	}
	if e.Dimensions() != 4 {
		t.Errorf("Dimensions() = %d, want 4 (probed)", e.Dimensions())
	}
	if len(captures) != 1 {
		t.Fatalf("expected exactly 1 probe request, got %d", len(captures))
	}
	got := captures[0]
	if got.Method != http.MethodPost {
		t.Errorf("probe method = %q, want POST", got.Method)
	}
	if got.Path != "/api/embed" {
		t.Errorf("probe path = %q, want /api/embed", got.Path)
	}
	if got.Body.Model != ollama.DefaultModel {
		t.Errorf("probe model = %q, want %q", got.Body.Model, ollama.DefaultModel)
	}
	if got.Body.Input == "" {
		t.Error("probe input must not be empty")
	}
	if ct := got.Headers.Get("Content-Type"); ct != "application/json" {
		t.Errorf("probe Content-Type = %q, want application/json", ct)
	}
}

func TestNew_WithModelOverride(t *testing.T) {
	var captures []capturedReq
	srv := stubServer(t, constantVec([]float32{0.1, 0.2}), &captures)
	defer srv.Close()

	e, err := ollama.New(context.Background(),
		ollama.WithEndpoint(srv.URL),
		ollama.WithModel("mxbai-embed-large"),
	)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if e.Name() != "mxbai-embed-large" {
		t.Errorf("Name() = %q, want mxbai-embed-large", e.Name())
	}
	if captures[0].Body.Model != "mxbai-embed-large" {
		t.Errorf("request model = %q, want mxbai-embed-large", captures[0].Body.Model)
	}
}

func TestNew_WithHTTPClient(t *testing.T) {
	srv := stubServer(t, constantVec([]float32{1, 2}), nil)
	defer srv.Close()
	custom := &http.Client{} // zero-timeout sentinel; we just want to prove it's used
	e, err := ollama.New(context.Background(),
		ollama.WithEndpoint(srv.URL),
		ollama.WithHTTPClient(custom),
	)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if e.Dimensions() != 2 {
		t.Errorf("Dimensions() = %d, want 2", e.Dimensions())
	}
}

func TestNew_ProbeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err == nil {
		t.Fatal("expected probe error from 404")
	}
	if !strings.Contains(err.Error(), "http 404") {
		t.Errorf("expected status 404 in error, got %v", err)
	}
}

func TestNew_ProbeNetworkError(t *testing.T) {
	// Closed server URL — connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	_, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err == nil {
		t.Fatal("expected probe network error against closed server")
	}
}

func TestNew_ProbeEmptyEmbedding(t *testing.T) {
	srv := stubServer(t, constantVec(nil), nil) // emits embeddings:[[]]
	defer srv.Close()
	_, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if !errors.Is(err, ollama.ErrEmptyEmbedding) {
		t.Fatalf("expected ErrEmptyEmbedding, got %v", err)
	}
}

func TestEmbed_HappyPath(t *testing.T) {
	var captures []capturedReq
	srv := stubServer(t, func(input string) []float32 {
		// Probe gets [0.1, 0.2, 0.3]; subsequent calls get [0.9, 0.8, 0.7].
		// Returning the same dimension keeps Embed happy.
		if strings.Contains(input, "probe") {
			return []float32{0.1, 0.2, 0.3}
		}
		return []float32{0.9, 0.8, 0.7}
	}, &captures)
	defer srv.Close()

	e, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	vec, err := e.Embed(context.Background(), "real input")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if got, want := vec, []float32{0.9, 0.8, 0.7}; !floatSliceEqual(got, want) {
		t.Errorf("vector = %v, want %v", got, want)
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 server calls (probe + embed), got %d", len(captures))
	}
	if captures[1].Body.Input != "real input" {
		t.Errorf("embed input = %q, want %q", captures[1].Body.Input, "real input")
	}
}

func TestEmbed_EmptyInputReturnsNilWithoutNetwork(t *testing.T) {
	var captures []capturedReq
	srv := stubServer(t, constantVec([]float32{1, 2, 3}), &captures)
	defer srv.Close()

	e, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	probeCount := len(captures)
	vec, err := e.Embed(context.Background(), "")
	if err != nil {
		t.Fatalf("embed empty: %v", err)
	}
	if vec != nil {
		t.Errorf("expected nil vector for empty input, got %v", vec)
	}
	if len(captures) != probeCount {
		t.Errorf("empty input must not trigger an HTTP call; got %d new calls", len(captures)-probeCount)
	}
}

func TestEmbed_DimensionMismatch(t *testing.T) {
	var calls int
	srv := stubServer(t, func(string) []float32 {
		calls++
		if calls == 1 {
			return []float32{1, 2, 3, 4} // probe: 4-dim
		}
		return []float32{1, 2} // subsequent: 2-dim, mismatch
	}, nil)
	defer srv.Close()

	e, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if e.Dimensions() != 4 {
		t.Fatalf("probe dim = %d, want 4", e.Dimensions())
	}
	_, err = e.Embed(context.Background(), "anything")
	if !errors.Is(err, ollama.ErrDimensionMismatch) {
		t.Fatalf("expected ErrDimensionMismatch, got %v", err)
	}
}

func TestEmbed_HTTPError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Successful probe.
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"model":"x","embeddings":[[1,2,3]]}`)
			return
		}
		http.Error(w, `{"error":"server overloaded"}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	e, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = e.Embed(context.Background(), "real input")
	if err == nil {
		t.Fatal("expected http error")
	}
	if !strings.Contains(err.Error(), "http 503") {
		t.Errorf("expected http 503 in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "server overloaded") {
		t.Errorf("expected upstream message in error, got %v", err)
	}
}

func TestEmbed_ContextCancellation(t *testing.T) {
	// Probe succeeds quickly; then a second handler blocks until the request
	// context is done. The Embed call uses an already-cancelled context so the
	// http client returns immediately with a context error.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"model":"x","embeddings":[[1,2]]}`)
			return
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	e, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.Embed(ctx, "anything")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// TestIntegration_StoreAndSearch exercises the full path: a memory.Store bound
// to an Ollama-backed Embedder, three writes, and a search whose ranking is
// determined by the stub's one-hot vectors. Proves the embedder satisfies
// memory.Embedder and the cosine-search path engages instead of substring
// fallback.
func TestIntegration_StoreAndSearch(t *testing.T) {
	srv := stubServer(t, oneHotVec, nil)
	defer srv.Close()

	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	emb, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}
	if emb.Dimensions() != 3 {
		t.Fatalf("expected 3-dim probe, got %d", emb.Dimensions())
	}

	store, err := memory.Open(":memory:", id, emb)
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	defer store.Close()

	contents := []string{
		"alpha is the first Greek letter",
		"beta is the second Greek letter",
		"gamma is the third Greek letter",
	}
	ctx := context.Background()
	for _, c := range contents {
		if _, err := store.Write(ctx, memory.WriteRequest{
			Kind:    memory.KindFact,
			Content: c,
			Source:  "test",
		}); err != nil {
			t.Fatalf("write %q: %v", c, err)
		}
	}

	// Search for "beta only" — query embeds to [0,1,0]. Cosine ranks the beta
	// memory at 1.0 and the others at 0.
	results, err := store.Search(ctx, "beta only", memory.SearchOptions{Limit: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result on cosine path")
	}
	if !strings.Contains(results[0].Memory.Content, "beta") {
		t.Errorf("top result for 'beta' query was %q; expected the beta memory", results[0].Memory.Content)
	}
	if results[0].Score < 0.99 {
		t.Errorf("expected near-1.0 cosine for one-hot match, got %f", results[0].Score)
	}

	// And a query that doesn't lexically match any content but embeds to a
	// known vector — search must still return cosine-ranked results, proving
	// we're not on the substring fallback.
	results, err = store.Search(ctx, "alpha only", memory.SearchOptions{Limit: 3})
	if err != nil {
		t.Fatalf("search alpha: %v", err)
	}
	if len(results) == 0 || !strings.Contains(results[0].Memory.Content, "alpha") {
		t.Errorf("top result for 'alpha' query was unexpected: %+v", results)
	}
}

// floatSliceEqual is an exact equality check sufficient for our deterministic
// stub vectors.
func floatSliceEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
