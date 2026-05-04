package openai_test

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
	"github.com/regitxx/Daimon/internal/provider/openai"
)

// openaiRequestCapture is the wire-format shape we assert against for outbound
// requests. Mirrors the unexported responsesRequest in the adapter.
type openaiRequestCapture struct {
	Model           string `json:"model"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Instructions    string `json:"instructions,omitempty"`
	Input           []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"input"`
	Temperature *float64 `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Stream      bool     `json:"stream,omitempty"`
}

// fakeServer returns an httptest.Server whose handler captures the request and
// returns the supplied response body with the supplied status.
func fakeServer(t *testing.T, status int, respBody string, capture *openaiRequestCapture, capturedHeaders *http.Header) *httptest.Server {
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
  "id": "resp_test",
  "object": "response",
  "status": "completed",
  "model": "gpt-5",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "status": "completed",
      "content": [
        {"type": "output_text", "text": "Hello, world."}
      ]
    }
  ],
  "usage": {"input_tokens": 12, "output_tokens": 5, "total_tokens": 17}
}`

func TestAdapter_NewRequiresAPIKey(t *testing.T) {
	_, err := openai.New("")
	if !errors.Is(err, openai.ErrEmptyAPIKey) {
		t.Fatalf("expected ErrEmptyAPIKey, got %v", err)
	}
}

func TestAdapter_NameAndModels(t *testing.T) {
	a, err := openai.New("sk-test")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if a.Name() != openai.Name {
		t.Fatalf("name: got %q, want %q", a.Name(), openai.Name)
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
	var capture openaiRequestCapture
	var headers http.Header
	srv := fakeServer(t, 200, happyResponse, &capture, &headers)
	defer srv.Close()

	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	resp, err := a.Invoke(context.Background(), provider.Request{
		Model:    "gpt-5",
		System:   "be brief",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	// Outbound request shape.
	if capture.Model != "gpt-5" {
		t.Errorf("request model: got %q", capture.Model)
	}
	if capture.MaxOutputTokens != openai.DefaultMaxTokens {
		t.Errorf("default max_output_tokens: got %d, want %d", capture.MaxOutputTokens, openai.DefaultMaxTokens)
	}
	if capture.Instructions != "be brief" {
		t.Errorf("instructions: got %q", capture.Instructions)
	}
	if len(capture.Input) != 1 || capture.Input[0].Role != "user" || capture.Input[0].Content != "Hi" {
		t.Errorf("input: got %+v", capture.Input)
	}

	// Headers.
	if got := headers.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: got %q", got)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q", got)
	}

	// Normalised response.
	if resp.Content != "Hello, world." {
		t.Errorf("content: got %q", resp.Content)
	}
	if resp.Model != "gpt-5" {
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
	a, err := openai.New("sk-test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{Model: "gpt-5"})
	if !errors.Is(err, openai.ErrEmptyMessages) {
		t.Fatalf("expected ErrEmptyMessages, got %v", err)
	}
}

func TestAdapter_InvokeDefaultsModel(t *testing.T) {
	var capture openaiRequestCapture
	srv := fakeServer(t, 200, happyResponse, &capture, nil)
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
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
	var capture openaiRequestCapture
	srv := fakeServer(t, 200, happyResponse, &capture, nil)
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.3
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:       "gpt-5",
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
	if len(capture.Stop) != 1 || capture.Stop[0] != "END" {
		t.Errorf("stop: %v", capture.Stop)
	}
	if capture.MaxOutputTokens != 256 {
		t.Errorf("max_output_tokens override: got %d, want 256", capture.MaxOutputTokens)
	}
}

func TestAdapter_InvokeHTTPError(t *testing.T) {
	srv := fakeServer(t, 400,
		`{"error":{"type":"invalid_request_error","code":"model_not_found","message":"unknown model"}}`,
		nil, nil)
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "gpt-bogus",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from 4xx")
	}
	if !strings.Contains(err.Error(), "http 400") {
		t.Errorf("expected status code in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Errorf("expected upstream message in error, got %v", err)
	}
}

func TestAdapter_InvokeResponseLevelError(t *testing.T) {
	// 200 OK but the response body carries an error payload (the Responses
	// API can do this when validation passes but generation fails partway).
	body := `{
        "id": "resp_x", "object": "response", "status": "failed", "model": "gpt-5",
        "output": [],
        "usage": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
        "error": {"type": "server_error", "message": "model overloaded"}
      }`
	srv := fakeServer(t, 200, body, nil, nil)
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from response-level error payload")
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Errorf("expected upstream message in error, got %v", err)
	}
}

func TestAdapter_InvokeStopReasonNormalisation(t *testing.T) {
	type tc struct {
		name             string
		status           string
		incompleteReason string
		want             provider.StopReason
	}
	cases := []tc{
		{"completed", "completed", "", provider.StopReasonEndTurn},
		{"incomplete-max-tokens", "incomplete", "max_output_tokens", provider.StopReasonMaxTokens},
		{"incomplete-stop-seq", "incomplete", "stop_sequence", provider.StopReasonStopSequence},
		{"incomplete-content-filter", "incomplete", "content_filter", provider.StopReasonOther},
		{"incomplete-no-details", "incomplete", "", provider.StopReasonOther},
		{"unknown-future-status", "experimental_future_status", "", provider.StopReasonOther},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body string
			if c.incompleteReason != "" {
				body = `{
                  "id": "r", "object": "response", "status": "` + c.status + `", "model": "gpt-5",
                  "output": [{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x"}]}],
                  "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
                  "incomplete_details": {"reason": "` + c.incompleteReason + `"}
                }`
			} else {
				body = `{
                  "id": "r", "object": "response", "status": "` + c.status + `", "model": "gpt-5",
                  "output": [{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x"}]}],
                  "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}
                }`
			}
			srv := fakeServer(t, 200, body, nil, nil)
			defer srv.Close()
			a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := a.Invoke(context.Background(), provider.Request{
				Model:    "gpt-5",
				Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StopReason != c.want {
				t.Errorf("status=%q reason=%q: got %q, want %q",
					c.status, c.incompleteReason, resp.StopReason, c.want)
			}
		})
	}
}

func TestAdapter_InvokeContextCancellation(t *testing.T) {
	// Block forever; expect ctx cancellation to propagate.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = a.Invoke(ctx, provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAdapter_ConcatenatesMultipleTextBlocks(t *testing.T) {
	body := `{
      "id": "resp_x", "object": "response", "status": "completed", "model": "gpt-5",
      "output": [
        {
          "type": "message", "role": "assistant",
          "content": [
            {"type": "output_text", "text": "alpha "},
            {"type": "output_text", "text": "beta"}
          ]
        }
      ],
      "usage": {"input_tokens": 1, "output_tokens": 2, "total_tokens": 3}
    }`
	srv := fakeServer(t, 200, body, nil, nil)
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Invoke(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "alpha beta" {
		t.Fatalf("got %q, want \"alpha beta\"", resp.Content)
	}
}

func TestAdapter_SkipsNonMessageOutputs(t *testing.T) {
	// Reasoning summaries from o-series models, tool calls, etc. should be
	// skipped — only assistant message text contributes to Content. v0.1
	// surfaces tools and reasoning when their respective spec changes land.
	body := `{
      "id": "resp_x", "object": "response", "status": "completed", "model": "gpt-5",
      "output": [
        {"type": "reasoning", "content": [{"type": "summary_text", "text": "internal thoughts"}]},
        {
          "type": "message", "role": "assistant",
          "content": [
            {"type": "output_text", "text": "user-visible answer"}
          ]
        },
        {"type": "tool_call", "name": "search", "arguments": "{}"}
      ],
      "usage": {"input_tokens": 1, "output_tokens": 2, "total_tokens": 3}
    }`
	srv := fakeServer(t, 200, body, nil, nil)
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Invoke(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "user-visible answer" {
		t.Fatalf("got %q, want %q", resp.Content, "user-visible answer")
	}
}

func TestAdapter_PreservesMultiTurnInput(t *testing.T) {
	// Verifies the message ordering and roles round-trip correctly through
	// our normalized → Responses-API translation.
	var capture openaiRequestCapture
	srv := fakeServer(t, 200, happyResponse, &capture, nil)
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model: "gpt-5",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "first"},
			{Role: provider.RoleAssistant, Content: "second"},
			{Role: provider.RoleUser, Content: "third"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(capture.Input))
	}
	want := []struct {
		role, content string
	}{
		{"user", "first"},
		{"assistant", "second"},
		{"user", "third"},
	}
	for i, w := range want {
		if capture.Input[i].Role != w.role || capture.Input[i].Content != w.content {
			t.Errorf("input[%d]: got %+v, want %+v", i, capture.Input[i], w)
		}
	}
}

// --- streaming tests ---

// streamServer wraps httptest.NewServer with a handler that lets the test
// write the SSE payload directly. The Flusher is asserted because without it
// the test would silently buffer events and fail to exercise real streaming
// behaviour (the cancellation test in particular depends on it).
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

// happySSE is the canonical OpenAI Responses API SSE shape: response.created
// (with model), then a stream of response.output_text.delta events carrying
// the token fragments, terminated by response.completed (with status, full
// usage, and the final response object). Interleaved are response.in_progress,
// response.output_item.added, response.content_part.added, response.
// output_text.done, response.content_part.done, and response.output_item.done
// — all events the parser must ignore for forward-compat with future
// reasoning/tool surfaces.
const happySSE = `event: response.created
data: {"type":"response.created","response":{"id":"resp_test","object":"response","status":"in_progress","model":"gpt-5","output":[],"usage":null}}

event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"resp_test","status":"in_progress","model":"gpt-5"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant"}}

event: response.content_part.added
data: {"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hello"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":", "}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"world."}

event: response.output_text.done
data: {"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"Hello, world."}

event: response.content_part.done
data: {"type":"response.content_part.done","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello, world."}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_test","object":"response","status":"completed","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello, world."}]}],"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}}

`

func TestAdapter_StreamHappyPath(t *testing.T) {
	var capture openaiRequestCapture
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capture)
		_, _ = io.WriteString(w, happySSE)
		flusher.Flush()
	})
	defer srv.Close()

	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
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
		Model:    "gpt-5",
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
	if resp.Model != "gpt-5" {
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
		_, _ = io.WriteString(w, "event: response.created\n"+
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"model\":\"gpt-5\"}}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "event: response.output_text.delta\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"first\"}\n\n")
		flusher.Flush()
		<-gate
		<-r.Context().Done()
	})
	defer srv.Close()
	defer close(gate)

	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
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
			Model:    "gpt-5",
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
		_, _ = io.WriteString(w, "event: response.output_text.delta\n"+
			"data: {not valid json}\n\n")
		flusher.Flush()
	})
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected decode error on malformed data payload")
	}
	if !strings.Contains(err.Error(), "decode response.output_text.delta") {
		t.Errorf("error should mention decode of the delta event: %v", err)
	}
}

