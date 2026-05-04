package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/provider"
)

// --- mock provider for routing tests ----------------------------------------

// mockProvider records the request it received and returns a canned response.
// Used to drive the daimon.provider.invoke handler without standing up an
// httptest server inside server_test.go (the claude package handles that).
type mockProvider struct {
	name    string
	models  []provider.Model
	lastReq provider.Request
	resp    *provider.Response
	err     error
}

func (m *mockProvider) Name() string             { return m.name }
func (m *mockProvider) Models() []provider.Model { return m.models }
func (m *mockProvider) Invoke(_ context.Context, req provider.Request) (*provider.Response, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	if m.resp == nil {
		return &provider.Response{Model: req.Model, Content: "ok", StopReason: provider.StopReasonEndTurn}, nil
	}
	return m.resp, nil
}

// fixtureWithProviders is fixture's cousin: same primitives, but Server is
// constructed with a provider registry and credential store wired in.
type fixtureWithProviders struct {
	*fixture
	reg   *provider.Registry
	creds *provider.CredentialStore
	mock  *mockProvider
}

func newFixtureWithProviders(t *testing.T, withCreds bool) *fixtureWithProviders {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	dir := t.TempDir()
	store, err := memory.Open(filepath.Join(dir, "memory.db"), id, memory.NullEmbedder{})
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	alog, err := activity.Open(filepath.Join(dir, "activity.log"), id)
	if err != nil {
		t.Fatalf("activity.Open: %v", err)
	}
	t.Cleanup(func() { _ = alog.Close() })

	reg := provider.NewRegistry()
	mock := &mockProvider{
		name:   "mock",
		models: []provider.Model{{ID: "mock-1", DisplayName: "Mock 1"}},
	}
	reg.Register(mock)

	var creds *provider.CredentialStore
	if withCreds {
		creds = provider.NewCredentialStore()
		creds.Set("mock", "secret-not-used-by-mock")
	}

	srv, err := New(Options{
		Identity:    id,
		Store:       store,
		Log:         alog,
		Providers:   reg,
		Credentials: creds,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	sockDir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "s.sock")
	if err := srv.Listen(sockPath); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() { _ = srv.Serve(ctx) }()
	waitForSocket(t, sockPath)

	f := &fixture{t: t, id: id, mem: store, alog: alog, srv: srv, addr: sockPath}
	return &fixtureWithProviders{fixture: f, reg: reg, creds: creds, mock: mock}
}

// --- daimon.provider.list ---------------------------------------------------

func TestProviderList_Empty(t *testing.T) {
	// Fixture with no providers attached: the method exists but returns [].
	f := newFixture(t)
	resp := f.call(t, "daimon.provider.list", nil)
	if resp.Error != nil {
		t.Fatalf("list with no registry should not error, got %+v", resp.Error)
	}
	var out []providerListEntry
	resultAs(t, resp, &out)
	if len(out) != 0 {
		t.Fatalf("expected empty list, got %v", out)
	}
}

func TestProviderList_WithRegistryAndCredentials(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	resp := f.call(t, "daimon.provider.list", nil)
	var out []providerListEntry
	resultAs(t, resp, &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(out))
	}
	if out[0].Name != "mock" {
		t.Errorf("name: got %q", out[0].Name)
	}
	if !out[0].Configured {
		t.Errorf("expected configured=true with credentials present")
	}
	if len(out[0].Models) != 1 || out[0].Models[0].ID != "mock-1" {
		t.Errorf("models: got %+v", out[0].Models)
	}
}

func TestProviderList_UnconfiguredWithoutCredentials(t *testing.T) {
	f := newFixtureWithProviders(t, false)
	resp := f.call(t, "daimon.provider.list", nil)
	var out []providerListEntry
	resultAs(t, resp, &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(out))
	}
	if out[0].Configured {
		t.Errorf("expected configured=false without credentials")
	}
}

// --- daimon.provider.invoke -------------------------------------------------

