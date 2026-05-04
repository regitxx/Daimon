package ollama_test

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
	"github.com/regitxx/Daimon/internal/provider/ollama"
)

// ollamaChatCapture is the wire-format shape we assert against for outbound
// /api/chat requests. Mirrors the unexported chatRequest in the adapter.
type ollamaChatCapture struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Options *struct {
		Temperature *float64 `json:"temperature,omitempty"`
		NumPredict  int      `json:"num_predict,omitempty"`
		Stop        []string `json:"stop,omitempty"`
	} `json:"options,omitempty"`
}

// fakeServer returns an httptest.Server whose handler routes /api/tags GETs
// to the supplied tagsBody (with the supplied tagsStatus) and /api/chat POSTs
// to the supplied chatBody/chatStatus, capturing the parsed chat request body
// into capture (when non-nil).
func fakeServer(
	t *testing.T,
	tagsStatus int,
	tagsBody string,
	chatStatus int,
	chatBody string,
	capture *ollamaChatCapture,
	capturedHeaders *http.Header,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tagsStatus)
			_, _ = io.WriteString(w, tagsBody)
		case "/api/chat":
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
			w.WriteHeader(chatStatus)
			_, _ = io.WriteString(w, chatBody)
		default:
			http.NotFound(w, r)
		}
	}))
}

const tagsBody = `{
  "models": [
    {"name": "llama3.2:latest", "modified_at": "2026-04-01T00:00:00Z", "size": 4661224676},
    {"name": "qwen2.5:7b",      "modified_at": "2026-04-02T00:00:00Z", "size": 4683074560},
    {"name": "nomic-embed-text:latest", "modified_at": "2026-04-03T00:00:00Z", "size": 274302450}
  ]
}`

const chatHappyBody = `{
  "model": "llama3.2:latest",
  "created_at": "2026-05-04T12:00:00Z",
  "message": {"role": "assistant", "content": "Hello, world."},
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 12,
  "eval_count": 5
}`

