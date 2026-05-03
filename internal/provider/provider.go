// Package provider defines the abstraction Daimon uses to talk to LLM
// providers. v0.1 ships the Claude adapter (internal/provider/claude); OpenAI
// and Ollama follow.
//
// The interface is deliberately minimal: a provider Name, a list of Models it
// exposes, and a single Invoke entrypoint. Streaming, tool use, and multimodal
// content arrive in subsequent versions and require both spec and interface
// changes.
package provider

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	// ErrUnsupportedModel is returned by Invoke when req.Model is not in the
	// provider's Models() list. Adapters MAY accept any model the provider
	// itself accepts (some providers add models continuously); this error
	// exists for adapters that strictly validate.
	ErrUnsupportedModel = errors.New("provider: unsupported model")
)

// Role is the speaker of a Message in a conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single turn in a conversation. v0.1 is text-only; image/audio
// content lands when SPEC §7 grows multimodal types.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request is the normalized invocation payload. Each adapter translates this
// to its provider's native API.
//
// Anthropic takes the system prompt outside the message list; OpenAI uses a
// system role inside it; Ollama mirrors OpenAI. We keep System as a top-level
// field so adapters can place it correctly without losing information.
//
// MaxTokens=0 means "adapter chooses a sensible default". Temperature is a
// pointer so 0 is distinguishable from "not set".
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	System      string    `json:"system,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	StopSeqs    []string  `json:"stop_sequences,omitempty"`
}

// StopReason normalizes provider-specific stop reasons into a small enum.
// Adapters that see an unrecognised reason return StopReasonOther so callers
// can detect novelty without parsing strings.
type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
	StopReasonToolUse      StopReason = "tool_use"
	StopReasonOther        StopReason = "other"
)

// Usage is token accounting for a single Invoke. Both fields are 0 when the
// provider does not report usage.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is the normalized return shape.
//
// Raw carries the provider's full original response body. Useful for
// debugging and for clients that need provider-specific fields the
// normalised shape doesn't expose. Adapters MAY omit it; v0.1 includes it.
type Response struct {
	Model      string          `json:"model"`
	Content    string          `json:"content"`
	StopReason StopReason      `json:"stop_reason"`
	Usage      Usage           `json:"usage"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

// Model describes one model offering of a provider. Context and MaxOutput are
// optional (0 means "unspecified") — adapters whose providers expose model
// metadata via API may populate them dynamically.
type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Context     int    `json:"context,omitempty"`
	MaxOutput   int    `json:"max_output,omitempty"`
}

// Provider is the adapter contract every LLM integration implements.
//
// Name must be stable and unique across registered adapters; clients address
// providers by name in daimon.provider.invoke. Models is a snapshot of the
// adapter's exposed model list at the time of the call. Invoke is the single
// entrypoint for inference; it MUST honour ctx cancellation.
type Provider interface {
	Name() string
	Models() []Model
	Invoke(ctx context.Context, req Request) (*Response, error)
}
