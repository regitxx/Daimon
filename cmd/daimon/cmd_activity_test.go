package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// activityHarness wires up a mock daemon listening on $DAIMON_HOME/daimon.sock
// inside a short tempdir (kept under AF_UNIX sun_path's 104-byte cap on
// darwin) and points daemonCall at it via t.Setenv("DAIMON_HOME", ...).
//
// The respond callback is invoked once per accepted connection; the request
// is captured into the returned channel so tests can assert on the wire shape
// the CLI sent. Drains and closes deterministically on test exit.
type activityHarness struct {
	home    string
	socket  string
	listener net.Listener
	requests chan jsonrpcRequest
}

// startActivityHarness installs t.Setenv("DAIMON_HOME", shortTempDir) and
// starts a Unix-socket mock daemon on the resolved socket path. respond is
// called once per request and may inspect the captured request to choose its
// reply.
func startActivityHarness(t *testing.T, respond func(req jsonrpcRequest) jsonrpcResponse) *activityHarness {
	t.Helper()
	// AF_UNIX sun_path is capped at 104 bytes on darwin. t.TempDir() yields
	// ~70-byte paths; appending "/daimon.sock" pushes some test cases over
	// the cap and triggers the daimonhome.SocketPath fallback to $TMPDIR,
	// which would dial a different path than this listener opens. Use the
	// same MkdirTemp("","dmn") trick the doctor tests use to get a ~10-byte
	// prefix, well under the cap.
	dir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	t.Setenv("DAIMON_HOME", dir)
	socket := filepath.Join(dir, "daimon.sock")

	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("Listen unix %s (len=%d): %v", socket, len(socket), err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	h := &activityHarness{
		home:     dir,
		socket:   socket,
		listener: ln,
		requests: make(chan jsonrpcRequest, 16),
	}
	go h.serve(respond)
	return h
}

func (h *activityHarness) serve(respond func(req jsonrpcRequest) jsonrpcResponse) {
	for {
		c, err := h.listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			var req jsonrpcRequest
			if err := json.NewDecoder(c).Decode(&req); err != nil {
				return
			}
			select {
			case h.requests <- req:
			default:
			}
			_ = json.NewEncoder(c).Encode(respond(req))
		}(c)
	}
}

// lastRequest pulls the most recently captured request, failing the test if
// none arrived. Each subcommand call sends exactly one request, so this
// closes the loop on "did the CLI send what we expected?".
func (h *activityHarness) lastRequest(t *testing.T) jsonrpcRequest {
	t.Helper()
	select {
	case req := <-h.requests:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("no request arrived within 2s")
		return jsonrpcRequest{}
	}
}

// fixedRespond returns a respond callback that always replies with the same
// canned entries — useful when a test's wire-shape assertion is the only
// thing that matters.
func fixedRespond(entries []activityEntry) func(req jsonrpcRequest) jsonrpcResponse {
	return func(req jsonrpcRequest) jsonrpcResponse {
		body, _ := json.Marshal(entries)
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Result:  body,
			ID:      json.RawMessage("1"),
		}
	}
}

func mustEntry(t *testing.T, kind string, ts int64, payload map[string]any) activityEntry {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}
	return activityEntry{
		ID:        "01H0TEST" + kind,
		Timestamp: ts,
		Kind:      kind,
		Payload:   body,
		PrevHash:  "blake3:0",
		Hash:      "blake3:test",
	}
}

