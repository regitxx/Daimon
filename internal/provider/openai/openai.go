// Package openai implements the OpenAI Responses API adapter.
//
// The adapter speaks raw HTTP/JSON against api.openai.com — no SDK dependency.
// The whole surface is the v1/responses POST: take a normalized
// provider.Request, translate to the Responses API wire format, post,
// translate the response back.
//
// SPEC §7.2 specifies the OpenAI adapter targets the Responses API with a
// future Chat Completions fallback. v0.1 ships only the Responses path; the
// fallback lands when a deployed model rejects the Responses endpoint.
//
// Tool use, structured outputs (`text.format`), and reasoning summary surfacing
// land in follow-on iterations alongside the corresponding spec changes.
// Streaming via the Responses API SSE format is implemented in Stream below
// (CHECKPOINT item 23a, second of three).
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/provider"
)

const (
	// Name is the provider identifier used in daimon.provider.invoke.
	Name = "openai"

	// DefaultEndpoint is OpenAI's production Responses API.
	DefaultEndpoint = "https://api.openai.com/v1/responses"

	// DefaultMaxTokens is what we send when the caller does not supply
	// MaxTokens. 1024 mirrors the Claude adapter — a conservative default
	// that fits typical chat replies without truncating.
	DefaultMaxTokens = 1024

	// DefaultTimeout is the per-request HTTP deadline. Generous because
	// long generations on reasoning models can run tens of seconds.
	DefaultTimeout = 90 * time.Second
)

// Errors specific to the OpenAI adapter.
var (
	ErrEmptyAPIKey   = errors.New("openai: api key is empty")
	ErrEmptyMessages = errors.New("openai: messages must contain at least one entry")
)

// Adapter implements provider.Provider against OpenAI's Responses API.
type Adapter struct {
	apiKey     string
	endpoint   string
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

// Invoke posts the normalized request to /v1/responses and translates the
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

	body := responsesRequest{
		Model:           req.Model,
		Input:           toResponsesInput(req.Messages),
		Instructions:    req.System,
		MaxOutputTokens: maxTokens,
		Temperature:     req.Temperature,
		Stop:            req.StopSeqs,
		Stream:          false,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai: http %d: %s", resp.StatusCode, truncateForError(respBody))
	}

	var parsed responsesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("openai: response error: %s", parsed.Error.Message)
	}

	var content bytes.Buffer
	for _, item := range parsed.Output {
		if item.Type != "message" {
			// Skip non-message items (e.g. reasoning summaries from o-series
			// models, tool calls). v0.1 surfaces only assistant message
			// content; reasoning surfacing and tool use land with their
			// respective spec changes.
			continue
		}
		for _, block := range item.Content {
			if block.Type == "output_text" {
				content.WriteString(block.Text)
			}
		}
	}

	return &provider.Response{
		Model:      parsed.Model,
		Content:    content.String(),
		StopReason: normalizeStopReason(parsed.Status, parsed.IncompleteDetails),
		Usage: provider.Usage{
			InputTokens:  parsed.Usage.InputTokens,
			OutputTokens: parsed.Usage.OutputTokens,
		},
		Raw: respBody,
	}, nil
}

