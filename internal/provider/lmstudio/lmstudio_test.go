package lmstudio_test

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
	"github.com/regitxx/Daimon/internal/provider/lmstudio"
)

// chatCapture is the wire-format shape we assert against for outbound
// /v1/chat/completions requests. Mirrors the unexported chatRequest in the
// adapter.
type chatCapture struct {
	Model       string   `json:"model"`
	Stream      bool     `json:"stream"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Messages    []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// fakeServer returns an httptest.Server whose handler routes /v1/models GETs
// to the supplied modelsBody (with the supplied modelsStatus) and
// /v1/chat/completions POSTs to the supplied chatBody/chatStatus, capturing
// the parsed chat request body into capture (when non-nil) and the inbound
// headers from each path.
func fakeServer(
	t *testing.T,
	modelsStatus int,
	modelsBody string,
	chatStatus int,
	chatBody string,
	capture *chatCapture,
	capturedHeaders *http.Header,
	probeHeaders *http.Header,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if probeHeaders != nil {
				h := r.Header.Clone()
				*probeHeaders = h
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(modelsStatus)
			_, _ = io.WriteString(w, modelsBody)
		case "/v1/chat/completions":
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

const modelsBody = `{
  "object": "list",
  "data": [
    {"id": "llama-3.2-1b-instruct", "object": "model", "owned_by": "organization_owner"},
    {"id": "qwen2.5-7b-instruct",   "object": "model", "owned_by": "organization_owner"},
    {"id": "phi-3.5-mini-instruct", "object": "model", "owned_by": "organization_owner"}
  ]
}`

const chatHappyBody = `{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1746345600,
  "model": "llama-3.2-1b-instruct",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "Hello, world."},
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17}
}`

func TestAdapter_NewProbesAndPopulatesModels(t *testing.T) {
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, nil, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if a.Name() != lmstudio.Name {
		t.Errorf("name: got %q, want %q", a.Name(), lmstudio.Name)
	}
	models := a.Models()
	if len(models) != 3 {
		t.Fatalf("models: got %d, want 3", len(models))
	}
	// Sorted alphabetically for determinism.
	want := []string{"llama-3.2-1b-instruct", "phi-3.5-mini-instruct", "qwen2.5-7b-instruct"}
	for i, w := range want {
		if models[i].ID != w {
			t.Errorf("models[%d].ID: got %q, want %q", i, models[i].ID, w)
		}
	}
}

func TestAdapter_NewProbeFailureReturnsError(t *testing.T) {
	srv := fakeServer(t, 500, `{"error":{"message":"internal"}}`, 200, chatHappyBody, nil, nil, nil)
	defer srv.Close()
	_, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err == nil {
		t.Fatal("expected probe error on 500 from /v1/models")
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("error should mention probe: %v", err)
	}
}

func TestAdapter_NewProbeNetworkErrorReturnsError(t *testing.T) {
	// Endpoint pointing at a closed port — net.Dial-level failure.
	_, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint("http://127.0.0.1:1"))
	if err == nil {
		t.Fatal("expected error from unreachable endpoint")
	}
}

func TestAdapter_NewProbeSendsBearerHeader(t *testing.T) {
	// The probe must send Authorization too — configurations that require
	// auth would fail the probe otherwise, masking a reachable server as
	// "unavailable".
	var probeHeaders http.Header
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, nil, nil, &probeHeaders)
	defer srv.Close()
	_, err := lmstudio.New(context.Background(),
		lmstudio.WithEndpoint(srv.URL),
		lmstudio.WithAPIKey("custom-secret"),
	)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if got := probeHeaders.Get("Authorization"); got != "Bearer custom-secret" {
		t.Errorf("probe Authorization: got %q, want %q", got, "Bearer custom-secret")
	}
}

func TestAdapter_NewWithModelsSkipsProbe(t *testing.T) {
	// No httptest.Server at all — WithModels() must short-circuit the probe.
	custom := []provider.Model{{ID: "llama-3.2-1b", DisplayName: "Llama 3.2 1B"}}
	a, err := lmstudio.New(context.Background(),
		lmstudio.WithEndpoint("http://127.0.0.1:1"),
		lmstudio.WithModels(custom),
	)
	if err != nil {
		t.Fatalf("new with models: %v", err)
	}
	if got := a.Models(); len(got) != 1 || got[0].ID != "llama-3.2-1b" {
		t.Errorf("models: got %+v", got)
	}
}

func TestAdapter_ModelsReturnsCopy(t *testing.T) {
	a, err := lmstudio.New(context.Background(),
		lmstudio.WithEndpoint("http://127.0.0.1:1"),
		lmstudio.WithModels([]provider.Model{{ID: "llama-3.2-1b"}}),
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
	var capture chatCapture
	var headers http.Header
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, &capture, &headers, nil)
	defer srv.Close()

	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	resp, err := a.Invoke(context.Background(), provider.Request{
		Model:    "llama-3.2-1b-instruct",
		System:   "be brief",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	// Outbound request shape: stream=false, model echoed, system prepended
	// as inline message, max_tokens default present.
	if capture.Model != "llama-3.2-1b-instruct" {
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
	if capture.MaxTokens != lmstudio.DefaultMaxTokens {
		t.Errorf("default max_tokens: got %d, want %d", capture.MaxTokens, lmstudio.DefaultMaxTokens)
	}

	// Headers — Chat Completions requires Authorization and Content-Type.
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q", got)
	}
	if got := headers.Get("Authorization"); got != "Bearer "+lmstudio.DefaultAPIKey {
		t.Errorf("Authorization: got %q, want %q", got, "Bearer "+lmstudio.DefaultAPIKey)
	}

	// Normalised response.
	if resp.Content != "Hello, world." {
		t.Errorf("content: got %q", resp.Content)
	}
	if resp.Model != "llama-3.2-1b-instruct" {
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

func TestAdapter_InvokeUsesCustomAPIKey(t *testing.T) {
	var headers http.Header
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, nil, &headers, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(),
		lmstudio.WithEndpoint(srv.URL),
		lmstudio.WithAPIKey("user-supplied-key"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "llama-3.2-1b-instruct",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := headers.Get("Authorization"); got != "Bearer user-supplied-key" {
		t.Errorf("Authorization: got %q, want %q", got, "Bearer user-supplied-key")
	}
}

func TestAdapter_InvokeRequiresMessages(t *testing.T) {
	a, err := lmstudio.New(context.Background(),
		lmstudio.WithEndpoint("http://127.0.0.1:1"),
		lmstudio.WithModels([]provider.Model{{ID: "llama-3.2-1b"}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{Model: "llama-3.2-1b"})
	if !errors.Is(err, lmstudio.ErrEmptyMessages) {
		t.Fatalf("expected ErrEmptyMessages, got %v", err)
	}
}

func TestAdapter_InvokeDefaultsModel(t *testing.T) {
	var capture chatCapture
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, &capture, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
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
	var capture chatCapture
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, &capture, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.3
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:       "llama-3.2-1b-instruct",
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
	if capture.MaxTokens != 256 {
		t.Errorf("max_tokens override: got %d, want 256", capture.MaxTokens)
	}
}

func TestAdapter_InvokeNoSystemNoExtraMessage(t *testing.T) {
	var capture chatCapture
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, &capture, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "llama-3.2-1b-instruct",
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
	srv := fakeServer(t, 200, modelsBody, 404,
		`{"error":{"message":"model 'gpt-bogus' not found","type":"invalid_request_error"}}`,
		nil, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
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
        "id": "chatcmpl-x", "object": "chat.completion", "created": 1746345600,
        "model": "llama-3.2-1b-instruct",
        "choices": [],
        "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
        "error": {"message": "model failed to load", "type": "server_error"}
      }`
	srv := fakeServer(t, 200, modelsBody, 200, body, nil, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "llama-3.2-1b-instruct",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from response-level error payload")
	}
	if !strings.Contains(err.Error(), "model failed to load") {
		t.Errorf("expected upstream message in error, got %v", err)
	}
}