func TestAdapter_StreamMissingTerminalEvent(t *testing.T) {
	// Stream cuts off after a few text deltas without sending response.
	// completed. Adapter must error rather than return a partial Response —
	// the terminal event is the contract that the assistant turn is complete.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		_, _ = io.WriteString(w, "event: response.created\n"+
			"data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-5\"}}\n\n"+
			"event: response.output_text.delta\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"only\"}\n\n")
		flusher.Flush()
	})
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error when stream ends without terminal event")
	}
	if !strings.Contains(err.Error(), "response.completed") {
		t.Errorf("error should mention missing terminal event: %v", err)
	}
}

func TestAdapter_StreamHTTPError(t *testing.T) {
	// 401 from /v1/responses with a real OpenAI-shaped error envelope.
	// Adapter should surface the status code and a prefix of the body.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_api_key","message":"invalid API key"}}`)
	})
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "http 401") {
		t.Errorf("expected status code in error: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("expected upstream message in error: %v", err)
	}
}

func TestAdapter_StreamMidStreamErrorEvent(t *testing.T) {
	// 200 OK opens the stream, deltas flow, then the server emits an
	// "error" event. Adapter aborts with the carried message — generation
	// failed partway and a partial Response would be misleading.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		_, _ = io.WriteString(w, "event: response.created\n"+
			"data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-5\"}}\n\n"+
			"event: response.output_text.delta\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"+
			"event: error\n"+
			"data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"upstream model overloaded\"}\n\n")
		flusher.Flush()
	})
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error from mid-stream error event")
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Errorf("error should carry the upstream message: %v", err)
	}
}

