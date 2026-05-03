// Package claude implements the Anthropic Messages API adapter.
//
// The adapter speaks raw HTTP/JSON against api.anthropic.com — no SDK
// dependency. The whole surface is the v1/messages POST: take a normalized
// provider.Request, translate to Anthropic's wire format, post, translate the
// response back. Streaming, tool use, and image content blocks land in
// follow-on iterations.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/regitxx/Daimon/internal/provider"
)

const (
	// Name is the provider identifier used in daimon.provider.invoke.
	Name = "claude"

	// DefaultEndpoint is Anthropic's production Messages API.
	DefaultEndpoint = "https://api.anthropic.com/v1/messages"

	// DefaultAPIVersion is the anthropic-version header. Pinned so the wire
	// format under us doesn't shift; bump deliberately.
	DefaultAPIVersion = "2023-06-01"

	// DefaultMaxTokens is what we send when the caller does not supply
	// MaxTokens. Anthropic requires the field; we pick a conservative value
	// that fits typical chat replies without truncating.
	DefaultMaxTokens = 1024

	// DefaultTimeout is the per-request HTTP deadline. Generous because
	// long generations under load can run tens of seconds.
	DefaultTimeout = 90 * time.Second
)

// Errors specific to the Claude adapter.
var (
	ErrEmptyAPIKey   = errors.New("claude: api key is empty")
	ErrEmptyMessages = errors.New("claude: messages must contain at least one entry")
)

// Adapter implements provider.Provider against Anthropic's Messages API.
type Adapter struct {
	apiKey     string
	endpoint   string
	apiVersion string
	httpClient *http.Client
	models     []provider.Model
}

// Option configures an Adapter at construction.
type Option func(*Adapter)

// WithEndpoint overrides the API endpoint. Primarily for tests against
// httptest.NewServer.
func WithEndpoint(url string) Option {
	return func(a *Adapter) { a.endpoint = url }
}

// WithHTTPClient overrides the HTTP client. Useful for custom transports,
// per-test timeouts, or for injecting a recording client.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) { a.httpClient = c }
}

// WithAPIVersion overrides the anthropic-version header.
func WithAPIVersion(v string) Option {
	return func(a *Adapter) { a.apiVersion = v }
}

// WithModels overrides the default model list.
func WithModels(m []provider.Model) Option {
	return func(a *Adapter) { a.models = m }
}

// New constructs an Adapter. apiKey is required.
func New(apiKey string, opts ...Option) (*Adapter, error) {
	if apiKey == "" {
		return nil, ErrEmptyAPIKey
	}
	a := &Adapter{
		apiKey:     apiKey,
		endpoint:   DefaultEndpoint,
		apiVersion: DefaultAPIVersion,
		httpClient: &http.Client{Timeout: DefaultTimeout},
		models:     defaultModels(),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// Name returns the adapter identifier.
func (a *Adapter) Name() string { return Name }

// Models returns the list of model IDs this adapter exposes. The list is
// static in v0.1; a future version may hit GET /v1/models for dynamic
// discovery.
func (a *Adapter) Models() []provider.Model {
	out := make([]provider.Model, len(a.models))
	copy(out, a.models)
	return out
}

// Invoke posts the normalized request to /v1/messages and translates the
// response back to provider.Response.
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

	body := messagesRequest{
		Model:         req.Model,
		MaxTokens:     maxTokens,
		System:        req.System,
		Messages:      toAnthropicMessages(req.Messages),
		Temperature:   req.Temperature,
		StopSequences: req.StopSeqs,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("claude: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("claude: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", a.apiVersion)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("claude: do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("claude: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("claude: http %d: %s", resp.StatusCode, truncateForError(respBody))
	}

	var parsed messagesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("claude: decode response: %w", err)
	}

	var content bytes.Buffer
	for _, block := range parsed.Content {
		if block.Type == "text" {
			content.WriteString(block.Text)
		}
	}

	return &provider.Response{
		Model:      parsed.Model,
		Content:    content.String(),
		StopReason: normalizeStopReason(parsed.StopReason),
		Usage: provider.Usage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
		},
		Raw: respBody,
	}, nil
}

// --- internal types matching the Anthropic Messages API wire format ---

type messagesRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	Temperature   *float64           `json:"temperature,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      usageBlock     `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usageBlock struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- helpers ---

func toAnthropicMessages(in []provider.Message) []anthropicMessage {
	out := make([]anthropicMessage, len(in))
	for i, m := range in {
		out[i] = anthropicMessage{Role: string(m.Role), Content: m.Content}
	}
	return out
}

func normalizeStopReason(s string) provider.StopReason {
	switch s {
	case "end_turn":
		return provider.StopReasonEndTurn
	case "max_tokens":
		return provider.StopReasonMaxTokens
	case "stop_sequence":
		return provider.StopReasonStopSequence
	case "tool_use":
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

// defaultModels lists the v0.1 default Claude model offerings.
//
// IDs only — Context and MaxOutput are deliberately omitted because hard-
// coding model-specific token counts that may be wrong is worse than omitting
// them. A future iteration can hit GET /v1/models or carry a vetted manifest.
func defaultModels() []provider.Model {
	return []provider.Model{
		{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
		{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5"},
	}
}