// Stream posts the normalized request to /v1/responses with stream:true, parses
// the Server-Sent Events response, sends each response.output_text.delta as a
// Delta, and returns the accumulated provider.Response when the terminal event
// (response.completed / response.incomplete / response.failed) arrives.
//
// The events we depend on (per the OpenAI Responses API streaming spec) are
// response.created (initial event carrying response.model — captured so the
// final Response carries it even if upstream renames mid-stream), response.
// output_text.delta (the token fragment in the top-level "delta" field), and
// the terminal trio response.completed / response.incomplete / response.failed
// (each carries the full response object with status, usage, and optionally
// incomplete_details). All other events (response.in_progress, response.
// output_item.added/done, response.content_part.added/done, response.
// output_text.done, response.refusal.delta, future tool/reasoning events) are
// ignored — forward-compat for the v0.1 text-only chat surface.
//
// An "error" event aborts the stream with the carried message — the Responses
// API can emit this mid-stream when generation fails after the initial 200 OK.
//
// The deltas channel is closed before Stream returns under all paths so the
// consumer's range loop terminates cleanly. Channel ownership: caller creates
// and consumes; Stream is sole sender and sole closer. Ctx cancellation
// propagates through http.NewRequestWithContext + a per-line ctx.Err() check —
// cancelling mid-stream causes the upstream Read to fail and Stream returns
// promptly with the ctx error, exactly as the unary path does.
func (a *Adapter) Stream(ctx context.Context, req provider.Request, deltas chan<- provider.Delta) (*provider.Response, error) {
	defer close(deltas)

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

	body := responsesRequest{
		Model:           req.Model,
		Input:           toResponsesInput(req.Messages),
		Instructions:    req.System,
		MaxOutputTokens: maxTokens,
		Temperature:     req.Temperature,
		Stop:            req.StopSeqs,
		Stream:          true,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("openai: http %d: %s", resp.StatusCode, truncateForError(errBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	// SSE data: payloads are typically small (one event per token) but a long
	// single delta or a future tool_use payload could carry more. 1 MiB matches
	// the chat-session loader and the Claude/Ollama streaming adapters.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		accumulated  strings.Builder
		model        string
		status       string
		incomplete   *responsesIncompletion
		inputTokens  int
		outputTokens int
		lastRaw      []byte
		seenTerminal bool

		curEvent string
		curData  []byte
	)

	// dispatch handles one fully-assembled SSE event (event: + one or more
	// data: lines, terminated by a blank line). Returns an error if the data
	// payload fails to decode for an event we care about — malformed events
	// for ignored types do not abort the stream.
	dispatch := func() error {
		defer func() { curEvent = ""; curData = curData[:0] }()
		if curEvent == "" || len(curData) == 0 {
			return nil
		}
		// Snapshot the most recent dispatched event's raw payload for the
		// final Response.Raw — the terminal response.completed event is the
		// natural endpoint, and keeping the most recent matches what unary
		// callers see (the full response body).
		lastRaw = append(lastRaw[:0], curData...)

		switch curEvent {
		case "response.created":
			var f struct {
				Response struct {
					Model string `json:"model"`
				} `json:"response"`
			}
			if err := json.Unmarshal(curData, &f); err != nil {
				return fmt.Errorf("openai: decode response.created: %w", err)
			}
			if f.Response.Model != "" {
				model = f.Response.Model
			}
		case "response.output_text.delta":
			var f struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal(curData, &f); err != nil {
				return fmt.Errorf("openai: decode response.output_text.delta: %w", err)
			}
			if f.Delta == "" {
				return nil
			}
			accumulated.WriteString(f.Delta)
			select {
			case deltas <- provider.Delta{Content: f.Delta}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case "response.completed", "response.incomplete", "response.failed":
			var f struct {
				Response struct {
					Model             string                 `json:"model"`
					Status            string                 `json:"status"`
					Usage             responsesUsage         `json:"usage"`
					IncompleteDetails *responsesIncompletion `json:"incomplete_details,omitempty"`
					Error             *responsesErrorPayload `json:"error,omitempty"`
				} `json:"response"`
			}
			if err := json.Unmarshal(curData, &f); err != nil {
				return fmt.Errorf("openai: decode %s: %w", curEvent, err)
			}
			if f.Response.Error != nil {
				return fmt.Errorf("openai: response error: %s", f.Response.Error.Message)
			}
			if f.Response.Model != "" {
				model = f.Response.Model
			}
			if f.Response.Status != "" {
				status = f.Response.Status
			}
			if f.Response.IncompleteDetails != nil {
				incomplete = f.Response.IncompleteDetails
			}
			if f.Response.Usage.InputTokens != 0 {
				inputTokens = f.Response.Usage.InputTokens
			}
			if f.Response.Usage.OutputTokens != 0 {
				outputTokens = f.Response.Usage.OutputTokens
			}
			seenTerminal = true
		case "error":
			// Top-level mid-stream error. Some emit {"type":"error",...} with
			// fields at the top level; others nest under "error". Try both.
			var top responsesErrorPayload
			if err := json.Unmarshal(curData, &top); err == nil && top.Message != "" {
				return fmt.Errorf("openai: stream error: %s", top.Message)
			}
			var nested struct {
				Error responsesErrorPayload `json:"error"`
			}
			if err := json.Unmarshal(curData, &nested); err != nil {
				return fmt.Errorf("openai: decode error event: %w", err)
			}
			return fmt.Errorf("openai: stream error: %s", nested.Error.Message)
		}
		return nil
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			if err := dispatch(); err != nil {
				return nil, err
			}
			if seenTerminal {
				break
			}
			continue
		}
		// SSE: skip comments (lines beginning with ":") and unrecognised
		// fields (id:, retry:). We only need event: and data:.
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			curEvent = string(bytes.TrimSpace(line[len("event: "):]))
		case bytes.HasPrefix(line, []byte("event:")):
			curEvent = string(bytes.TrimSpace(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data: ")):
			if len(curData) > 0 {
				curData = append(curData, '\n')
			}
			curData = append(curData, line[len("data: "):]...)
		case bytes.HasPrefix(line, []byte("data:")):
			if len(curData) > 0 {
				curData = append(curData, '\n')
			}
			curData = append(curData, line[len("data:"):]...)
		}
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("openai: read stream: %w", err)
	}
	// Some servers omit the trailing blank line after the final event. Try
	// one last dispatch in case the terminal event is buffered.
	if !seenTerminal {
		if err := dispatch(); err != nil {
			return nil, err
		}
	}
	if !seenTerminal {
		return nil, errors.New("openai: stream ended without response.completed event")
	}

	return &provider.Response{
		Model:      model,
		Content:    accumulated.String(),
		StopReason: normalizeStopReason(status, incomplete),
		Usage: provider.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
		Raw: append(json.RawMessage(nil), lastRaw...),
	}, nil
}

// --- internal types matching the OpenAI Responses API wire format ---

type responsesRequest struct {
	Model           string           `json:"model"`
	Input           []responsesInput `json:"input"`
	Instructions    string           `json:"instructions,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	Stop            []string         `json:"stop,omitempty"`
	Stream          bool             `json:"stream,omitempty"`
}

// responsesInput is the simplified message form the Responses API accepts as
// shorthand for the typed input_text block format. Each entry carries a role
// and a string content; the API converts to the typed shape internally.
type responsesInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responsesResponse struct {
	ID                string                  `json:"id"`
	Object            string                  `json:"object"`
	Status            string                  `json:"status"`
	Model             string                  `json:"model"`
	Output            []responsesOutputItem   `json:"output"`
	Usage             responsesUsage          `json:"usage"`
	IncompleteDetails *responsesIncompletion  `json:"incomplete_details,omitempty"`
	Error             *responsesErrorPayload  `json:"error,omitempty"`
}

type responsesOutputItem struct {
	Type    string                  `json:"type"`
	Role    string                  `json:"role,omitempty"`
	Status  string                  `json:"status,omitempty"`
	Content []responsesOutputBlock  `json:"content,omitempty"`
}

type responsesOutputBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type responsesIncompletion struct {
	Reason string `json:"reason"`
}

type responsesErrorPayload struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- helpers ---

func toResponsesInput(in []provider.Message) []responsesInput {
	out := make([]responsesInput, len(in))
	for i, m := range in {
		out[i] = responsesInput{Role: string(m.Role), Content: m.Content}
	}
	return out
}

// normalizeStopReason maps Responses API status + incomplete_details onto the
// normalized StopReason enum. Responses API uses status="completed" /
// "incomplete" / "failed" / "in_progress"; for incomplete results the reason
// lives in incomplete_details.reason ("max_output_tokens" / "content_filter").
//
// The Responses API does not surface a stop_sequence-equivalent status today;
// callers that supply Request.StopSeqs and want to detect the stop-sequence
// path explicitly must inspect the returned content. If the API grows a
// dedicated status, this mapping picks it up via the explicit case.
func normalizeStopReason(status string, incomplete *responsesIncompletion) provider.StopReason {
	switch status {
	case "completed":
		return provider.StopReasonEndTurn
	case "incomplete":
		if incomplete != nil {
			switch incomplete.Reason {
			case "max_output_tokens", "max_tokens":
				return provider.StopReasonMaxTokens
			case "stop_sequence":
				return provider.StopReasonStopSequence
			}
		}
		return provider.StopReasonOther
	case "stop_sequence":
		return provider.StopReasonStopSequence
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

// defaultModels lists the v0.1 default OpenAI model offerings.
//
// IDs only — Context and MaxOutput are deliberately omitted because hard-
// coding model-specific token counts that may be wrong is worse than
// omitting them. A future iteration can hit GET /v1/models or carry a vetted
// manifest.
//
// First entry is the default when Request.Model is empty.
func defaultModels() []provider.Model {
	return []provider.Model{
		{ID: "gpt-5", DisplayName: "GPT-5"},
		{ID: "gpt-5-mini", DisplayName: "GPT-5 mini"},
		{ID: "gpt-4.1", DisplayName: "GPT-4.1"},
	}
}
