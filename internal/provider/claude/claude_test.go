package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/provider"
	"github.com/regitxx/Daimon/internal/provider/claude"
)

// claudeRequestCapture is the wire-format shape we assert against for outbound
// requests. Mirrors the unexported messagesRequest in the adapter.
type claudeRequestCapture struct {
	Model         string `json:"model"`
	MaxTokens     int    `json:"max_tokens"`
	System        string `json:"system,omitempty"`
	Messages      []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Temperature   *float64 `json:"temperature,omitempty"`
	StopSequences []string `json:"stop_sequences,omitempty"`
	Stream        bool     `json:"stream,omitempty"`
}

// fakeServer returns an httptest.Server whose handler captures the request and
// returns the supplied response body with the supplied status.
func fakeServer(t *testing.T, status int, respBody string, capture *claudeRequestCapture, capturedHeaders *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capturedHeaders != nil {
			h := r.Header.Clone()
			*capturedHeaders = h
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if capture != nil {
			if err := json.Unmarshal(body, capture); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
}

const happyResponse = `{
  "id": "msg_test",
  "type": "message",
  "role": "assistant",
  "model": "claude-opus-4-7",
  "content": [
    {"type": "text", "text": "Hello, world."}
  ],
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 12, "output_tokens": 5}
}`

func TestAdapter_NewRequiresAPIKey(t *testing.T) {
	_, err := claude.New("")
	if !errors.Is(err, claude.ErrEmptyAPIKey) {
		t.Fatalf("expected ErrEmptyAPIKey, got %v", err)
	}
}

func TestAdapter_NameAndModels(t *testing.T) {
	a, err := claude.New("sk-test")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if a.Name() != claude.Name {
		t.Fatalf("name: got %q, want %q", a.Name(), claude.Name)
	}
	models := a.Models()
	if len(models) == 0 {
		t.Fatal("expected at least one default model")
	}
	// Models() returns a copy; mutating the result must not leak back.
	models[0].ID = "tampered"
	if a.Models()[0].ID == "tampered" {
		t.Fatal("Models() must return a copy, not a reference")
	}
}

func TestAdapter_InvokeHappyPath(t *testing.T) {
	var capture claudeRequestCapture
	var headers http.Header
	srv := fakeServer(t, 200, happyResponse, &capture, &headers)
	defer srv.Close()

	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	resp, err := a.Invoke(context.Background(), provider.Request{
		Model:    "claude-opus-4-7",
		System:   "be brief",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	// Outbound request shape.
	if capture.Model != "claude-opus-4-7" {
		t.Errorf("request model: got %q", capture.Model)
	}
	if capture.MaxTokens != claude.DefaultMaxTokens {
		t.Errorf("default max_tokens: got %d, want %d", capture.MaxTokens, claude.DefaultMaxTokens)
	}
	if capture.System != "be brief" {
		t.Errorf("system: got %q", capture.System)
	}
	if len(capture.Messages) != 1 || capture.Messages[0].Role != "user" || capture.Messages[0].Content != "Hi" {
		t.Errorf("messages: got %+v", capture.Messages)
	}

	// Headers.
	if got := headers.Get("x-api-key"); got != "sk-test" {
		t.Errorf("x-api-key: got %q", got)
	}
	if got := headers.Get("anthropic-version"); got != claude.DefaultAPIVersion {
		t.Errorf("anthropic-version: got %q, want %q", got, claude.DefaultAPIVersion)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q", got)
	}

	// Normalised response.
	if resp.Content != "Hello, world." {
		t.Errorf("content: got %q", resp.Content)
	}
	if resp.Model != "claude-opus-4-7" {
		t.Errorf("response model: got %q", resp.Model)
	}
	if resp.StopReason != provider.StopReasonEndTurn {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 5 {
		t.Errorf("usage: got %+v", resp.Usage)
	}
	if len(resp.Raw) == 0 {
		t.Error("Raw should be populated")
	}
}

func TestAdapter_InvokeRequiresMessages(t *testing.T) {
	a, err := claude.New("sk-test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{Model: "claude-opus-4-7"})
	if !errors.Is(err, claude.ErrEmptyMessages) {
		t.Fatalf("expected ErrEmptyMessages, got %v", err)
	}
}

func TestAdapter_InvokeDefaultsModel(t *testing.T) {
	var capture claudeRequestCapture
	srv := fakeServer(t, 200, happyResponse, &capture, nil)
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if capture.Model != a.Models()[0].ID {
		t.Errorf("expected first model %q to be defaulted, got %q", a.Models()[0].ID, capture.Model)
	}
}

func TestAdapter_InvokeRespectsTemperatureAndStopSeqs(t *testing.T) {
	var capture claudeRequestCapture
	srv := fakeServer(t, 200, happyResponse, &capture, nil)
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.3
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:       "claude-opus-4-7",
		Messages:    []provider.Message{{Role: provider.RoleUser, Content: "x"}},
		Temperature: &temp,
		StopSeqs:    []string{"END"},
		MaxTokens:   256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Temperature == nil || *capture.Temperature != 0.3 {
		t.Errorf("temperature: %v", capture.Temperature)
	}
	if len(capture.StopSequences) != 1 || capture.StopSequences[0] != "END" {
		t.Errorf("stop_sequences: %v", capture.StopSequences)
	}
	if capture.MaxTokens != 256 {
		t.Errorf("max_tokens override: got %d, want 256", capture.MaxTokens)
	}
}

func TestAdapter_InvokeHTTPError(t *testing.T) {
	srv := fakeServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad model"}}`, nil, nil)
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "claude-opus-4-7",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from 4xx")
	}
	if !strings.Contains(err.Error(), "http 400") {
		t.Errorf("expected status code in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "bad model") {
		t.Errorf("expected upstream message in error, got %v", err)
	}
}

func TestAdapter_InvokeStopReasonNormalisation(t *testing.T) {
	cases := []struct {
		raw  string
		want provider.StopReason
	}{
		{"end_turn", provider.StopReasonEndTurn},
		{"max_tokens", provider.StopReasonMaxTokens},
		{"stop_sequence", provider.StopReasonStopSequence},
		{"tool_use", provider.StopReasonToolUse},
		{"unknown_future_reason", provider.StopReasonOther},
	}
	for _, tc := range cases {
		body := strings.Replace(happyResponse, `"stop_reason": "end_turn"`, `"stop_reason": "`+tc.raw+`"`, 1)
		srv := fakeServer(t, 200, body, nil, nil)
		a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		resp, err := a.Invoke(context.Background(), provider.Request{
			Model:    "claude-opus-4-7",
			Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
		})
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if resp.StopReason != tc.want {
			t.Errorf("%s: got %q, want %q", tc.raw, resp.StopReason, tc.want)
		}
	}
}

func TestAdapter_InvokeContextCancellation(t *testing.T) {
	// Block forever; expect ctx cancellation to propagate.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = a.Invoke(ctx, provider.Request{
		Model:    "claude-opus-4-7",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// --- streaming tests ---

// streamServer wraps httptest.NewServer with a handler that captures the
// request body, sets text/event-stream, and lets the test write the SSE
// payload directly. The Flusher is asserted because without it the test would
// silently buffer events and fail to exercise real streaming behaviour.
func streamServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, flusher http.Flusher)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not flush — test cannot stream")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		h(w, r, flusher)
	}))
}

// happySSE is the canonical Anthropic Messages SSE shape: message_start with
// model + input_tokens, three content_block_delta of type text_delta, then
// message_delta with stop_reason and cumulative output_tokens, then
// message_stop. ping / content_block_start / content_block_stop are
// interleaved to verify the parser ignores them.
const happySSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAdapter_StreamHappyPath(t *testing.T) {
	var capture claudeRequestCapture
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capture)
		_, _ = io.WriteString(w, happySSE)
		flusher.Flush()
	})
	defer srv.Close()

	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	deltas := make(chan provider.Delta, 16)
	var got []string
	done := make(chan struct{})
	go func() {
		for d := range deltas {
			got = append(got, d.Content)
		}
		close(done)
	}()

	resp, err := a.Stream(context.Background(), provider.Request{
		Model:    "claude-opus-4-7",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	<-done

	// Outbound stream:true was set on the wire.
	if !capture.Stream {
		t.Errorf("outbound stream flag: got %v, want true", capture.Stream)
	}

	wantDeltas := []string{"Hello", ", ", "world."}
	if len(got) != len(wantDeltas) {
		t.Fatalf("delta count: got %d (%v), want %d", len(got), got, len(wantDeltas))
	}
	for i, w := range wantDeltas {
		if got[i] != w {
			t.Errorf("delta[%d]: got %q, want %q", i, got[i], w)
		}
	}
	if resp.Content != "Hello, world." {
		t.Errorf("accumulated content: got %q", resp.Content)
	}
	if resp.Model != "claude-opus-4-7" {
		t.Errorf("model: got %q", resp.Model)
	}
	if resp.StopReason != provider.StopReasonEndTurn {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 12 {
		t.Errorf("input_tokens: got %d, want 12", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 4 {
		t.Errorf("output_tokens: got %d, want 4", resp.Usage.OutputTokens)
	}
	if len(resp.Raw) == 0 {
		t.Error("Raw should carry the most recent SSE data payload")
	}
}

func TestAdapter_StreamCancellation(t *testing.T) {
	// Server emits one delta, flushes, then hangs. The test cancels the
	// context once it has seen that delta and asserts Stream returns
	// promptly — no goroutine leak, no continued upstream consumption.
	gate := make(chan struct{})
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		_, _ = io.WriteString(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_x\",\"model\":\"claude-opus-4-7\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"first\"}}\n\n")
		flusher.Flush()
		<-gate
		<-r.Context().Done()
	})
	defer srv.Close()
	defer close(gate)

	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	deltas := make(chan provider.Delta, 4)
	go func() {
		<-deltas
		cancel()
		for range deltas {
		}
	}()

	doneAt := make(chan time.Time, 1)
	go func() {
		_, _ = a.Stream(ctx, provider.Request{
			Model:    "claude-opus-4-7",
			Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		}, deltas)
		doneAt <- time.Now()
	}()

	select {
	case <-doneAt:
		// expected — Stream returned within the deadline
	case <-time.After(2 * time.Second):
		t.Fatal("Stream did not return after ctx cancellation")
	}
}

func TestAdapter_StreamMalformedDataPayload(t *testing.T) {
	// data: line with non-JSON for an event type we care about must abort
	// with a decode error — silent skip would let a corrupted token slip
	// through and the user would see a truncated response.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		_, _ = io.WriteString(w, "event: message_start\n"+
			"data: {not valid json}\n\n")
		flusher.Flush()
	})
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "claude-opus-4-7",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected decode error on malformed data payload")
	}
	if !strings.Contains(err.Error(), "decode message_start") {
		t.Errorf("error should mention message_start decode: %v", err)
	}
}

