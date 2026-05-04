// Package ollama implements the Ollama chat adapter.
//
// The adapter speaks raw HTTP/JSON against a local Ollama server — no SDK
// dependency. The whole surface is the /api/chat POST: take a normalized
// provider.Request, translate to Ollama's wire format, post, translate the
// response back.
//
// SPEC §7.2 ships Ollama as the third v0.1 adapter, alongside Claude
// (Anthropic Messages) and OpenAI (Responses). Wire-shape-wise it's closer to
// OpenAI's Chat Completions than to either of those: messages are an array of
// {role, content}, the system prompt enters as an inline message with role
// "system", and generation parameters (temperature, max tokens, stop seqs)
// live in a nested options object. Streaming, tool use, and multimodal content
// land in subsequent versions and require both spec and interface changes.
//
// Unlike Claude/OpenAI, Ollama has no API key. New() probes the local server
// once at construction via /api/tags so callers can decide whether to register
// the adapter at all — a daimon that advertises an unreachable provider via
// daimon.provider.list undermines the "switch Claude → GPT → local Llama
// mid-task" pitch the registry is for. The probe also harvests the locally-
// pulled model list, so Models() reflects what is actually callable rather
// than a hardcoded guess.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/provider"
)

const (
	// Name is the provider identifier used in daimon.provider.invoke.
	Name = "ollama"

	// DefaultEndpoint is the Ollama server's default localhost address.
	// Matches internal/memory/ollama (the embedder talks to the same daemon).
	DefaultEndpoint = "http://localhost:11434"

	// DefaultMaxTokens is what we send when the caller does not supply
	// MaxTokens. Mirrors the Claude and OpenAI adapters — a conservative
	// default that fits typical chat replies without truncating. Ollama
	// treats num_predict=-1 as "unlimited"; we set a finite ceiling so a
	// runaway generation can't burn the whole context window.
	DefaultMaxTokens = 1024

	// DefaultTimeout is the per-request HTTP deadline. Ollama's first call
	// after process start can take many seconds while the model loads from
	// disk into RAM/VRAM; subsequent calls are faster. Generous on purpose.
	DefaultTimeout = 5 * time.Minute

	chatPath = "/api/chat"
	tagsPath = "/api/tags"
)

// Errors specific to the Ollama chat adapter.
var (
	// ErrEmptyMessages is returned by Invoke when the request carries no
	// messages. Ollama would reject the request anyway; we fail fast.
	ErrEmptyMessages = errors.New("ollama: messages must contain at least one entry")
)

// Adapter implements provider.Provider against a local Ollama server.
type Adapter struct {
	endpoint   string
	httpClient *http.Client
	models     []provider.Model
}

// Option configures an Adapter at construction.
type Option func(*Adapter)

// WithEndpoint overrides the Ollama server URL. Primarily for tests against
// httptest.NewServer; in production OLLAMA_HOST handling lives in the caller
// (see cmd/daimond/main.go).
func WithEndpoint(url string) Option {
	return func(a *Adapter) { a.endpoint = url }
}

// WithHTTPClient overrides the HTTP client (e.g. to inject a custom transport
// or shorter timeout in tests).
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) { a.httpClient = c }
}

// WithModels overrides the model list discovered by the probe. Useful for
// tests that want to assert against a fixed list, or for callers that already
// know what's installed and want to skip the probe call.
func WithModels(m []provider.Model) Option {
	return func(a *Adapter) { a.models = m }
}