func TestAdapter_InvokeEmptyChoicesError(t *testing.T) {
	// 200 OK, no error field, but choices is empty — malformed response that
	// otherwise parses. Adapter must not panic on choices[0]; surface the
	// shape problem.
	body := `{
        "id": "chatcmpl-x", "object": "chat.completion", "created": 1746345600,
        "model": "llama-3.2-1b-instruct",
        "choices": [],
        "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
      }`
	srv := fakeServer(t, 200, modelsBody, 200, body, nil, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "llama-3.2-1b-instruct",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("expected 'no choices' in error, got %v", err)
	}
}

func TestAdapter_InvokeMalformedJSONError(t *testing.T) {
	srv := fakeServer(t, 200, modelsBody, 200, `not-json{`, nil, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:    "llama-3.2-1b-instruct",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected decode error from malformed body")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("expected decode error, got %v", err)
	}
}

func TestAdapter_InvokeStopReasonNormalisation(t *testing.T) {
	type tc struct {
		name         string
		finishReason string
		want         provider.StopReason
	}
	cases := []tc{
		{"stop", "stop", provider.StopReasonEndTurn},
		{"length", "length", provider.StopReasonMaxTokens},
		{"tool_calls", "tool_calls", provider.StopReasonToolUse},
		{"function_call", "function_call", provider.StopReasonToolUse},
		{"content_filter", "content_filter", provider.StopReasonOther},
		{"empty", "", provider.StopReasonOther},
		{"unknown", "experimental_future_reason", provider.StopReasonOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"id":      "chatcmpl-x",
				"object":  "chat.completion",
				"created": 1746345600,
				"model":   "llama-3.2-1b-instruct",
				"choices": []map[string]any{
					{
						"index":         0,
						"message":       map[string]string{"role": "assistant", "content": "x"},
						"finish_reason": c.finishReason,
					},
				},
				"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
			srv := fakeServer(t, 200, modelsBody, 200, string(body), nil, nil, nil)
			defer srv.Close()
			a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := a.Invoke(context.Background(), provider.Request{
				Model:    "llama-3.2-1b-instruct",
				Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StopReason != c.want {
				t.Errorf("finish_reason=%q: got %q, want %q",
					c.finishReason, resp.StopReason, c.want)
			}
		})
	}
}

func TestAdapter_InvokeContextCancellation(t *testing.T) {
	// Models endpoint must respond so New() succeeds; chat endpoint blocks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, modelsBody)
			return
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = a.Invoke(ctx, provider.Request{
		Model:    "llama-3.2-1b-instruct",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAdapter_PreservesMultiTurnInput(t *testing.T) {
	// Verifies message ordering and roles round-trip through our normalized →
	// /v1/chat/completions translation, with system prepended.
	var capture chatCapture
	srv := fakeServer(t, 200, modelsBody, 200, chatHappyBody, &capture, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Invoke(context.Background(), provider.Request{
		Model:  "llama-3.2-1b-instruct",
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

func TestAdapter_NewWithEmptyModelsList(t *testing.T) {
	// LM Studio up but no model loaded. Adapter should construct successfully
	// with an empty model list — not an error. Caller can decide whether to
	// register an adapter that advertises zero callable models.
	srv := fakeServer(t, 200, `{"object":"list","data":[]}`, 200, chatHappyBody, nil, nil, nil)
	defer srv.Close()
	a, err := lmstudio.New(context.Background(), lmstudio.WithEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("empty models list should not fail: %v", err)
	}
	if len(a.Models()) != 0 {
		t.Errorf("expected zero models, got %d", len(a.Models()))
	}
}

func TestAdapter_ProbeRespectsContext(t *testing.T) {
	// Slow models endpoint with a tight context; New() should fail fast.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := lmstudio.New(ctx, lmstudio.WithEndpoint(srv.URL))
	if err == nil {
		t.Fatal("expected timeout error from probe")
	}
}