func TestAdapter_StreamMissingMessageStop(t *testing.T) {
	// Stream cuts off after a few text deltas without sending message_stop.
	// Adapter must error rather than return a partial Response — message_stop
	// is the contract that the assistant turn is complete.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		_, _ = io.WriteString(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-7\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"+
			"event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"only\"}}\n\n")
		flusher.Flush()
	})
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "claude-opus-4-7",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error when stream ends without message_stop")
	}
	if !strings.Contains(err.Error(), "message_stop") {
		t.Errorf("error should mention missing message_stop: %v", err)
	}
}

func TestAdapter_StreamHTTPError(t *testing.T) {
	// 401 from /v1/messages with a real Anthropic-shaped error envelope.
	// Adapter should surface the status code and a prefix of the body.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	})
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "claude-opus-4-7",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "http 401") {
		t.Errorf("expected status code in error: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid x-api-key") {
		t.Errorf("expected upstream message in error: %v", err)
	}
}

func TestAdapter_StreamEmptyMessagesRejected(t *testing.T) {
	// Early-return guard mirrors the unary path. Channel must still be closed
	// so a consumer's range loop terminates cleanly.
	a, err := claude.New("sk-test", claude.WithEndpoint("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 1)
	_, err = a.Stream(context.Background(), provider.Request{
		Model: "claude-opus-4-7",
	}, deltas)
	if !errors.Is(err, claude.ErrEmptyMessages) {
		t.Errorf("err: got %v, want %v", err, claude.ErrEmptyMessages)
	}
	if _, ok := <-deltas; ok {
		t.Error("deltas channel should be closed on early error")
	}
}

// TestAdapter_StreamerInterface verifies the type assertion the server uses
// before dispatching daimon.provider.stream succeeds — wire-shape regression
// guard for future refactors.
func TestAdapter_StreamerInterface(t *testing.T) {
	a, err := claude.New("sk-test", claude.WithEndpoint("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.Provider(a).(provider.Streamer); !ok {
		t.Fatal("claude.Adapter must implement provider.Streamer")
	}
}

func TestAdapter_ConcatenatesMultipleTextBlocks(t *testing.T) {
	body := `{
      "id": "msg_x", "type": "message", "role": "assistant", "model": "claude-opus-4-7",
      "content": [
        {"type": "text", "text": "alpha "},
        {"type": "text", "text": "beta"}
      ],
      "stop_reason": "end_turn",
      "usage": {"input_tokens": 1, "output_tokens": 2}
    }`
	srv := fakeServer(t, 200, body, nil, nil)
	defer srv.Close()
	a, err := claude.New("sk-test", claude.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Invoke(context.Background(), provider.Request{
		Model:    "claude-opus-4-7",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "alpha beta" {
		t.Fatalf("got %q, want \"alpha beta\"", resp.Content)
	}
}