func TestProviderInvoke_HappyPath(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	f.mock.resp = &provider.Response{
		Model:      "mock-1",
		Content:    "hi back",
		StopReason: provider.StopReasonEndTurn,
		Usage:      provider.Usage{InputTokens: 3, OutputTokens: 2},
	}

	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
	})
	var out providerInvokeResult
	resultAs(t, resp, &out)

	if out.Response == nil {
		t.Fatal("response is nil")
	}
	if out.Response.Content != "hi back" {
		t.Errorf("content: got %q", out.Response.Content)
	}
	if out.Response.StopReason != provider.StopReasonEndTurn {
		t.Errorf("stop reason: got %q", out.Response.StopReason)
	}
	if len(out.InjectedMemoryIDs) != 0 {
		t.Errorf("expected no injected_memory_ids without inject_context, got %v", out.InjectedMemoryIDs)
	}
	if f.mock.lastReq.Model != "mock-1" {
		t.Errorf("last req model: got %q", f.mock.lastReq.Model)
	}
	if len(f.mock.lastReq.Messages) != 1 || f.mock.lastReq.Messages[0].Content != "hi" {
		t.Errorf("last req messages: %+v", f.mock.lastReq.Messages)
	}
}

func TestProviderInvoke_MissingProviderField(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestProviderInvoke_UnknownProvider(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "nope",
		"request": map[string]any{
			"model":    "x",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeNotFound {
		t.Errorf("code: got %d, want %d", resp.Error.Code, CodeNotFound)
	}
}

func TestProviderInvoke_NoRegistry(t *testing.T) {
	f := newFixture(t) // no providers attached
	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "x",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeNotFound {
		t.Errorf("code: got %d, want %d", resp.Error.Code, CodeNotFound)
	}
}

func TestProviderInvoke_AdapterError(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	f.mock.err = errors.New("upstream exploded")
	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d, want %d", resp.Error.Code, CodeInternalError)
	}
}

func TestProviderInvoke_LogsActivity(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	f.mock.resp = &provider.Response{
		Model:      "mock-1",
		Content:    "ok",
		StopReason: provider.StopReasonEndTurn,
		Usage:      provider.Usage{InputTokens: 7, OutputTokens: 3},
	}
	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{Kind: activity.KindProviderInvoke})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 provider.invoke entry, got %d", len(entries))
	}
	var payload map[string]any
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["provider"] != "mock" {
		t.Errorf("payload provider: %v", payload["provider"])
	}
	if payload["model"] != "mock-1" {
		t.Errorf("payload model: %v", payload["model"])
	}
	// JSON numbers decode to float64.
	if it, _ := payload["input_tokens"].(float64); int(it) != 7 {
		t.Errorf("payload input_tokens: %v", payload["input_tokens"])
	}
	if ot, _ := payload["output_tokens"].(float64); int(ot) != 3 {
		t.Errorf("payload output_tokens: %v", payload["output_tokens"])
	}
	if payload["stop_reason"] != "end_turn" {
		t.Errorf("payload stop_reason: %v", payload["stop_reason"])
	}
	if _, hasInjected := payload["injected_memory_ids"]; hasInjected {
		t.Error("payload should not include injected_memory_ids when no inject_context was set")
	}

	// Activity chain remains valid after the new entry kind.
	if _, err := f.alog.Verify(context.Background()); err != nil {
		t.Errorf("activity chain verify: %v", err)
	}
}

func TestProviderInvoke_InjectContextEnrichesSystem(t *testing.T) {
	f := newFixtureWithProviders(t, true)

	// Seed two memories that the substring search will find on "favourite".
	must := func(_ *memory.Memory, err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.mem.Write(context.Background(), memory.WriteRequest{
		Kind:    memory.KindFact,
		Content: "huckgod's favourite colour is olive green",
	}))
	must(f.mem.Write(context.Background(), memory.WriteRequest{
		Kind:    memory.KindPreference,
		Content: "favourite editor: vim",
	}))

	f.mock.resp = &provider.Response{Model: "mock-1", Content: "noted", StopReason: provider.StopReasonEndTurn}

	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "what do you know about me"}},
			"system":   "you are concise",
		},
		"inject_context": map[string]any{
			"query":      "favourite",
			"max_tokens": 500,
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// The mock provider received an enriched system prompt — original text
	// must survive intact and the injected block must precede it.
	got := f.mock.lastReq.System
	if got == "" || got == "you are concise" {
		t.Fatalf("system was not enriched: %q", got)
	}
	if !contains(got, "favourite") {
		t.Errorf("system should contain retrieved memories: %q", got)
	}
	if !contains(got, "you are concise") {
		t.Errorf("system should preserve the original prompt: %q", got)
	}

	// Activity log entry should record the injected memory ids.
	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{Kind: activity.KindProviderInvoke})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 provider.invoke entry, got %d", len(entries))
	}
	var payload map[string]any
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if _, hasInjected := payload["injected_memory_ids"]; !hasInjected {
		t.Error("payload should include injected_memory_ids when inject_context was set")
	}
}