func TestActivityQuery_HappyPath_RendersAllKinds(t *testing.T) {
	now := time.Now().UnixMilli()
	entries := []activityEntry{
		mustEntry(t, "daimon.created", now-3000, map[string]any{
			"version": "v0.1.0-dev",
			"did":     "did:key:zTestDID",
		}),
		mustEntry(t, "memory.write", now-2000, map[string]any{
			"id":     "01H0MEM",
			"kind":   "fact",
			"source": "user-write",
		}),
		mustEntry(t, "provider.invoke", now-1000, map[string]any{
			"provider":            "ollama",
			"model":               "llama3.2:latest",
			"streamed":            true,
			"input_tokens":        5,
			"output_tokens":       42,
			"injected_memory_ids": []string{"a", "b"},
		}),
	}
	h := startActivityHarness(t, fixedRespond(entries))

	var stdout, stderr bytes.Buffer
	if err := runActivityQuery(&stdout, &stderr, []string{}); err != nil {
		t.Fatalf("runActivityQuery: %v (stderr=%q)", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"TIME", "KIND", "ID", "SUMMARY",
		"daimon.created", "did=did:key:zTestDID",
		"memory.write", "id=01H0MEM kind=fact",
		"provider.invoke", "ollama/llama3.2:latest streamed=true matched=2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered table missing %q; full output:\n%s", want, out)
		}
	}

	// Wire shape: empty filters → no since/kind, default limit=50.
	req := h.lastRequest(t)
	if req.Method != "daimon.activity.query" {
		t.Errorf("Method: got %q want daimon.activity.query", req.Method)
	}
	pj, _ := json.Marshal(req.Params)
	var got map[string]any
	_ = json.Unmarshal(pj, &got)
	if _, ok := got["since"]; ok {
		t.Errorf("expected no 'since' in params; got %v", got)
	}
	if _, ok := got["kind"]; ok {
		t.Errorf("expected no 'kind' in params; got %v", got)
	}
	if v, _ := got["limit"].(float64); int(v) != 50 {
		t.Errorf("limit: got %v want 50", got["limit"])
	}
}

func TestActivityQuery_EmptyLogPrintsNoEntriesNote(t *testing.T) {
	startActivityHarness(t, fixedRespond([]activityEntry{}))

	var stdout, stderr bytes.Buffer
	if err := runActivityQuery(&stdout, &stderr, []string{}); err != nil {
		t.Fatalf("runActivityQuery: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout; got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no activity entries") {
		t.Errorf("expected 'no activity entries' note; stderr=%q", stderr.String())
	}
}

func TestActivityQuery_WireParams(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantSince func(int64) bool // predicate; true = OK
		wantKind  string           // "" = absent
		wantLimit int              // 0 = absent
	}{
		{
			name:      "duration_since",
			args:      []string{"--since", "1h", "--limit", "10"},
			wantSince: func(v int64) bool { return v > 0 && time.Now().UnixMilli()-v < 2*time.Hour.Milliseconds() && time.Now().UnixMilli()-v > time.Hour.Milliseconds()-5000 },
			wantLimit: 10,
		},
		{
			name:      "rfc3339_since",
			args:      []string{"--since", "2026-05-05T00:00:00Z"},
			wantSince: func(v int64) bool { return v == time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC).UnixMilli() },
			wantLimit: 50,
		},
		{
			name:      "single_kind_passes_through",
			args:      []string{"--kind", "memory.write"},
			wantKind:  "memory.write",
			wantLimit: 50,
		},
		{
			name:      "multiple_kinds_omits_wire_kind_and_limit",
			args:      []string{"--kind", "memory.write", "--kind", "provider.invoke"},
			wantKind:  "",
			wantLimit: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := startActivityHarness(t, fixedRespond(nil))
			var stdout, stderr bytes.Buffer
			if err := runActivityQuery(&stdout, &stderr, tc.args); err != nil {
				t.Fatalf("runActivityQuery: %v", err)
			}
			req := h.lastRequest(t)
			pj, _ := json.Marshal(req.Params)
			var got map[string]any
			_ = json.Unmarshal(pj, &got)
			if tc.wantKind == "" {
				if _, ok := got["kind"]; ok {
					t.Errorf("expected no 'kind' on wire; got %v", got)
				}
			} else if got["kind"] != tc.wantKind {
				t.Errorf("kind: got %v want %q", got["kind"], tc.wantKind)
			}
			if tc.wantLimit == 0 {
				if _, ok := got["limit"]; ok {
					t.Errorf("expected no 'limit' on wire; got %v", got)
				}
			} else if v, _ := got["limit"].(float64); int(v) != tc.wantLimit {
				t.Errorf("limit: got %v want %d", got["limit"], tc.wantLimit)
			}
			if tc.wantSince != nil {
				v, _ := got["since"].(float64)
				if !tc.wantSince(int64(v)) {
					t.Errorf("since predicate failed; got %v", v)
				}
			} else {
				if _, ok := got["since"]; ok {
					t.Errorf("expected no 'since' on wire; got %v", got)
				}
			}
		})
	}
}

