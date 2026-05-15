package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/provider"
	"github.com/regitxx/Daimon/internal/wallet"
)

// --- mock streamer ----------------------------------------------------------

// mockStreamer is a Provider that ALSO implements provider.Streamer. The
// canned chunks are sent as deltas in order, then a final Response is
// returned with the accumulated content.
type mockStreamer struct {
	mockProvider
	chunks   []string
	streamErr error
}

func (m *mockStreamer) Stream(ctx context.Context, req provider.Request, deltas chan<- provider.Delta) (*provider.Response, error) {
	defer close(deltas)
	m.lastReq = req
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	var acc strings.Builder
	for _, c := range m.chunks {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case deltas <- provider.Delta{Content: c}:
			acc.WriteString(c)
		}
	}
	return &provider.Response{
		Model:      req.Model,
		Content:    acc.String(),
		StopReason: provider.StopReasonEndTurn,
		Usage:      provider.Usage{InputTokens: 5, OutputTokens: 7},
	}, nil
}

// fixtureWithStreamer is a server fixture wired with a streaming provider.
type fixtureWithStreamer struct {
	*fixture
	reg  *provider.Registry
	mock *mockStreamer
}

func newFixtureWithStreamer(t *testing.T) *fixtureWithStreamer {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := memory.Open(filepath.Join(dir, "memory.db"), id, memory.NullEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	alog, err := activity.Open(filepath.Join(dir, "activity.log"), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = alog.Close() })

	reg := provider.NewRegistry()
	mock := &mockStreamer{
		mockProvider: mockProvider{name: "mockstream", models: []provider.Model{{ID: "m-1"}}},
		chunks:       []string{"Hello", ", ", "world."},
	}
	reg.Register(mock)

	srv, err := New(Options{Identity: id, Store: store, Log: alog, Providers: reg})
	if err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "s.sock")
	if err := srv.Listen(sockPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() { _ = srv.Serve(ctx) }()
	waitForSocket(t, sockPath)

	f := &fixture{t: t, id: id, mem: store, alog: alog, srv: srv, addr: sockPath}
	return &fixtureWithStreamer{fixture: f, reg: reg, mock: mock}
}

// --- wire-shape helpers ------------------------------------------------------

// streamFrame is a tolerant decode envelope: notifications carry method+params
// and no id; responses carry id+result/error.
type streamFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func (f streamFrame) isNotification() bool { return len(f.ID) == 0 && f.Method != "" }

// readFrame decodes one frame from c with a deadline. Used for reading the
// notification + response sequence on a single conn.
func readFrame(t *testing.T, dec *json.Decoder) streamFrame {
	t.Helper()
	var f streamFrame
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return f
}

// --- tests ------------------------------------------------------------------