// TestProviderInvoke_RPCResponseSurfacesInjectedMemoryIDs proves the session-24
// wire-shape change: when inject_context retrieval matches one or more memories,
// the daimon.provider.invoke RPC response's envelope MUST carry their IDs in
// the optional injected_memory_ids field. This is what lets the chat REPL
// print "[inject_context: query=... matched=N]" without having to re-query the
// memory store. The activity log already records the same IDs (the principal-
// side audit trail is the source of truth); this test guards the client-side
// surface that depends on the response shape.
func TestProviderInvoke_RPCResponseSurfacesInjectedMemoryIDs(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	if _, err := f.mem.Write(context.Background(), memory.WriteRequest{
		Kind:    memory.KindFact,
		Content: "huckgod runs Daimon on macOS",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mem.Write(context.Background(), memory.WriteRequest{
		Kind:    memory.KindPreference,
		Content: "huckgod prefers vim",
	}); err != nil {
		t.Fatal(err)
	}
	f.mock.resp = &provider.Response{Model: "mock-1", Content: "ok", StopReason: provider.StopReasonEndTurn}

	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "what do you know"}},
		},
		"inject_context": map[string]any{"query": "huckgod"},
	})
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var out providerInvokeResult
	resultAs(t, resp, &out)
	if out.Response == nil {
		t.Fatal("response is nil")
	}
	if len(out.InjectedMemoryIDs) == 0 {
		t.Fatal("expected injected_memory_ids to be populated when inject_context matches memories")
	}
	for _, id := range out.InjectedMemoryIDs {
		if id == "" {
			t.Errorf("injected_memory_ids contains empty string: %v", out.InjectedMemoryIDs)
		}
	}
}

func TestProviderInvoke_InjectContextWithEmptyOriginalSystem(t *testing.T) {
	f := newFixtureWithProviders(t, true)
	if _, err := f.mem.Write(context.Background(), memory.WriteRequest{
		Kind:    memory.KindFact,
		Content: "alpha bravo charlie",
	}); err != nil {
		t.Fatal(err)
	}
	f.mock.resp = &provider.Response{Model: "mock-1", Content: "ok", StopReason: provider.StopReasonEndTurn}

	resp := f.call(t, "daimon.provider.invoke", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "alpha"}},
		},
		"inject_context": map[string]any{"query": "alpha"},
	})
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	if f.mock.lastReq.System == "" {
		t.Error("system should be set from injected context even when caller did not supply one")
	}
}

// --- transport-shape sanity -------------------------------------------------

func TestProviderInvoke_RawJSONShape(t *testing.T) {
	// Verify the wire shape one more time: the response result for invoke
	// is the providerInvokeResult envelope — {"response": ProviderResponse,
	// "injected_memory_ids"?: [...]}. The bare-ProviderResponse shape from
	// sessions 7-23 was dropped in session 24 so the daimon can surface the
	// IDs of memories that were folded into the prompt via inject_context.
	// omitempty on injected_memory_ids: a no-inject-context call MUST NOT
	// include the field at all.
	f := newFixtureWithProviders(t, true)
	f.mock.resp = &provider.Response{Model: "mock-1", Content: "hi", StopReason: provider.StopReasonEndTurn}

	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))

	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      "raw-1",
		"method":  "daimon.provider.invoke",
		"params": map[string]any{
			"provider": "mock",
			"request": map[string]any{
				"model":    "mock-1",
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			},
		},
	}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The result MUST be an envelope with a nested "response" object.
	if !contains(string(resp.Result), `"response":{`) {
		t.Fatalf("expected envelope with nested response, got: %s", resp.Result)
	}
	if !contains(string(resp.Result), `"content":"hi"`) {
		t.Fatalf("response content not present: %s", resp.Result)
	}
	// omitempty: no inject_context was set, so the field must not be present.
	if contains(string(resp.Result), `"injected_memory_ids"`) {
		t.Fatalf("envelope must omit injected_memory_ids when no inject_context: %s", resp.Result)
	}
}

// --- helpers ----------------------------------------------------------------

// contains is a stdlib-free substring helper to avoid importing "strings"
// just for this in a file that already imports a lot.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
