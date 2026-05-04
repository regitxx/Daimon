// Package lmstudio implements the LM Studio chat adapter.
//
// LM Studio is a desktop GUI for running local LLMs that exposes an
// OpenAI-compatible HTTP server on localhost:1234. The wire format is the
// OpenAI Chat Completions API (POST /v1/chat/completions) — distinct from the
// Responses API the existing internal/provider/openai adapter targets, hence a
// separate package rather than a flag on openai.
//
// Adding LM Studio as a fourth v0.1.x adapter (after Claude, OpenAI, and
// Ollama) broadens "switch to local" beyond Ollama: many users prefer LM
// Studio's GUI for model management, and forcing them to install Ollama just
// to use Daimon is a wedge that doesn't need to exist.
//
// The adapter mirrors internal/provider/ollama in structure: probe at
// construction (GET /v1/models), gate registration on a successful probe,
// harvest the loaded model list live, no SDK dependency, raw HTTP/JSON.
//
// Auth: LM Studio's local server doesn't authenticate by default, but some
// configurations reject requests without an Authorization header. The adapter
// always sends `Authorization: Bearer <key>`; the key defaults to the
// placeholder string "lm-studio" and is overridden via WithAPIKey when the
// caller has the LMSTUDIO_API_KEY env var set. Lowest-friction default.
//
// Streaming, tool use, and structured outputs land in subsequent versions
// alongside the corresponding spec changes.
package lmstudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/regitxx/Daimon/internal/provider"
)

const (
	// Name is the provider identifier used in daimon.provider.invoke.
	Name = "lmstudio"

	// DefaultEndpoint is LM Studio's default OpenAI-compatible server URL.
	DefaultEndpoint = "http://localhost:1234"

	// DefaultAPIKey is the placeholder bearer token sent when no
	// LMSTUDIO_API_KEY is configured. LM Studio's default config ignores the
	// header value entirely; the placeholder exists so configurations that
	// require *some* bearer (a recent LM Studio option) still work without
	// further setup.
	DefaultAPIKey = "lm-studio"

	// DefaultMaxTokens is what we send when the caller does not supply
	// MaxTokens. Mirrors the other v0.1 adapters.
	DefaultMaxTokens = 1024

	// DefaultTimeout is the per-request HTTP deadline. Generous because the
	// first call after model load can take many seconds.
	DefaultTimeout = 5 * time.Minute

	chatPath   = "/v1/chat/completions"
	modelsPath = "/v1/models"
)

// Errors specific to the LM Studio adapter.
var (
	ErrEmptyMessages = errors.New("lmstudio: messages must contain at least one entry")
)

// Adapter implements provider.Provider against an LM Studio local server.
type Adapter struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
	models     []provider.Model
}

// Option configures an Adapter at construction.
type Option func(*Adapter)

// WithEndpoint overrides the LM Studio server URL. Primarily for tests against
// httptest.NewServer; in production LMSTUDIO_HOST handling lives in the
// caller (see cmd/daimond/main.go).
func WithEndpoint(url string) Option {
	return func(a *Adapter) { a.endpoint = url }
}

// WithAPIKey overrides the bearer token sent on every request. When unset the
// adapter sends DefaultAPIKey; configurations that require a specific key
// pass it via this option (driven by LMSTUDIO_API_KEY in main).
func WithAPIKey(key string) Option {
	return func(a *Adapter) { a.apiKey = key }
}

// WithHTTPClient overrides the HTTP client (e.g. to inject a custom transport
// or shorter timeout in tests).
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) { a.httpClient = c }
}

// WithModels overrides the model list discovered by the probe. Useful for
// tests that want to assert against a fixed list, or for callers that already
// know what's loaded and want to skip the probe call.
func WithModels(m []provider.Model) Option {
	return func(a *Adapter) { a.models = m }
}

// New constructs an Adapter and probes the local LM Studio server once via
// /v1/models. The probe is the registration gate — if LM Studio is not
// running, not reachable, or returns an unexpected response, New returns an
// error and the caller skips registration. The probe also populates the model
// list so daimon.provider.list reports what is actually loaded on this
// machine.
//
// An empty model list (LM Studio up, no model loaded) is NOT an error: the
// adapter constructs successfully, and the caller can decide whether to
// register an adapter that advertises zero models.
func New(ctx context.Context, opts ...Option) (*Adapter, error) {
	a := &Adapter{
		endpoint:   DefaultEndpoint,
		apiKey:     DefaultAPIKey,
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
	for _, opt := range opts {
		opt(a)
	}
	// If WithModels was used, skip the live probe — caller has asserted what
	// is available. Otherwise probe /v1/models to populate the list and to
	// confirm the server is reachable.
	if a.models == nil {
		models, err := a.probeModels(ctx)
		if err != nil {
			return nil, fmt.Errorf("probe: %w", err)
		}
		a.models = models
	}
	return a, nil
}

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return Name }