// New constructs an Adapter and probes the local Ollama server once via
// /api/tags. The probe is the registration gate — if Ollama is not running,
// not reachable, or returns an unexpected response, New returns an error and
// the caller skips registration. The probe also populates the model list so
// daimon.provider.list reports what is actually callable on this machine.
//
// An empty model list (Ollama up, no models pulled) is NOT an error: the
// adapter constructs successfully, and the caller can decide whether to
// register an adapter that advertises zero models.
func New(ctx context.Context, opts ...Option) (*Adapter, error) {
	a := &Adapter{
		endpoint:   DefaultEndpoint,
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
	for _, opt := range opts {
		opt(a)
	}
	// If WithModels was used, skip the live probe — caller has asserted what
	// is available. Otherwise probe /api/tags to populate the list and to
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
// construction from /api/tags (or via WithModels). Returns a defensive copy.
func (a *Adapter) Models() []provider.Model {
	out := make([]provider.Model, len(a.models))
	copy(out, a.models)
	return out
}

// Invoke posts the normalized request to /api/chat and translates the response
// back to provider.Response.
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
		Model:    req.Model,
		Messages: toOllamaMessages(req.System, req.Messages),
		Stream:   false,
		Options:  buildOptions(req.Temperature, maxTokens, req.StopSeqs),
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+chatPath, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama: http %d: %s", resp.StatusCode, truncateForError(respBody))
	}

	var parsed chatResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}
	// Ollama can return {"error": "..."} on a 200 (rare, but observed when
	// the model name validates but loading fails partway). Surface it.
	if parsed.Error != "" {
		return nil, fmt.Errorf("ollama: response error: %s", parsed.Error)
	}

	return &provider.Response{
		Model:      parsed.Model,
		Content:    parsed.Message.Content,
		StopReason: normalizeStopReason(parsed.DoneReason, parsed.Done),
		Usage: provider.Usage{
			InputTokens:  parsed.PromptEvalCount,
			OutputTokens: parsed.EvalCount,
		},
		Raw: respBody,
	}, nil
}

// Stream posts the normalized request to /api/chat with stream:true, parses
// the NDJSON line-by-line, sends each non-empty content fragment as a Delta,
// and returns the accumulated provider.Response when the terminal frame
// (done:true) arrives. Honours ctx via http.NewRequestWithContext — cancelling
// ctx mid-stream causes the upstream Read to fail with the ctx error and
// Stream returns promptly.
//
// The deltas channel is closed before Stream returns under all paths
// (success, error, context cancellation) so the consumer's range loop
// terminates cleanly. Channel ownership: caller creates and consumes; Stream
// is sole sender and sole closer.
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

	body := chatRequest{
		Model:    req.Model,
		Messages: toOllamaMessages(req.System, req.Messages),
		Stream:   true,
		Options:  buildOptions(req.Temperature, maxTokens, req.StopSeqs),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+chatPath, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded prefix for the error message; don't try to consume
		// an arbitrary error body.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("ollama: http %d: %s", resp.StatusCode, truncateForError(errBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Tokens of substantial size occasionally arrive on the same NDJSON line
	// (long single-token outputs, or merged buffer flushes). 1 MiB is the
	// same ceiling chat-session loading uses.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		accumulated strings.Builder
		final       chatResponse
		lastRaw     []byte
		seenTerminal bool
	)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		// Bail out if the caller cancelled — don't burn through buffered
		// upstream bytes the user no longer wants.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var frame chatResponse
		if err := json.Unmarshal(line, &frame); err != nil {
			return nil, fmt.Errorf("ollama: decode stream frame: %w (line: %s)", err, truncateForError(line))
		}
		if frame.Error != "" {
			return nil, fmt.Errorf("ollama: stream error: %s", frame.Error)
		}
		// Record the most recent raw frame so the final Response can carry it
		// — Ollama's terminal frame is the one with the usage counters and
		// done_reason that callers care about.
		lastRaw = append(lastRaw[:0], line...)

		if frame.Message.Content != "" {
			accumulated.WriteString(frame.Message.Content)
			// Block until the consumer takes the delta or ctx fires; this is
			// the back-pressure point that lets a slow CLI throttle Ollama.
			select {
			case deltas <- provider.Delta{Content: frame.Message.Content}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if frame.Done {
			final = frame
			seenTerminal = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		// ctx cancellation surfaces here as a wrapped net error; prefer the
		// ctx error for caller clarity.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("ollama: read stream: %w", err)
	}
	if !seenTerminal {
		return nil, errors.New("ollama: stream ended without done:true frame")
	}

	return &provider.Response{
		Model:      final.Model,
		Content:    accumulated.String(),
		StopReason: normalizeStopReason(final.DoneReason, final.Done),
		Usage: provider.Usage{
			InputTokens:  final.PromptEvalCount,
			OutputTokens: final.EvalCount,
		},
		Raw: append(json.RawMessage(nil), lastRaw...),
	}, nil
}

// probeModels GETs /api/tags and returns the locally-pulled models translated
// to provider.Model. The probe is the daimon's "is Ollama reachable?" check:
// any failure here propagates as a New() error so the caller can skip
// registration cleanly.
func (a *Adapter) probeModels(ctx context.Context) ([]provider.Model, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint+tagsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
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
	var parsed tagsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	out := make([]provider.Model, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		if m.Name == "" {
			continue
		}
		out = append(out, provider.Model{
			ID:          m.Name,
			DisplayName: m.Name,
		})
	}
	// Stable ordering for deterministic provider.list output across runs.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// --- internal types matching the Ollama /api/chat and /api/tags wire format ---

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *chatOptions    `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatOptions maps to Ollama's nested generation parameters. Only fields the
// daimon's normalized Request can populate are present; the underlying API
// supports many more (top_k, top_p, repeat_penalty, mirostat, …) that v0.1
// does not surface.
type chatOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type chatResponse struct {
	Model           string        `json:"model"`
	CreatedAt       string        `json:"created_at"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	DoneReason      string        `json:"done_reason,omitempty"`
	PromptEvalCount int           `json:"prompt_eval_count,omitempty"`
	EvalCount       int           `json:"eval_count,omitempty"`
	Error           string        `json:"error,omitempty"`
}

type tagsResponse struct {
	Models []tagsModel `json:"models"`
}

type tagsModel struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at,omitempty"`
	Size       int64  `json:"size,omitempty"`
}