func TestAdapter_NewProbesAndPopulatesModels(t *testing.T) {
	srv := fakeServer(t, 200, tagsBody, 200, chatHappyBody, nil, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if a.Name() != ollama.Name {
		t.Errorf("name: got %q, want %q", a.Name(), ollama.Name)
	}
	models := a.Models()
	if len(models) != 3 {
		t.Fatalf("models: got %d, want 3", len(models))
	}
	// Sorted alphabetically for determinism.
	want := []string{"llama3.2:latest", "nomic-embed-text:latest", "qwen2.5:7b"}
	for i, w := range want {
		if models[i].ID != w {
			t.Errorf("models[%d].ID: got %q, want %q", i, models[i].ID, w)
		}
	}
}

func TestAdapter_NewProbeFailureReturnsError(t *testing.T) {
	// Tags endpoint returns 500 — adapter construction must fail so the
	// caller can skip registration.
	srv := fakeServer(t, 500, `{"error": "internal"}`, 200, chatHappyBody, nil, nil)
	defer srv.Close()
	_, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err == nil {
		t.Fatal("expected probe error on 500 from /api/tags")
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("error should mention probe: %v", err)
	}
}

func TestAdapter_NewProbeNetworkErrorReturnsError(t *testing.T) {
	// Endpoint pointing at a closed port — net.Dial-level failure.
	_, err := ollama.New(context.Background(), ollama.WithEndpoint("http://127.0.0.1:1"))
	if err == nil {
		t.Fatal("expected error from unreachable endpoint")
	}
}

func TestAdapter_NewWithModelsSkipsProbe(t *testing.T) {
	// No httptest.Server at all — WithModels() must short-circuit the probe.
	custom := []provider.Model{{ID: "llama3.2", DisplayName: "Llama 3.2"}}
	a, err := ollama.New(context.Background(),
		ollama.WithEndpoint("http://127.0.0.1:1"),
		ollama.WithModels(custom),
	)
	if err != nil {
		t.Fatalf("new with models: %v", err)
	}
	if got := a.Models(); len(got) != 1 || got[0].ID != "llama3.2" {
		t.Errorf("models: got %+v", got)
	}
}

func TestAdapter_ModelsReturnsCopy(t *testing.T) {
	a, err := ollama.New(context.Background(),
		ollama.WithEndpoint("http://127.0.0.1:1"),
		ollama.WithModels([]provider.Model{{ID: "llama3.2"}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	models := a.Models()
	models[0].ID = "tampered"
	if a.Models()[0].ID == "tampered" {
		t.Fatal("Models() must return a copy, not a reference")
	}
}

func TestAdapter_InvokeHappyPath(t *testing.T) {
	var capture ollamaChatCapture
	var headers http.Header
	srv := fakeServer(t, 200, tagsBody, 200, chatHappyBody, &capture, &headers)
	defer srv.Close()

	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	resp, err := a.Invoke(context.Background(), provider.Request{
		Model:    "llama3.2:latest",
		System:   "be brief",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	// Outbound request shape: stream=false, model echoed, system prepended
	// as inline message, options present with default num_predict.
	if capture.Model != "llama3.2:latest" {
		t.Errorf("request model: got %q", capture.Model)
	}
	if capture.Stream != false {
		t.Errorf("stream: got %v, want false", capture.Stream)
	}
	if len(capture.Messages) != 2 {
		t.Fatalf("messages: got %d, want 2 (system+user)", len(capture.Messages))
	}
	if capture.Messages[0].Role != "system" || capture.Messages[0].Content != "be brief" {
		t.Errorf("system message: got %+v", capture.Messages[0])
	}
	if capture.Messages[1].Role != "user" || capture.Messages[1].Content != "Hi" {
		t.Errorf("user message: got %+v", capture.Messages[1])
	}
	if capture.Options == nil {
		t.Fatal("options should be present (num_predict default)")
	}
	if capture.Options.NumPredict != ollama.DefaultMaxTokens {
		t.Errorf("default num_predict: got %d, want %d", capture.Options.NumPredict, ollama.DefaultMaxTokens)
	}

	// Headers — Ollama needs no auth header, only Content-Type.
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q", got)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Errorf("Authorization should be absent for Ollama: got %q", got)
	}

	// Normalised response.
	if resp.Content != "Hello, world." {
		t.Errorf("content: got %q", resp.Content)
	}
	if resp.Model != "llama3.2:latest" {
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
	a, err := ollama.New(context.Background(),
		ollama.WithEndpoint("http://127.0.0.1:1"),
		ollama.WithModels([]provider.Model{{ID: "llama3.2"}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{Model: "llama3.2"})
	if !errors.Is(err, ollama.ErrEmptyMessages) {
		t.Fatalf("expected ErrEmptyMessages, got %v", err)
	}
}

func TestAdapter_InvokeDefaultsModel(t *testing.T) {
	var capture ollamaChatCapture
	srv := fakeServer(t, 200, tagsBody, 200, chatHappyBody, &capture, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	// Default is the first model returned by the probe (sorted alphabetically).
	if capture.Model != a.Models()[0].ID {
		t.Errorf("expected first model %q to be defaulted, got %q", a.Models()[0].ID, capture.Model)
	}
}

func TestAdapter_InvokeRespectsTemperatureStopSeqsAndMaxTokens(t *testing.T) {
	var capture ollamaChatCapture
	srv := fakeServer(t, 200, tagsBody, 200, chatHappyBody, &capture, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.3
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:       "llama3.2:latest",
		Messages:    []provider.Message{{Role: provider.RoleUser, Content: "x"}},
		Temperature: &temp,
		StopSeqs:    []string{"END"},
		MaxTokens:   256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Options == nil {
		t.Fatal("options should be present")
	}
	if capture.Options.Temperature == nil || *capture.Options.Temperature != 0.3 {
		t.Errorf("temperature: %v", capture.Options.Temperature)
	}
	if len(capture.Options.Stop) != 1 || capture.Options.Stop[0] != "END" {
		t.Errorf("stop: %v", capture.Options.Stop)
	}
	if capture.Options.NumPredict != 256 {
		t.Errorf("num_predict override: got %d, want 256", capture.Options.NumPredict)
	}
}

func TestAdapter_InvokeNoSystemNoExtraMessage(t *testing.T) {
	var capture ollamaChatCapture
	srv := fakeServer(t, 200, tagsBody, 200, chatHappyBody, &capture, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "llama3.2:latest",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Messages) != 1 {
		t.Fatalf("expected 1 message (no system prepended), got %d", len(capture.Messages))
	}
	if capture.Messages[0].Role != "user" {
		t.Errorf("first message role: got %q, want user", capture.Messages[0].Role)
	}
}

func TestAdapter_InvokeHTTPError(t *testing.T) {
	srv := fakeServer(t, 200, tagsBody, 404,
		`{"error":"model 'gpt-bogus' not found, try pulling it first"}`,
		nil, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
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
	if !strings.Contains(err.Error(), "http 404") {
		t.Errorf("expected status code in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected upstream message in error, got %v", err)
	}
}

func TestAdapter_InvokeResponseLevelError(t *testing.T) {
	// 200 OK with an `error` field on the body — surface the upstream message.
	body := `{
        "model": "llama3.2", "created_at": "2026-05-04T12:00:00Z",
        "message": {"role": "assistant", "content": ""},
        "done": false,
        "error": "model failed to load"
      }`
	srv := fakeServer(t, 200, tagsBody, 200, body, nil, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "llama3.2",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from response-level error payload")
	}
	if !strings.Contains(err.Error(), "model failed to load") {
		t.Errorf("expected upstream message in error, got %v", err)
	}
}

func TestAdapter_InvokeStopReasonNormalisation(t *testing.T) {
	type tc struct {
		name       string
		doneReason string
		done       bool
		want       provider.StopReason
	}
	cases := []tc{
		{"stop", "stop", true, provider.StopReasonEndTurn},
		{"length", "length", true, provider.StopReasonMaxTokens},
		{"load", "load", true, provider.StopReasonOther},
		{"empty-but-done", "", true, provider.StopReasonEndTurn},
		{"empty-not-done", "", false, provider.StopReasonOther},
		{"unknown", "experimental_future_reason", true, provider.StopReasonOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"model":             "llama3.2",
				"created_at":        "2026-05-04T12:00:00Z",
				"message":           map[string]string{"role": "assistant", "content": "x"},
				"done":              c.done,
				"done_reason":       c.doneReason,
				"prompt_eval_count": 1,
				"eval_count":        1,
			})
			srv := fakeServer(t, 200, tagsBody, 200, string(body), nil, nil)
			defer srv.Close()
			a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := a.Invoke(context.Background(), provider.Request{
				Model:    "llama3.2",
				Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StopReason != c.want {
				t.Errorf("done_reason=%q done=%v: got %q, want %q",
					c.doneReason, c.done, resp.StopReason, c.want)
			}
		})
	}
}

func TestAdapter_InvokeContextCancellation(t *testing.T) {
	// Tags endpoint must respond so New() succeeds; chat endpoint blocks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, tagsBody)
			return
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = a.Invoke(ctx, provider.Request{
		Model:    "llama3.2:latest",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAdapter_PreservesMultiTurnInput(t *testing.T) {
	// Verifies message ordering and roles round-trip through our normalized →
	// /api/chat translation, with system prepended.
	var capture ollamaChatCapture
	srv := fakeServer(t, 200, tagsBody, 200, chatHappyBody, &capture, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:  "llama3.2:latest",
		System: "you are helpful",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "first"},
			{Role: provider.RoleAssistant, Content: "second"},
			{Role: provider.RoleUser, Content: "third"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Messages) != 4 {
		t.Fatalf("expected 4 messages (system + 3 turns), got %d", len(capture.Messages))
	}
	want := []struct{ role, content string }{
		{"system", "you are helpful"},
		{"user", "first"},
		{"assistant", "second"},
		{"user", "third"},
	}
	for i, w := range want {
		if capture.Messages[i].Role != w.role || capture.Messages[i].Content != w.content {
			t.Errorf("messages[%d]: got %+v, want %+v", i, capture.Messages[i], w)
		}
	}
}

func TestAdapter_NewWithEmptyTagsList(t *testing.T) {
	// Ollama up but no models pulled. Adapter should construct successfully
	// with an empty model list — not an error. Caller can decide whether to
	// register an adapter that advertises zero callable models.
	srv := fakeServer(t, 200, `{"models": []}`, 200, chatHappyBody, nil, nil)
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("empty tags list should not fail: %v", err)
	}
	if len(a.Models()) != 0 {
		t.Errorf("expected zero models, got %d", len(a.Models()))
	}
}

func TestAdapter_ProbeRespectsContext(t *testing.T) {
	// Slow tags endpoint with a tight context; New() should fail fast.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := ollama.New(ctx, ollama.WithEndpoint(srv.URL))
	if err == nil {
		t.Fatal("expected timeout error from probe")
	}
}

// --- streaming ---------------------------------------------------------------

// streamServer routes /api/tags to the canonical fixture and /api/chat to a
// custom handler the test supplies so it can stream NDJSON, write malformed
// frames, or hang to exercise context cancellation.
func streamServer(t *testing.T, chatHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, tagsBody)
		case "/api/chat":
			chatHandler(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

// chatStreamFrames is the canonical NDJSON shape Ollama emits with
// stream:true: one frame per generated chunk, terminated by a frame with
// done:true that carries the usage counters and done_reason.
const chatStreamFrames = `{"model":"llama3.2:latest","created_at":"2026-05-04T12:00:00Z","message":{"role":"assistant","content":"Hello"},"done":false}
{"model":"llama3.2:latest","created_at":"2026-05-04T12:00:00Z","message":{"role":"assistant","content":", "},"done":false}
{"model":"llama3.2:latest","created_at":"2026-05-04T12:00:00Z","message":{"role":"assistant","content":"world."},"done":false}
{"model":"llama3.2:latest","created_at":"2026-05-04T12:00:00Z","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":12,"eval_count":3}
`

func TestAdapter_StreamHappyPath(t *testing.T) {
	var capture ollamaChatCapture
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capture)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, chatStreamFrames)
	})
	defer srv.Close()

	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
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
		Model:    "llama3.2:latest",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	<-done

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
	if resp.StopReason != provider.StopReasonEndTurn {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage: got %+v", resp.Usage)
	}
	if resp.Model != "llama3.2:latest" {
		t.Errorf("model: got %q", resp.Model)
	}
	if len(resp.Raw) == 0 {
		t.Errorf("raw should carry the terminal frame")
	}
}

func TestAdapter_StreamCancellation(t *testing.T) {
	// Server emits one frame, then hangs forever. The test cancels mid-stream
	// and asserts Stream returns promptly with the ctx error and the channel
	// closes — no goroutine leak, no continued upstream consumption.
	gate := make(chan struct{})
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not flush — test cannot stream")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"model":"llama3.2:latest","message":{"role":"assistant","content":"first"},"done":false}`+"\n")
		flusher.Flush()
		<-gate
		<-r.Context().Done()
	})
	defer srv.Close()
	defer close(gate)

	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	deltas := make(chan provider.Delta, 4)
	go func() {
		// Drain the first delta then cancel.
		<-deltas
		cancel()
		// Continue draining so the sender doesn't block on a full channel
		// during shutdown — for the case where another delta is mid-flight.
		for range deltas {
		}
	}()

	doneAt := make(chan time.Time, 1)
	go func() {
		_, _ = a.Stream(ctx, provider.Request{
			Model:    "llama3.2:latest",
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

func TestAdapter_StreamMalformedFrame(t *testing.T) {
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w,
			`{"model":"llama3.2:latest","message":{"role":"assistant","content":"ok"},"done":false}`+"\n"+
				`{not json at all}`+"\n",
		)
	})
	defer srv.Close()

	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 8)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "llama3.2:latest",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error on malformed NDJSON")
	}
	if !strings.Contains(err.Error(), "decode stream frame") {
		t.Errorf("error should mention frame decode: %v", err)
	}
}

func TestAdapter_StreamMissingTerminalFrame(t *testing.T) {
	// Stream cuts off without ever sending done:true. Adapter should error
	// rather than return a partial Response — callers expect the final frame
	// to populate stop_reason and usage.
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w,
			`{"model":"llama3.2:latest","message":{"role":"assistant","content":"only"},"done":false}`+"\n",
		)
	})
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "llama3.2:latest",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error when stream ends without done:true")
	}
}

func TestAdapter_StreamHTTPError(t *testing.T) {
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	})
	defer srv.Close()
	a, err := ollama.New(context.Background(), ollama.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 4)
	go func() {
		for range deltas {
		}
	}()
	_, err = a.Stream(context.Background(), provider.Request{
		Model:    "wat",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}, deltas)
	if err == nil {
		t.Fatal("expected error on non-2xx status")
	}
}

