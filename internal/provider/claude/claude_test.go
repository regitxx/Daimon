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