// Models returns the list of model IDs this adapter exposes. Populated at
// construction from /v1/models (or via WithModels). Returns a defensive copy.
func (a *Adapter) Models() []provider.Model {
	out := make([]provider.Model, len(a.models))
	copy(out, a.models)
	return out
}

// Invoke posts the normalized request to /v1/chat/completions and translates
// the response back to provider.Response.
func (a *Adapter) Invoke(ctx context.Context, req provider.Request) (*provider.Response, error) {
	if len(req.Messages) == 0 {
		return nil, ErrEmptyMessages
	}
	if req.Model == "" && len(a.models) > 0 {
		req.Model = a.models[0].ID
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}

	body := chatRequest{
		Model:       req.Model,
		Messages:    toChatMessages(req.System, req.Messages),
		Stream:      false,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stop:        req.StopSeqs,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+chatPath, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("lmstudio: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("lmstudio: http %d: %s", resp.StatusCode, truncateForError(respBody))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("lmstudio: decode response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("lmstudio: response error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("lmstudio: response has no choices")
	}
	choice := parsed.Choices[0]

	return &provider.Response{
		Model:      parsed.Model,
		Content:    choice.Message.Content,
		StopReason: normalizeStopReason(choice.FinishReason),
		Usage: provider.Usage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
		},
		Raw: respBody,
	}, nil
}

// probeModels GETs /v1/models and returns the loaded models translated to
// provider.Model. The probe is the daimon's "is LM Studio reachable?" check:
// any failure here propagates as a New() error so the caller can skip
// registration cleanly.
func (a *Adapter) probeModels(ctx context.Context) ([]provider.Model, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint+modelsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Send the bearer on the probe too — same header policy as Invoke, in
	// case the configuration has the auth requirement enabled.
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncateForError(body))
	}
	var parsed modelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	out := make([]provider.Model, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, provider.Model{
			ID:          m.ID,
			DisplayName: m.ID,
		})
	}
	// Stable ordering for deterministic provider.list output across runs.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// --- internal types matching the Chat Completions wire format ---

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []chatChoice       `json:"choices"`
	Usage   chatUsage          `json:"usage"`
	Error   *chatErrorPayload  `json:"error,omitempty"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatErrorPayload struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type modelsResponse struct {
	Object string         `json:"object"`
	Data   []modelsEntry  `json:"data"`
}

type modelsEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// --- helpers ---

// toChatMessages prepends the system prompt as an inline {role:"system"}
// message when present. Chat Completions follows the OpenAI convention of an
// inline system role; the normalised Request.System field plumbs cleanly via
// per-adapter mapping.
func toChatMessages(system string, in []provider.Message) []chatMessage {
	out := make([]chatMessage, 0, len(in)+1)
	if system != "" {
		out = append(out, chatMessage{Role: "system", Content: system})
	}
	for _, m := range in {
		out = append(out, chatMessage{Role: string(m.Role), Content: m.Content})
	}
	return out
}

// normalizeStopReason maps Chat Completions finish_reason onto the normalised
// enum.
//
// Observed values:
//   - "stop"           — model produced an EOS token OR matched a stop seq
//   - "length"         — hit max_tokens
//   - "content_filter" — server-side filter intervened
//   - "tool_calls"     — model emitted tool calls (not surfaced in v0.1)
//   - ""/null          — older or non-conforming servers; treat as Other
//
// As with Ollama, "stop" doesn't distinguish natural EOS from a matched stop
// sequence; both map to StopReasonEndTurn. Callers needing the distinction
// inspect Request.StopSeqs against the returned content themselves.
func normalizeStopReason(reason string) provider.StopReason {
	switch reason {
	case "stop":
		return provider.StopReasonEndTurn
	case "length":
		return provider.StopReasonMaxTokens
	case "tool_calls", "function_call":
		return provider.StopReasonToolUse
	default:
		return provider.StopReasonOther
	}
}

func truncateForError(b []byte) string {
	const max = 512
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...[truncated]"
}
