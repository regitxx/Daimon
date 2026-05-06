package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memoryHarness mirrors activityHarness — a Unix-socket mock daemon under a
// short tempdir (kept under AF_UNIX sun_path's 104-byte cap on darwin) that
// daemonCall dials via t.Setenv("DAIMON_HOME"). The respond callback is
// invoked once per accepted connection; the captured request is queued so
// tests can assert on the wire shape the CLI sent.
type memoryHarness struct {
	home     string
	socket   string
	listener net.Listener
	requests chan jsonrpcRequest
}

func startMemoryHarness(t *testing.T, respond func(req jsonrpcRequest) jsonrpcResponse) *memoryHarness {
	t.Helper()
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

	h := &memoryHarness{
		home:     dir,
		socket:   socket,
		listener: ln,
		requests: make(chan jsonrpcRequest, 16),
	}
	go h.serve(respond)
	return h
}

func (h *memoryHarness) serve(respond func(req jsonrpcRequest) jsonrpcResponse) {
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

func (h *memoryHarness) lastRequest(t *testing.T) jsonrpcRequest {
	t.Helper()
	select {
	case req := <-h.requests:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("no request arrived within 2s")
		return jsonrpcRequest{}
	}
}

// canned wraps a payload as a successful jsonrpcResponse — saves boilerplate
// in the per-test respond callbacks.
func canned(payload any) func(req jsonrpcRequest) jsonrpcResponse {
	return func(req jsonrpcRequest) jsonrpcResponse {
		body, _ := json.Marshal(payload)
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Result:  body,
			ID:      json.RawMessage("1"),
		}
	}
}

// --- daimon memory search --inject-preview -----------------------------------

func TestMemorySearchInjectPreview_HappyPathRendersHeaderAndBlock(t *testing.T) {
	res := contextGetResult{
		Context:       "[1] (fact) huckgod has a daughter\n[2] (preference) prefers terse responses",
		MemoryIDs:     []string{"01HMEM1", "01HMEM2"},
		TokenEstimate: 84,
	}
	h := startMemoryHarness(t, canned(res))

	var stdout, stderr bytes.Buffer
	if err := runMemorySearchInjectPreview(&stdout, &stderr, "huckgod", nil, 0, false); err != nil {
		t.Fatalf("runMemorySearchInjectPreview: %v (stderr=%q)", err, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		`[inject_preview] query="huckgod"`,
		"matched=2",
		"tokens≈84/2000", // default budget when --max-tokens unset
		"[1] (fact) huckgod has a daughter",
		"[2] (preference) prefers terse responses",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q; full output:\n%s", want, out)
		}
	}

	req := h.lastRequest(t)
	if req.Method != "daimon.context.get" {
		t.Errorf("Method: got %q want daimon.context.get", req.Method)
	}
	pj, _ := json.Marshal(req.Params)
	var got map[string]any
	_ = json.Unmarshal(pj, &got)
	if got["query"] != "huckgod" {
		t.Errorf("query: got %v want %q", got["query"], "huckgod")
	}
	if _, ok := got["max_tokens"]; ok {
		t.Errorf("expected no max_tokens on wire when unset; got %v", got)
	}
	if _, ok := got["kinds"]; ok {
		t.Errorf("expected no kinds on wire when unset; got %v", got)
	}
}