func TestAdapter_StreamIncompleteTerminal(t *testing.T) {
	// response.incomplete carries incomplete_details; the adapter must
	// surface it through the StopReason normaliser exactly as the unary
	// path does. Wire-shape regression guard for the streaming → unary
	// parity that the chat REPL depends on for consistent UX.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
		_, _ = io.WriteString(w, "event: response.created\n"+
			"data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-5\"}}\n\n"+
			"event: response.output_text.delta\n"+
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"truncated\"}\n\n"+
			"event: response.incomplete\n"+
			"data: {\"type\":\"response.incomplete\",\"response\":{\"model\":\"gpt-5\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n")
		flusher.Flush()
	})
	defer srv.Close()
	a, err := openai.New("sk-test", openai.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	resp, err := a.Stream(context.Background(), provider.Request{
		Model:    "gpt-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if resp.StopReason != provider.StopReasonMaxTokens {
		t.Errorf("stop reason for incomplete-max-output-tokens: got %q, want %q",
			resp.StopReason, provider.StopReasonMaxTokens)
	}
	if resp.Content != "truncated" {
		t.Errorf("accumulated content: got %q, want %q", resp.Content, "truncated")
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 1 {
		t.Errorf("usage: got %+v", resp.Usage)
	}
}

func TestAdapter_StreamEmptyMessagesRejected(t *testing.T) {
	// Early-return guard mirrors the unary path. Channel must still be closed
	// so a consumer's range loop terminates cleanly.
	a, err := openai.New("sk-test", openai.WithEndpoint("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 1)
	_, err = a.Stream(context.Background(), provider.Request{
		Model: "gpt-5",
	}, deltas)
	if !errors.Is(err, openai.ErrEmptyMessages) {
		t.Errorf("err: got %v, want %v", err, openai.ErrEmptyMessages)
	}
	if _, ok := <-deltas; ok {
		t.Error("deltas channel should be closed on early error")
	}
}

// TestAdapter_StreamerInterface verifies the type assertion the server uses
// before dispatching daimon.provider.stream succeeds — wire-shape regression
// guard for future refactors.
func TestAdapter_StreamerInterface(t *testing.T) {
	a, err := openai.New("sk-test", openai.WithEndpoint("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.Provider(a).(provider.Streamer); !ok {
		t.Fatal("openai.Adapter must implement provider.Streamer")
	}
}
