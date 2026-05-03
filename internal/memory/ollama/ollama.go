// Package ollama implements memory.Embedder against a local Ollama server.
//
// SPEC §5.3 specifies nomic-embed-text (768 dimensions) running locally via
// Ollama as the v0.1 default. SPEC §11's fallback policy ("if Ollama absent —
// semantic search disabled; key-value memory still functions") is the caller's
// responsibility: New() returns an error when the probe fails, and the daimon
// chooses to fall back to memory.NullEmbedder.
//
// Wire format is the modern POST /api/embed endpoint:
//
//	request:  {"model": "nomic-embed-text", "input": "text to embed"}
//	response: {"model": "...", "embeddings": [[0.1, 0.2, ...]], ...}
//
// The legacy /api/embeddings (singular, "embedding") path is not used.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultEndpoint is the Ollama server's default localhost address.
	DefaultEndpoint = "http://localhost:11434"

	// DefaultModel is the v0.1 default per SPEC §5.3.
	DefaultModel = "nomic-embed-text"

	// DefaultTimeout is the per-request HTTP deadline. Embedding is fast on
	// modest hardware (a few hundred ms) but cold-load of a model can take
	// seconds; this is generous without being unreasonable.
	DefaultTimeout = 30 * time.Second

	// probeText is what New() embeds to discover and cache the model's
	// vector dimension. Content is irrelevant; the dimension is what we need.
	probeText = "daimon-probe"

	embedPath = "/api/embed"
)

// Errors specific to this adapter.
var (
	// ErrEmptyEmbedding is returned when the server replies with a well-formed
	// JSON body but no actual vector. It usually means the model is not pulled
	// or the server returned an unexpected payload shape.
	ErrEmptyEmbedding = errors.New("ollama: response contains no embedding")

	// ErrDimensionMismatch is returned when a subsequent embed call produces a
	// vector of a different size than the one cached at construction. Cosine
	// search across mixed dimensions is meaningless, so we surface the error
	// instead of silently degrading.
	ErrDimensionMismatch = errors.New("ollama: embedding dimension changed")
)

// Embedder implements memory.Embedder by calling a local Ollama server.
type Embedder struct {
	endpoint   string
	model      string
	httpClient *http.Client
	dim        int // cached at construction via probe
}

// Option configures an Embedder at construction.
type Option func(*Embedder)

// WithEndpoint overrides the Ollama server URL. Primarily for tests against
// httptest.NewServer.
func WithEndpoint(url string) Option {
	return func(e *Embedder) { e.endpoint = url }
}

// WithModel overrides the embedding model name (e.g. "mxbai-embed-large").
func WithModel(name string) Option {
	return func(e *Embedder) { e.model = name }
}

// WithHTTPClient overrides the HTTP client (e.g. to inject a custom transport
// or shorter timeout in tests).
func WithHTTPClient(c *http.Client) Option {
	return func(e *Embedder) { e.httpClient = c }
}

// New constructs an Embedder and probes the server once to discover the model's
// vector dimension. The probe context bounds that single call; in production
// the caller usually passes context.Background() and relies on the HTTP client
// timeout.
//
// New returns a non-nil error when the probe fails — Ollama not running, model
// not pulled, network unreachable, or unexpected response shape. Callers
// implement SPEC §11's graceful-degrade by checking the error and substituting
// memory.NullEmbedder.
func New(ctx context.Context, opts ...Option) (*Embedder, error) {
	e := &Embedder{
		endpoint:   DefaultEndpoint,
		model:      DefaultModel,
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
	for _, opt := range opts {
		opt(e)
	}
	vec, err := e.embed(ctx, probeText)
	if err != nil {
		return nil, fmt.Errorf("probe: %w", err)
	}
	if len(vec) == 0 {
		return nil, ErrEmptyEmbedding
	}
	e.dim = len(vec)
	return e, nil
}

// Name returns the model identifier. The memory package uses this to tag rows
// so vectors produced by mismatched models can be excluded from cosine search.
func (e *Embedder) Name() string { return e.model }

// Dimensions returns the cached vector size. Zero would mean "no vectors", but
// New() rejects an empty probe response, so a constructed Embedder always
// reports a positive dimension.
func (e *Embedder) Dimensions() int { return e.dim }

// Embed returns a vector for text. Empty input returns (nil, nil) — matching
// memory.NullEmbedder — so search.go's empty-vector path takes over instead of
// burning a network call.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}
	vec, err := e.embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if e.dim != 0 && len(vec) != e.dim {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(vec), e.dim)
	}
	return vec, nil
}

// embed performs the raw HTTP call. The dimension check lives in Embed, not
// here, so the probe inside New() can populate the cached dimension on its
// first successful call.
func (e *Embedder) embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+embedPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama: http %d: %s", resp.StatusCode, truncateForError(respBody))
	}

	var parsed embedResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Embeddings) == 0 || len(parsed.Embeddings[0]) == 0 {
		return nil, ErrEmptyEmbedding
	}
	return parsed.Embeddings[0], nil
}

// --- internal types matching the Ollama /api/embed wire format ---

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

func truncateForError(b []byte) string {
	const max = 512
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...[truncated]"
}