func TestMemorySearchInjectPreview_EmptyMatchPrintsStderrAndExitsZero(t *testing.T) {
	startMemoryHarness(t, canned(contextGetResult{Context: "", MemoryIDs: nil, TokenEstimate: 0}))

	var stdout, stderr bytes.Buffer
	if err := runMemorySearchInjectPreview(&stdout, &stderr, "no-such-query", nil, 0, false); err != nil {
		t.Fatalf("expected nil error on empty match; got %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected empty stdout on empty match; got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no memories matched") {
		t.Errorf("expected 'no memories matched' note; stderr=%q", stderr.String())
	}
}

func TestMemorySearchInjectPreview_WireShapeCarriesMaxTokensAndKinds(t *testing.T) {
	cases := []struct {
		name      string
		maxTokens int
		kinds     []string
		wantMax   any // nil = absent on wire
		wantKinds any // nil = absent on wire
	}{
		{"defaults", 0, nil, nil, nil},
		{"max_tokens_only", 500, nil, float64(500), nil},
		{"single_kind", 0, []string{"fact"}, nil, []any{"fact"}},
		{"multi_kind", 0, []string{"fact", "preference"}, nil, []any{"fact", "preference"}},
		{"all_set", 1024, []string{"fact"}, float64(1024), []any{"fact"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := startMemoryHarness(t, canned(contextGetResult{
				Context:       "[1] (fact) ...",
				MemoryIDs:     []string{"01HX"},
				TokenEstimate: 5,
			}))

			var stdout, stderr bytes.Buffer
			if err := runMemorySearchInjectPreview(&stdout, &stderr, "q", tc.kinds, tc.maxTokens, false); err != nil {
				t.Fatalf("runMemorySearchInjectPreview: %v", err)
			}
			req := h.lastRequest(t)
			pj, _ := json.Marshal(req.Params)
			var got map[string]any
			_ = json.Unmarshal(pj, &got)

			if tc.wantMax == nil {
				if _, ok := got["max_tokens"]; ok {
					t.Errorf("expected no max_tokens on wire; got %v", got)
				}
			} else if got["max_tokens"] != tc.wantMax {
				t.Errorf("max_tokens: got %v want %v", got["max_tokens"], tc.wantMax)
			}

			if tc.wantKinds == nil {
				if _, ok := got["kinds"]; ok {
					t.Errorf("expected no kinds on wire; got %v", got)
				}
			} else {
				kinds, ok := got["kinds"].([]any)
				if !ok {
					t.Fatalf("kinds: expected []any; got %T (%v)", got["kinds"], got["kinds"])
				}
				if len(kinds) != len(tc.wantKinds.([]any)) {
					t.Errorf("kinds len: got %d want %d", len(kinds), len(tc.wantKinds.([]any)))
				}
				for i, k := range kinds {
					if k != tc.wantKinds.([]any)[i] {
						t.Errorf("kinds[%d]: got %v want %v", i, k, tc.wantKinds.([]any)[i])
					}
				}
			}
		})
	}
}

func TestMemorySearchInjectPreview_BudgetReflectsExplicitMaxTokens(t *testing.T) {
	startMemoryHarness(t, canned(contextGetResult{
		Context:       "[1] (fact) only one",
		MemoryIDs:     []string{"01HMEM"},
		TokenEstimate: 12,
	}))
	var stdout, stderr bytes.Buffer
	if err := runMemorySearchInjectPreview(&stdout, &stderr, "q", nil, 750, false); err != nil {
		t.Fatalf("runMemorySearchInjectPreview: %v", err)
	}
	if !strings.Contains(stdout.String(), "tokens≈12/750") {
		t.Errorf("expected explicit budget in header; got:\n%s", stdout.String())
	}
}

func TestMemorySearchInjectPreview_JSONRoundtripsEnvelope(t *testing.T) {
	res := contextGetResult{
		Context:       "[1] (fact) ...",
		MemoryIDs:     []string{"01HMEM1"},
		TokenEstimate: 7,
	}
	startMemoryHarness(t, canned(res))

	var stdout, stderr bytes.Buffer
	if err := runMemorySearchInjectPreview(&stdout, &stderr, "q", nil, 0, true); err != nil {
		t.Fatalf("runMemorySearchInjectPreview --json: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("--json should not write to stderr; got %q", stderr.String())
	}
	var back map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &back); err != nil {
		t.Fatalf("Unmarshal --json output: %v\n%s", err, stdout.String())
	}
	if back["context"] != res.Context {
		t.Errorf("context in JSON: got %v want %q", back["context"], res.Context)
	}
	ids, _ := back["memory_ids"].([]any)
	if len(ids) != 1 || ids[0] != "01HMEM1" {
		t.Errorf("memory_ids in JSON: got %v want [01HMEM1]", back["memory_ids"])
	}
	if v, _ := back["token_estimate"].(float64); int(v) != 7 {
		t.Errorf("token_estimate in JSON: got %v want 7", back["token_estimate"])
	}
}

func TestMemorySearchInjectPreview_DaemonErrorsHumanise(t *testing.T) {
	t.Run("locked", func(t *testing.T) {
		startMemoryHarness(t, func(req jsonrpcRequest) jsonrpcResponse {
			return jsonrpcResponse{
				JSONRPC: "2.0",
				Error:   &jsonrpcError{Code: codeIdentityLocked, Message: "locked"},
				ID:      json.RawMessage("1"),
			}
		})
		var stdout, stderr bytes.Buffer
		err := runMemorySearchInjectPreview(&stdout, &stderr, "q", nil, 0, false)
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
		err = runMemorySearchInjectPreview(&stdout, &stderr, "q", nil, 0, false)
		if err == nil {
			t.Fatal("expected error from absent daemon; got nil")
		}
		if !strings.Contains(err.Error(), "daemon not running") {
			t.Errorf("expected 'daemon not running' hint; got %v", err)
		}
	})
}

// --- cmdMemorySearch dispatcher branching ------------------------------------

// These cover the flag-parsing surface in cmdMemorySearch: --inject-preview
// flips the mode; --limit and --max-tokens are exclusive across the modes;
// multi --kind is allowed only in inject-preview mode. cmdMemorySearch wires
// os.Stdout/os.Stderr, so we drive it via the dispatcher and assert on the
// returned error (mode-misuse cases never reach the RPC, so no harness
// needed).

func TestCmdMemorySearch_RejectsLimitWithInjectPreview(t *testing.T) {
	dir, _ := os.MkdirTemp("", "dmn")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("DAIMON_HOME", dir)

	err := cmdMemorySearch([]string{"--inject-preview", "--limit", "5", "q"})
	if err == nil {
		t.Fatal("expected error for --limit + --inject-preview; got nil")
	}
	if !strings.Contains(err.Error(), "--limit is meaningless") {
		t.Errorf("expected '--limit is meaningless' error; got %v", err)
	}
}

func TestCmdMemorySearch_RejectsMaxTokensWithoutInjectPreview(t *testing.T) {
	dir, _ := os.MkdirTemp("", "dmn")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("DAIMON_HOME", dir)

	err := cmdMemorySearch([]string{"--max-tokens", "500", "q"})
	if err == nil {
		t.Fatal("expected error for --max-tokens without --inject-preview; got nil")
	}
	if !strings.Contains(err.Error(), "--max-tokens is only valid with --inject-preview") {
		t.Errorf("expected '--max-tokens is only valid with --inject-preview' error; got %v", err)
	}
}

func TestCmdMemorySearch_RejectsMultiKindInSearchMode(t *testing.T) {
	dir, _ := os.MkdirTemp("", "dmn")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("DAIMON_HOME", dir)

	err := cmdMemorySearch([]string{"--kind", "fact", "--kind", "preference", "q"})
	if err == nil {
		t.Fatal("expected error for multi --kind in search mode; got nil")
	}
	if !strings.Contains(err.Error(), "single-valued in search mode") {
		t.Errorf("expected 'single-valued in search mode' error; got %v", err)
	}
}

func TestCmdMemorySearch_RejectsMissingPositional(t *testing.T) {
	dir, _ := os.MkdirTemp("", "dmn")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("DAIMON_HOME", dir)

	t.Run("search_mode", func(t *testing.T) {
		err := cmdMemorySearch([]string{"--kind", "fact"})
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("expected usage error; got %v", err)
		}
		if strings.Contains(err.Error(), "--inject-preview") {
			t.Errorf("expected search-mode usage; got %v", err)
		}
	})
	t.Run("inject_preview_mode", func(t *testing.T) {
		err := cmdMemorySearch([]string{"--inject-preview", "--kind", "fact"})
		if err == nil || !strings.Contains(err.Error(), "--inject-preview") {
			t.Errorf("expected inject-preview-mode usage; got %v", err)
		}
	})
}