func TestProviderStream_HappyPath(t *testing.T) {
	f := newFixtureWithStreamer(t)
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))

	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      "stream-1",
		"method":  "daimon.provider.stream",
		"params": map[string]any{
			"provider": "mockstream",
			"request": map[string]any{
				"model":    "m-1",
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	dec := json.NewDecoder(c)
	var (
		got       []string
		finalSeen bool
		final     streamFrame
	)
	for !finalSeen {
		fr := readFrame(t, dec)
		if fr.JSONRPC != "2.0" {
			t.Fatalf("bad jsonrpc: %q", fr.JSONRPC)
		}
		if fr.isNotification() {
			if fr.Method != "daimon.provider.stream.delta" {
				t.Fatalf("notification method: got %q", fr.Method)
			}
			var p struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(fr.Params, &p); err != nil {
				t.Fatalf("unmarshal delta: %v", err)
			}
			got = append(got, p.Content)
			continue
		}
		// Response on the original id.
		if string(fr.ID) != `"stream-1"` {
			t.Fatalf("response id: got %s, want \"stream-1\"", fr.ID)
		}
		if fr.Error != nil {
			t.Fatalf("response error: %+v", fr.Error)
		}
		final = fr
		finalSeen = true
	}

	want := []string{"Hello", ", ", "world."}
	if len(got) != len(want) {
		t.Fatalf("delta count: got %d (%v), want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("delta[%d]: got %q, want %q", i, got[i], w)
		}
	}
	var env providerInvokeResult
	if err := json.Unmarshal(final.Result, &env); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	if env.Response == nil {
		t.Fatal("final response is nil")
	}
	if env.Response.Content != "Hello, world." {
		t.Errorf("final content: got %q", env.Response.Content)
	}
	if env.Response.StopReason != provider.StopReasonEndTurn {
		t.Errorf("stop reason: got %q", env.Response.StopReason)
	}
	if env.Response.Usage.InputTokens != 5 || env.Response.Usage.OutputTokens != 7 {
		t.Errorf("usage: got %+v", env.Response.Usage)
	}
	// omitempty: no inject_context, so no injected_memory_ids on the wire.
	if !contains(string(final.Result), `"response":{`) {
		t.Errorf("expected envelope with nested response, got: %s", final.Result)
	}
	if contains(string(final.Result), `"injected_memory_ids"`) {
		t.Errorf("envelope must omit injected_memory_ids when no inject_context: %s", final.Result)
	}
	if len(env.InjectedMemoryIDs) != 0 {
		t.Errorf("expected empty injected_memory_ids slice, got %v", env.InjectedMemoryIDs)
	}
}

// TestProviderStream_RPCResponseSurfacesInjectedMemoryIDs is the streaming
// counterpart to provider_handlers_test's TestProviderInvoke_RPCResponseSurfacesInjectedMemoryIDs.
// Same property: when inject_context retrieval matches, the terminal response
// envelope on daimon.provider.stream MUST carry the matched IDs so the chat
// REPL's `--stream` path renders the same matched=N annotation as the unary
// path.
func TestProviderStream_RPCResponseSurfacesInjectedMemoryIDs(t *testing.T) {
	f := newFixtureWithStreamer(t)
	if _, err := f.mem.Write(context.Background(), memory.WriteRequest{
		Kind:    memory.KindFact,
		Content: "huckgod runs Daimon on macOS",
	}); err != nil {
		t.Fatal(err)
	}

	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))

	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      "stream-inject",
		"method":  "daimon.provider.stream",
		"params": map[string]any{
			"provider": "mockstream",
			"request": map[string]any{
				"model":    "m-1",
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			},
			"inject_context": map[string]any{"query": "huckgod"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(c)
	var final streamFrame
	for {
		fr := readFrame(t, dec)
		if !fr.isNotification() {
			final = fr
			break
		}
	}
	if final.Error != nil {
		t.Fatalf("stream error: %+v", final.Error)
	}
	var env providerInvokeResult
	if err := json.Unmarshal(final.Result, &env); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	if env.Response == nil {
		t.Fatal("final response is nil")
	}
	if len(env.InjectedMemoryIDs) == 0 {
		t.Fatal("expected injected_memory_ids on stream terminal response when inject_context matches")
	}
}

func TestProviderStream_ConnectionReusableAfterStream(t *testing.T) {
	// Same conn — stream first, then a regular invoke — proves the dispatch
	// loop continues reading after the stream completes.
	f := newFixtureWithStreamer(t)
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))

	enc := json.NewEncoder(c)
	dec := json.NewDecoder(c)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": "stream-1", "method": "daimon.provider.stream",
		"params": map[string]any{
			"provider": "mockstream",
			"request":  map[string]any{"model": "m-1", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for {
		fr := readFrame(t, dec)
		if !fr.isNotification() {
			break
		}
	}

	// Now a regular invoke on the SAME conn. The mock Provider's Invoke
	// returns the canned response; we just need to see a response come back.
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": "invoke-2", "method": "daimon.provider.invoke",
		"params": map[string]any{
			"provider": "mockstream",
			"request":  map[string]any{"model": "m-1", "messages": []map[string]any{{"role": "user", "content": "again"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	fr := readFrame(t, dec)
	if string(fr.ID) != `"invoke-2"` {
		t.Fatalf("invoke response id: got %s", fr.ID)
	}
	if fr.Error != nil {
		t.Fatalf("invoke error after stream: %+v", fr.Error)
	}
}

func TestProviderStream_NonStreamerProviderReturnsError(t *testing.T) {
	// Use the regular mock provider (Provider only, no Streamer).
	f := newFixtureWithProviders(t, true)
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))

	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0", "id": "x", "method": "daimon.provider.stream",
		"params": map[string]any{
			"provider": "mock",
			"request":  map[string]any{"model": "mock-1", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	fr := readFrame(t, json.NewDecoder(c))
	if fr.Error == nil {
		t.Fatal("expected error from non-streaming provider")
	}
	if fr.Error.Code != CodeNotFound {
		t.Errorf("code: got %d, want %d", fr.Error.Code, CodeNotFound)
	}
	if !strings.Contains(fr.Error.Message, "does not support streaming") {
		t.Errorf("message: got %q", fr.Error.Message)
	}
}

func TestProviderStream_UnknownProvider(t *testing.T) {
	f := newFixtureWithStreamer(t)
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))

	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0", "id": "x", "method": "daimon.provider.stream",
		"params": map[string]any{
			"provider": "nope",
			"request":  map[string]any{"model": "x", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	fr := readFrame(t, json.NewDecoder(c))
	if fr.Error == nil {
		t.Fatal("expected error")
	}
	if fr.Error.Code != CodeNotFound {
		t.Errorf("code: got %d", fr.Error.Code)
	}
}

func TestProviderStream_AdapterError(t *testing.T) {
	f := newFixtureWithStreamer(t)
	f.mock.streamErr = errors.New("upstream exploded")

	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0", "id": "x", "method": "daimon.provider.stream",
		"params": map[string]any{
			"provider": "mockstream",
			"request":  map[string]any{"model": "m-1", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	fr := readFrame(t, json.NewDecoder(c))
	if fr.Error == nil {
		t.Fatal("expected error")
	}
	if fr.Error.Code != CodeInternalError {
		t.Errorf("code: got %d", fr.Error.Code)
	}
}

func TestProviderStream_ConcurrentInvokeOnSecondConn(t *testing.T) {
	// Per the locked decision: while a stream is in flight on conn A, an
	// invoke on conn B must complete intact. Daemon-level concurrency is
	// many-conns, not in-conn-multiplexed.
	f := newFixtureWithStreamer(t)
	gate := make(chan struct{})
	f.mock.chunks = nil // we'll feed chunks manually via the gate below

	// Replace the mock with one that blocks until the gate fires, so we know
	// the second conn's invoke actually runs concurrently.
	gatedStreamer := &gatedMockStreamer{
		mockProvider: mockProvider{name: "mockstream", models: []provider.Model{{ID: "m-1"}}},
		release:      gate,
	}
	f.reg.Register(gatedStreamer) // overwrites by name

	connStream, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer connStream.Close()
	_ = connStream.SetDeadline(time.Now().Add(3 * time.Second))
	streamDone := make(chan streamFrame, 1)
	go func() {
		_ = json.NewEncoder(connStream).Encode(map[string]any{
			"jsonrpc": "2.0", "id": "s", "method": "daimon.provider.stream",
			"params": map[string]any{
				"provider": "mockstream",
				"request":  map[string]any{"model": "m-1", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
			},
		})
		dec := json.NewDecoder(connStream)
		for {
			fr := readFrame(t, dec)
			if !fr.isNotification() {
				streamDone <- fr
				return
			}
		}
	}()

	// Race-resistant: small wait for the stream goroutine to send + the
	// adapter to pick up. Then fire the second conn.
	time.Sleep(50 * time.Millisecond)

	connInvoke, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer connInvoke.Close()
	_ = connInvoke.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(connInvoke).Encode(map[string]any{
		"jsonrpc": "2.0", "id": "inv", "method": "daimon.provider.list",
	}); err != nil {
		t.Fatal(err)
	}
	fr := readFrame(t, json.NewDecoder(connInvoke))
	if fr.Error != nil {
		t.Fatalf("invoke during stream errored: %+v", fr.Error)
	}
	if string(fr.ID) != `"inv"` {
		t.Fatalf("invoke id: got %s", fr.ID)
	}

	// Release the stream and let it complete.
	close(gate)
	finalFr := <-streamDone
	if finalFr.Error != nil {
		t.Fatalf("stream final error: %+v", finalFr.Error)
	}
}

// gatedMockStreamer streams its single delta only after the release channel
// closes, so a test can interleave a second-conn request mid-stream.
type gatedMockStreamer struct {
	mockProvider
	release chan struct{}
}

func (g *gatedMockStreamer) Stream(ctx context.Context, req provider.Request, deltas chan<- provider.Delta) (*provider.Response, error) {
	defer close(deltas)
	g.lastReq = req
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.release:
	}
	deltas <- provider.Delta{Content: "ok"}
	return &provider.Response{
		Model:      req.Model,
		Content:    "ok",
		StopReason: provider.StopReasonEndTurn,
	}, nil
}

func TestProviderStream_LogsActivityWithStreamedFlag(t *testing.T) {
	f := newFixtureWithStreamer(t)
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))

	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0", "id": "s", "method": "daimon.provider.stream",
		"params": map[string]any{
			"provider": "mockstream",
			"request":  map[string]any{"model": "m-1", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(c)
	for {
		fr := readFrame(t, dec)
		if !fr.isNotification() {
			if fr.Error != nil {
				t.Fatalf("stream error: %+v", fr.Error)
			}
			break
		}
	}

	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{Kind: activity.KindProviderInvoke})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 provider.invoke entry, got %d", len(entries))
	}
	var payload map[string]any
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["streamed"] != true {
		t.Errorf("payload streamed flag: got %v, want true", payload["streamed"])
	}
	if payload["provider"] != "mockstream" {
		t.Errorf("payload provider: got %v", payload["provider"])
	}
}

func TestProviderStream_LockedDaemonRejected(t *testing.T) {
	// Build a server in serve-mode (locked) without ever unlocking, then try
	// to stream. Should get CodeIdentityLocked.
	srv, err := New(Options{Unlock: func(_ context.Context, _ string) (*identity.Identity, *memory.Store, *activity.Log, *wallet.Store, *wallet.Mnemonic, error) {
		t.Fatal("unlock should not be called")
		return nil, nil, nil, nil, nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sockPath := filepath.Join(sockDir, "s.sock")
	if err := srv.Listen(sockPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer srv.Close()
	go func() { _ = srv.Serve(ctx) }()
	waitForSocket(t, sockPath)

	c, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(c).Encode(map[string]any{
		"jsonrpc": "2.0", "id": "s", "method": "daimon.provider.stream",
		"params": map[string]any{
			"provider": "x",
			"request":  map[string]any{"model": "x", "messages": []map[string]any{{"role": "user", "content": "hi"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	fr := readFrame(t, json.NewDecoder(c))
	if fr.Error == nil {
		t.Fatal("expected error")
	}
	if fr.Error.Code != CodeIdentityLocked {
		t.Errorf("code: got %d, want %d", fr.Error.Code, CodeIdentityLocked)
	}
}
