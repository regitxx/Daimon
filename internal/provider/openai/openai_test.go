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