func TestActivityQuery_MultiKindFiltersClientSide(t *testing.T) {
	now := time.Now().UnixMilli()
	all := []activityEntry{
		mustEntry(t, "daimon.created", now-4000, map[string]any{"did": "did:key:z1"}),
		mustEntry(t, "memory.write", now-3000, map[string]any{"id": "m1", "kind": "fact"}),
		mustEntry(t, "activity.queried", now-2000, map[string]any{"matched": 5}),
		mustEntry(t, "provider.invoke", now-1000, map[string]any{
			"provider": "ollama", "model": "llama3.2", "streamed": false,
		}),
	}
	startActivityHarness(t, fixedRespond(all))

	var stdout, stderr bytes.Buffer
	if err := runActivityQuery(&stdout, &stderr,
		[]string{"--kind", "memory.write", "--kind", "provider.invoke"}); err != nil {
		t.Fatalf("runActivityQuery: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "memory.write") {
		t.Errorf("expected memory.write row; got:\n%s", out)
	}
	if !strings.Contains(out, "provider.invoke") {
		t.Errorf("expected provider.invoke row; got:\n%s", out)
	}
	if strings.Contains(out, "daimon.created") {
		t.Errorf("did NOT expect daimon.created row; got:\n%s", out)
	}
	if strings.Contains(out, "activity.queried") {
		t.Errorf("did NOT expect activity.queried row; got:\n%s", out)
	}
}

func TestActivityQuery_JSONOutputRoundtrips(t *testing.T) {
	now := time.Now().UnixMilli()
	entries := []activityEntry{
		mustEntry(t, "memory.write", now, map[string]any{"id": "m1", "kind": "fact"}),
	}
	startActivityHarness(t, fixedRespond(entries))

	var stdout, stderr bytes.Buffer
	if err := runActivityQuery(&stdout, &stderr, []string{"--json"}); err != nil {
		t.Fatalf("runActivityQuery: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("--json should not write to stderr; got %q", stderr.String())
	}
	var back []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &back); err != nil {
		t.Fatalf("Unmarshal --json output: %v\n%s", err, stdout.String())
	}
	if len(back) != 1 {
		t.Fatalf("expected 1 entry; got %d", len(back))
	}
	if got := back[0]["kind"]; got != "memory.write" {
		t.Errorf("kind in JSON: got %v want memory.write", got)
	}
}

func TestActivityQuery_DaemonErrors_HumaniseIntoActionableHints(t *testing.T) {
	t.Run("locked", func(t *testing.T) {
		startActivityHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
			return jsonrpcResponse{
				JSONRPC: "2.0",
				Error:   &jsonrpcError{Code: codeIdentityLocked, Message: "locked"},
				ID:      json.RawMessage("1"),
			}
		})
		var stdout, stderr bytes.Buffer
		err := runActivityQuery(&stdout, &stderr, []string{})
		if err == nil {
			t.Fatal("expected error from locked daemon; got nil")
		}
		if !strings.Contains(err.Error(), "daimon unlock") {
			t.Errorf("expected actionable hint; got %v", err)
		}
	})

	t.Run("not_running", func(t *testing.T) {
		// Set DAIMON_HOME to a short tempdir but DON'T start a listener — the
		// dial returns ENOENT and humaniseDaemonErr should rewrite to the
		// "daemon not running" hint.
		dir, err := os.MkdirTemp("", "dmn")
		if err != nil {
			t.Fatalf("MkdirTemp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		t.Setenv("DAIMON_HOME", dir)

		var stdout, stderr bytes.Buffer
		err = runActivityQuery(&stdout, &stderr, []string{})
		if err == nil {
			t.Fatal("expected error from absent daemon; got nil")
		}
		if !strings.Contains(err.Error(), "daemon not running") {
			t.Errorf("expected 'daemon not running' hint; got %v", err)
		}
	})
}

func TestActivityQuery_BadFlagValues(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"bad_since", []string{"--since", "yesterday"}, "--since"},
		{"empty_kind", []string{"--kind", ""}, "--kind"},
		{"positional_arg", []string{"unexpected"}, "no positional"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No daemon — flag parsing should fail before any RPC call.
			dir, err := os.MkdirTemp("", "dmn")
			if err != nil {
				t.Fatalf("MkdirTemp: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			t.Setenv("DAIMON_HOME", dir)

			var stdout, stderr bytes.Buffer
			err = runActivityQuery(&stdout, &stderr, tc.args)
			if err == nil {
				t.Fatalf("expected error; got nil (stderr=%q)", stderr.String())
			}
			combined := err.Error() + stderr.String()
			if !strings.Contains(combined, tc.want) {
				t.Errorf("expected error mentioning %q; got %q", tc.want, combined)
			}
		})
	}
}