// --- helpers ---

// toOllamaMessages prepends the system prompt as an inline {role:"system"}
// message when present. Anthropic puts system outside the message list,
// OpenAI Responses uses an `instructions` field, Ollama follows the OpenAI
// Chat Completions convention of an inline system role — the normalised
// Request.System field plumbs cleanly into either via per-adapter mapping.
func toOllamaMessages(system string, in []provider.Message) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(in)+1)
	if system != "" {
		out = append(out, ollamaMessage{Role: "system", Content: system})
	}
	for _, m := range in {
		out = append(out, ollamaMessage{Role: string(m.Role), Content: m.Content})
	}
	return out
}

// buildOptions returns nil when none of the configurable knobs differ from
// defaults — keeps the wire payload tidy and avoids sending num_predict=0
// (which Ollama interprets as "no tokens").
func buildOptions(temp *float64, maxTokens int, stop []string) *chatOptions {
	if temp == nil && maxTokens <= 0 && len(stop) == 0 {
		return nil
	}
	return &chatOptions{
		Temperature: temp,
		NumPredict:  maxTokens,
		Stop:        stop,
	}
}

// normalizeStopReason maps Ollama's done_reason onto the normalized enum.
//
// Ollama's done_reason values observed in the wild:
//   - "stop"   — model produced an EOS token OR matched a stop sequence
//   - "length" — hit num_predict limit
//   - "load"   — model loaded but did not generate (rare; treat as Other)
//   - ""       — older Ollama builds omit done_reason; fall back to Done flag
//
// Note that Ollama does not distinguish "natural EOS" from "matched stop
// sequence" the way Anthropic does — both surface as done_reason="stop". v0.1
// maps both to StopReasonEndTurn; callers needing the distinction should
// consult Request.StopSeqs and inspect the returned content themselves.
func normalizeStopReason(reason string, done bool) provider.StopReason {
	switch reason {
	case "stop":
		return provider.StopReasonEndTurn
	case "length":
		return provider.StopReasonMaxTokens
	case "":
		if done {
			return provider.StopReasonEndTurn
		}
		return provider.StopReasonOther
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