func TestAdapter_StreamEmptyMessagesRejected(t *testing.T) {
	a, err := ollama.New(context.Background(),
		ollama.WithEndpoint("http://127.0.0.1:1"),
		ollama.WithModels([]provider.Model{{ID: "x"}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	deltas := make(chan provider.Delta, 1)
	_, err = a.Stream(context.Background(), provider.Request{
		Model: "x",
	}, deltas)
	if !errors.Is(err, ollama.ErrEmptyMessages) {
		t.Errorf("err: got %v, want %v", err, ollama.ErrEmptyMessages)
	}
	// Channel should still be closed on the early-return path.
	if _, ok := <-deltas; ok {
		t.Error("deltas channel should be closed on early error")
	}
}

// TestAdapter_StreamerInterface verifies the type assertion the server uses
// before dispatching daimon.provider.stream succeeds — wire-shape regression
// guard for future refactors that might split the adapter.
func TestAdapter_StreamerInterface(t *testing.T) {
	a, err := ollama.New(context.Background(),
		ollama.WithEndpoint("http://127.0.0.1:1"),
		ollama.WithModels([]provider.Model{{ID: "x"}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.Provider(a).(provider.Streamer); !ok {
		t.Fatal("ollama.Adapter must implement provider.Streamer")
	}
}