func TestSummarizeEntry_PerKindShapes(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload map[string]any
		want    string
	}{
		{
			name:    "provider_invoke_full",
			kind:    "provider.invoke",
			payload: map[string]any{"provider": "claude", "model": "claude-3-5-sonnet", "streamed": true, "injected_memory_ids": []any{"a", "b", "c"}},
			want:    "claude/claude-3-5-sonnet streamed=true matched=3",
		},
		{
			name:    "provider_invoke_no_inject",
			kind:    "provider.invoke",
			payload: map[string]any{"provider": "ollama", "model": "llama3.2", "streamed": false},
			want:    "ollama/llama3.2 streamed=false matched=0",
		},
		{
			name:    "memory_write_with_kind",
			kind:    "memory.write",
			payload: map[string]any{"id": "01HX", "kind": "fact", "source": "user"},
			want:    "id=01HX kind=fact",
		},
		{
			name:    "memory_write_no_kind",
			kind:    "memory.write",
			payload: map[string]any{"id": "01HY"},
			want:    "id=01HY",
		},
		{
			name:    "memory_export",
			kind:    "memory.export",
			payload: map[string]any{"memories": float64(7)},
			want:    "memories=7",
		},
		{
			name:    "memory_import",
			kind:    "memory.import",
			payload: map[string]any{"imported": float64(3), "skipped": float64(1)},
			want:    "imported=3 skipped=1",
		},
		{
			name:    "activity_queried",
			kind:    "activity.queried",
			payload: map[string]any{"matched": float64(12)},
			want:    "matched=12",
		},
		{
			name:    "activity_verified",
			kind:    "activity.verified",
			payload: map[string]any{"verified": float64(42)},
			want:    "verified=42",
		},
		{
			name:    "context_previewed",
			kind:    "context.previewed",
			payload: map[string]any{"query": "huckgod", "matched": float64(3)},
			want:    `query="huckgod" matched=3`,
		},
		{
			name:    "daimon_created",
			kind:    "daimon.created",
			payload: map[string]any{"version": "v0.1.0-dev", "did": "did:key:zABC"},
			want:    "did=did:key:zABC",
		},
		{
			name:    "unrecognised_kind_is_empty",
			kind:    "future.kind.we.dont.know",
			payload: map[string]any{"foo": "bar"},
			want:    "",
		},
		{
			name:    "missing_payload_is_empty",
			kind:    "provider.invoke",
			payload: nil,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body json.RawMessage
			if tc.payload != nil {
				b, err := json.Marshal(tc.payload)
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				body = b
			}
			e := activityEntry{ID: "x", Kind: tc.kind, Payload: body}
			if got := summarizeEntry(e); got != tc.want {
				t.Errorf("summarizeEntry(%s): got %q want %q", tc.kind, got, tc.want)
			}
		})
	}
}

// --- daimon activity verify --------------------------------------------------

func TestActivityVerify_HappyPathRendersCount(t *testing.T) {
	startActivityHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
		body, _ := json.Marshal(activityVerifyResult{Verified: 7, OK: true})
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Result:  body,
			ID:      json.RawMessage("1"),
		}
	})
	var stdout, stderr bytes.Buffer
	if err := runActivityVerify(&stdout, &stderr, []string{}); err != nil {
		t.Fatalf("runActivityVerify: %v (stderr=%q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "verified 7 entries") {
		t.Errorf("expected 'verified 7 entries' in stdout; got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "chain ok") {
		t.Errorf("expected 'chain ok' in stdout; got %q", stdout.String())
	}
}

func TestActivityVerify_JSONOutputRoundtrips(t *testing.T) {
	startActivityHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
		body, _ := json.Marshal(activityVerifyResult{Verified: 42, OK: true})
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Result:  body,
			ID:      json.RawMessage("1"),
		}
	})
	var stdout, stderr bytes.Buffer
	if err := runActivityVerify(&stdout, &stderr, []string{"--json"}); err != nil {
		t.Fatalf("runActivityVerify --json: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("--json should not write to stderr on success; got %q", stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal --json output: %v\n%s", err, stdout.String())
	}
	if v, _ := got["verified"].(float64); int(v) != 42 {
		t.Errorf("verified in JSON: got %v want 42", got["verified"])
	}
	if got["ok"] != true {
		t.Errorf("ok in JSON: got %v want true", got["ok"])
	}
}