func TestCmdMemorySearch_DispatchesInjectPreviewToRunner(t *testing.T) {
	// End-to-end through cmdMemorySearch (the dispatcher) — proves the parsed
	// flags are threaded into runMemorySearchInjectPreview's RPC. Asserts on
	// the wire shape only; rendered output goes to os.Stdout which we don't
	// capture here (the runner-level tests above already cover rendering).
	h := startMemoryHarness(t, canned(contextGetResult{
		Context:       "[1] (fact) ...",
		MemoryIDs:     []string{"01HX"},
		TokenEstimate: 5,
	}))
	if err := cmdMemorySearch([]string{
		"--inject-preview",
		"--kind", "fact",
		"--kind", "preference",
		"--max-tokens", "1024",
		"q",
	}); err != nil {
		t.Fatalf("cmdMemorySearch: %v", err)
	}
	req := h.lastRequest(t)
	if req.Method != "daimon.context.get" {
		t.Errorf("Method: got %q want daimon.context.get", req.Method)
	}
	pj, _ := json.Marshal(req.Params)
	var got map[string]any
	_ = json.Unmarshal(pj, &got)
	if got["max_tokens"] != float64(1024) {
		t.Errorf("max_tokens: got %v want 1024", got["max_tokens"])
	}
	kinds, _ := got["kinds"].([]any)
	if len(kinds) != 2 || kinds[0] != "fact" || kinds[1] != "preference" {
		t.Errorf("kinds: got %v want [fact preference]", got["kinds"])
	}
}
