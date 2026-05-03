package memory

import "context"

// Embedder produces a fixed-dimension vector representation of text for
// semantic search. Implementations may call out to a local model (Ollama) or
// a remote API.
//
// In v0.1, the default is NullEmbedder — search falls back to substring match.
// The Ollama-backed embedder lands alongside the provider primitive.
type Embedder interface {
	// Embed returns a vector for text. Returning a nil/empty slice signals
	// that this embedder does not produce vectors (e.g. NullEmbedder); Search
	// will then fall back to text matching.
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dimensions reports the vector size produced by Embed. Zero means
	// "no vectors" — callers should treat this embedder as text-only.
	Dimensions() int

	// Name identifies the model so memories embedded by mismatched models
	// can be excluded from cosine search. v0.1 uses this only for telemetry.
	Name() string
}

// NullEmbedder is the default. It produces no vectors; semantic search
// degrades to substring match. Used when no embedding model is configured
// (per SPEC §11: "Fallback if Ollama absent — semantic search disabled;
// key-value memory still functions").
type NullEmbedder struct{}

func (NullEmbedder) Embed(_ context.Context, _ string) ([]float32, error) { return nil, nil }
func (NullEmbedder) Dimensions() int                                      { return 0 }
func (NullEmbedder) Name() string                                         { return "null" }