func TestActivityVerify_ChainCorruptReturnsErrorAndNonZero(t *testing.T) {
	startActivityHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Error: &jsonrpcError{
				Code:    -32603, // CodeInternalError
				Message: "activity chain broken",
				Data:    json.RawMessage(`"activity: chain broken: id=01HXYZ expected_prev=blake3:abc got_prev=blake3:def"`),
			},
			ID: json.RawMessage("1"),
		}
	})
	var stdout, stderr bytes.Buffer
	err := runActivityVerify(&stdout, &stderr, []string{})
	if err == nil {
		t.Fatal("expected error on chain corruption; got nil")
	}
	if !strings.Contains(err.Error(), "chain INVALID") {
		t.Errorf("expected 'chain INVALID' in returned error; got %v", err)
	}
	if !strings.Contains(err.Error(), "chain broken") {
		t.Errorf("expected underlying reason in returned error; got %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("non-JSON failure should not write stdout (avoid duplicate with exitOnErr); got %q", stdout.String())
	}
}

func TestActivityVerify_ChainCorruptJSONEnvelopeShape(t *testing.T) {
	startActivityHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Error: &jsonrpcError{
				Code:    -32603,
				Message: "activity payload AEAD authentication failed",
			},
			ID: json.RawMessage("1"),
		}
	})
	var stdout, stderr bytes.Buffer
	err := runActivityVerify(&stdout, &stderr, []string{"--json"})
	if err == nil {
		t.Fatal("expected error on AEAD failure; got nil")
	}
	if !strings.Contains(err.Error(), "chain INVALID") {
		t.Errorf("expected 'chain INVALID' in returned error; got %v", err)
	}
	var got map[string]any
	if jerr := json.Unmarshal(stdout.Bytes(), &got); jerr != nil {
		t.Fatalf("Unmarshal --json failure envelope: %v\n%s", jerr, stdout.String())
	}
	if got["ok"] != false {
		t.Errorf("ok in JSON failure: got %v want false", got["ok"])
	}
	if _, ok := got["error"].(string); !ok {
		t.Errorf("expected 'error' string in JSON failure envelope; got %v", got)
	}
}

func TestActivityVerify_DaemonErrors_HumaniseIntoActionableHints(t *testing.T) {
	t.Run("locked", func(t *testing.T) {
		startActivityHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
			return jsonrpcResponse{
				JSONRPC: "2.0",
				Error:   &jsonrpcError{Code: codeIdentityLocked, Message: "locked"},
				ID:      json.RawMessage("1"),
			}
		})
		var stdout, stderr bytes.Buffer
		err := runActivityVerify(&stdout, &stderr, []string{})
		if err == nil {
			t.Fatal("expected error from locked daemon; got nil")
		}
		if !strings.Contains(err.Error(), "daimon unlock") {
			t.Errorf("expected actionable hint; got %v", err)
		}
	})

	t.Run("not_running", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "dmn")
		if err != nil {
			t.Fatalf("MkdirTemp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		t.Setenv("DAIMON_HOME", dir)

		var stdout, stderr bytes.Buffer
		err = runActivityVerify(&stdout, &stderr, []string{})
		if err == nil {
			t.Fatal("expected error from absent daemon; got nil")
		}
		if !strings.Contains(err.Error(), "daemon not running") {
			t.Errorf("expected 'daemon not running' hint; got %v", err)
		}
	})
}

func TestActivityVerify_RejectsPositionalArgs(t *testing.T) {
	dir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("DAIMON_HOME", dir)

	var stdout, stderr bytes.Buffer
	err = runActivityVerify(&stdout, &stderr, []string{"unexpected"})
	if err == nil {
		t.Fatal("expected error for positional arg; got nil")
	}
	if !strings.Contains(err.Error(), "no positional") {
		t.Errorf("expected 'no positional' in error; got %v", err)
	}
}

// concurrencyGuard is a smoke check that the harness's request capture is
// race-free under -race. Listed at the bottom because it's belt-and-braces
// rather than functional coverage.
func TestActivityQuery_HarnessIsRaceClean(t *testing.T) {
	var calls atomic.Int32
	startActivityHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
		calls.Add(1)
		return fixedRespond(nil)(req)
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var stdout, stderr bytes.Buffer
			_ = runActivityQuery(&stdout, &stderr, []string{})
		}()
	}
	wg.Wait()
	if calls.Load() != 4 {
		t.Errorf("expected 4 RPC calls; got %d", calls.Load())
	}
}
