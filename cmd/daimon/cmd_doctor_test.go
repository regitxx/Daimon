package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
)

// shortAbsentSocket returns a guaranteed-absent, AF_UNIX-safe (<=104 byte)
// socket path. t.TempDir() on darwin produces ~110+ byte paths which overflow
// sun_path and surface as EINVAL — masking the ENOENT we actually want to
// classify as "not_running". Mirrors the MkdirTemp("","dmn") trick the
// internal/server tests use.
func shortAbsentSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// helper: a doctor config wired to a tempdir home and the supplied runtime
// endpoints; the daemon-socket override is left empty so the caller can plug
// in a per-test socket path (or leave empty to test the not_running path).
func newTestDoctorConfig(t *testing.T, home, ollama, lmstudio, socket string) doctorConfig {
	t.Helper()
	cfg := doctorConfig{
		HomeOverride:     home,
		SocketOverride:   socket,
		OllamaEndpoint:   ollama,
		LMStudioEndpoint: lmstudio,
		Timeout:          500 * time.Millisecond,
		HTTPClient:       &http.Client{Timeout: 500 * time.Millisecond},
	}
	return cfg
}

// shieldEnv clears the env vars doctor reads so an outer test environment
// doesn't pollute the report under test. Restored automatically on test exit.
func shieldEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "LMSTUDIO_API_KEY",
		"OLLAMA_HOST", "LMSTUDIO_HOST",
		"DAIMON_HOME",
	} {
		t.Setenv(k, "")
	}
}

func TestDoctor_FreshHomeNoDaemonNoRuntimes(t *testing.T) {
	shieldEnv(t)
	home := t.TempDir()

	// Use a guaranteed-absent socket path so probeDaemon hits ENOENT and
	// classifies as not_running. Must be short enough for AF_UNIX sun_path.
	socket := shortAbsentSocket(t)
	cfg := newTestDoctorConfig(t, home,
		"http://127.0.0.1:1", // unreachable port
		"http://127.0.0.1:1",
		socket,
	)
	rep := gatherDoctorReport(context.Background(), cfg)

	if rep.Home.Resolved != home {
		t.Errorf("Home.Resolved: got %q want %q", rep.Home.Resolved, home)
	}
	if rep.Home.Keystore.Present {
		t.Error("expected keystore absent in fresh home")
	}
	if rep.Home.MemoryDB.Present {
		t.Error("expected memory.db absent in fresh home")
	}
	if rep.Home.ActivityLog.Present {
		t.Error("expected activity.log absent in fresh home")
	}
	if rep.Daemon.State != "not_running" {
		t.Errorf("Daemon.State: got %q want %q", rep.Daemon.State, "not_running")
	}
	if rep.Env.AnthropicAPIKey || rep.Env.OpenAIAPIKey || rep.Env.LMStudioAPIKey {
		t.Errorf("expected all API key env vars unset, got %+v", rep.Env)
	}
	if rep.Runtimes.Ollama.Reachable {
		t.Error("expected Ollama unreachable")
	}
	if rep.Runtimes.LMStudio.Reachable {
		t.Error("expected LM Studio unreachable")
	}
}

func TestDoctor_PopulatedHomeReportsEntryCountAndLastHash(t *testing.T) {
	shieldEnv(t)
	home := t.TempDir()

	// Plant a fake keystore + memory.db so the on-disk stats are populated.
	mustWriteFile(t, filepath.Join(home, "identity.keystore"), []byte("fake-keystore"))
	mustWriteFile(t, filepath.Join(home, "memory.db"), bytes.Repeat([]byte("x"), 4096))

	// Real activity log with two valid entries — use the production Open/Append
	// path so the chain is real and ScanLastHash actually has something to walk.
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	logPath := filepath.Join(home, "activity.log")
	alog, err := activity.Open(logPath, id)
	if err != nil {
		t.Fatalf("activity.Open: %v", err)
	}
	first, err := alog.Append(context.Background(), activity.KindDaimonCreated, map[string]any{"v": "test"})
	if err != nil {
		t.Fatalf("Append #1: %v", err)
	}
	second, err := alog.Append(context.Background(), activity.KindMemoryWrite, map[string]any{"id": "m1"})
	if err != nil {
		t.Fatalf("Append #2: %v", err)
	}
	_ = alog.Close()

	cfg := newTestDoctorConfig(t, home,
		"http://127.0.0.1:1",
		"http://127.0.0.1:1",
		shortAbsentSocket(t),
	)
	rep := gatherDoctorReport(context.Background(), cfg)

	if !rep.Home.Keystore.Present || rep.Home.Keystore.Size == 0 {
		t.Errorf("Keystore not reported present: %+v", rep.Home.Keystore)
	}
	if !rep.Home.MemoryDB.Present || rep.Home.MemoryDB.Size != 4096 {
		t.Errorf("MemoryDB not reported with size 4096: %+v", rep.Home.MemoryDB)
	}
	if !rep.Home.ActivityLog.Present {
		t.Errorf("ActivityLog not reported present: %+v", rep.Home.ActivityLog)
	}
	if rep.Home.ActivityLog.Entries != 2 {
		t.Errorf("ActivityLog.Entries: got %d want 2", rep.Home.ActivityLog.Entries)
	}
	if rep.Home.ActivityLog.LastHash != second.Hash {
		t.Errorf("ActivityLog.LastHash: got %q want %q", rep.Home.ActivityLog.LastHash, second.Hash)
	}
	if rep.Home.ActivityLog.LastHash == first.Hash {
		t.Errorf("LastHash should be the second entry's hash, not the first")
	}
}

func TestDoctor_DaemonStates(t *testing.T) {
	cases := []struct {
		name      string
		respond   func(req jsonrpcRequest) jsonrpcResponse
		wantState string
		wantDID   string
	}{
		{
			name: "locked",
			respond: func(req jsonrpcRequest) jsonrpcResponse {
				return jsonrpcResponse{
					JSONRPC: "2.0",
					Error:   &jsonrpcError{Code: codeIdentityLocked, Message: "locked"},
					ID:      json.RawMessage("1"),
				}
			},
			wantState: "locked",
		},
		{
			name: "unlocked",
			respond: func(req jsonrpcRequest) jsonrpcResponse {
				result, _ := json.Marshal(map[string]string{"did": "did:key:zTest"})
				return jsonrpcResponse{
					JSONRPC: "2.0",
					Result:  result,
					ID:      json.RawMessage("1"),
				}
			},
			wantState: "unlocked",
			wantDID:   "did:key:zTest",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shieldEnv(t)
			socket := startMockDaemon(t, tc.respond)
			cfg := newTestDoctorConfig(t, t.TempDir(),
				"http://127.0.0.1:1",
				"http://127.0.0.1:1",
				socket,
			)
			rep := gatherDoctorReport(context.Background(), cfg)
			if rep.Daemon.State != tc.wantState {
				t.Errorf("Daemon.State: got %q want %q (dial_error=%q)",
					rep.Daemon.State, tc.wantState, rep.Daemon.DialError)
			}
			if tc.wantDID != "" && rep.Daemon.DID != tc.wantDID {
				t.Errorf("Daemon.DID: got %q want %q", rep.Daemon.DID, tc.wantDID)
			}
		})
	}
}

func TestDoctor_WalletProbeStates(t *testing.T) {
	// Each case is a mock daemon that answers BOTH identity.get (to land
	// the daemon at state=unlocked, the precondition for wallet probing)
	// AND wallet.list (the verb under test). Captures the three states
	// the wallet probe distinguishes: ok with wallets, ok without
	// wallets, and the "not_loaded" silent-failure mode that motivated
	// adding this whole section.
	cases := []struct {
		name          string
		walletResp    func(req jsonrpcRequest) jsonrpcResponse
		wantState     string
		wantCount     int
		wantHasChains bool
	}{
		{
			name: "ok_with_wallets",
			walletResp: func(req jsonrpcRequest) jsonrpcResponse {
				result, _ := json.Marshal([]map[string]any{
					{"id": "01K", "chain": "evm:base", "address": "0xABC"},
					{"id": "02K", "chain": "evm:base-sepolia", "address": "0xDEF"},
				})
				return jsonrpcResponse{JSONRPC: "2.0", Result: result, ID: json.RawMessage("1")}
			},
			wantState:     "ok",
			wantCount:     2,
			wantHasChains: true,
		},
		{
			name: "ok_no_wallets",
			walletResp: func(req jsonrpcRequest) jsonrpcResponse {
				result, _ := json.Marshal([]map[string]any{})
				return jsonrpcResponse{JSONRPC: "2.0", Result: result, ID: json.RawMessage("1")}
			},
			wantState: "ok",
			wantCount: 0,
		},
		{
			name: "not_loaded_silently_disabled",
			walletResp: func(req jsonrpcRequest) jsonrpcResponse {
				// Mirror the canonical walletNotReady() error shape from
				// internal/server/wallet_handlers.go — CodeInvalidRequest
				// with the "wallet keystore not loaded" message. The doctor
				// matches on the message because CodeInvalidRequest covers
				// many things.
				return jsonrpcResponse{
					JSONRPC: "2.0",
					Error: &jsonrpcError{
						Code:    codeInvalidRequest,
						Message: "wallet keystore not loaded; check daemon logs for keystore open failures",
					},
					ID: json.RawMessage("1"),
				}
			},
			wantState: "not_loaded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shieldEnv(t)
			socket := startMockDaemon(t, func(req jsonrpcRequest) jsonrpcResponse {
				switch req.Method {
				case "daimon.identity.get":
					result, _ := json.Marshal(map[string]string{"did": "did:key:zTest"})
					return jsonrpcResponse{JSONRPC: "2.0", Result: result, ID: json.RawMessage("1")}
				case "daimon.wallet.list":
					return tc.walletResp(req)
				}
				return jsonrpcResponse{
					JSONRPC: "2.0",
					// JSON-RPC standard "method not found" code; the CLI
					// doesn't import this constant since it doesn't need
					// to branch on it, but the mock daemon does need to
					// emit something realistic for unmatched methods.
					Error: &jsonrpcError{Code: -32601, Message: "method not found"},
					ID:    json.RawMessage("1"),
				}
			})
			cfg := newTestDoctorConfig(t, t.TempDir(),
				"http://127.0.0.1:1", "http://127.0.0.1:1", socket)
			rep := gatherDoctorReport(context.Background(), cfg)

			if rep.Daemon.State != "unlocked" {
				t.Fatalf("precondition: Daemon.State = %q, want unlocked (dial_error=%q)",
					rep.Daemon.State, rep.Daemon.DialError)
			}
			if rep.Wallet.State != tc.wantState {
				t.Errorf("Wallet.State: got %q want %q (rpc_error=%q)",
					rep.Wallet.State, tc.wantState, rep.Wallet.RPCError)
			}
			if rep.Wallet.WalletCount != tc.wantCount {
				t.Errorf("Wallet.WalletCount: got %d want %d", rep.Wallet.WalletCount, tc.wantCount)
			}
			if tc.wantHasChains && len(rep.Wallet.Chains) == 0 {
				t.Errorf("expected Chains populated; got empty")
			}
		})
	}
}

func TestDoctor_WalletNotProbedWhenDaemonLocked(t *testing.T) {
	// When the daemon is locked, the wallet probe is skipped — locked
	// means no wallet RPCs could possibly succeed anyway, and probing
	// would just produce noise.
	shieldEnv(t)
	socket := startMockDaemon(t, func(req jsonrpcRequest) jsonrpcResponse {
		return jsonrpcResponse{
			JSONRPC: "2.0",
			Error:   &jsonrpcError{Code: codeIdentityLocked, Message: "locked"},
			ID:      json.RawMessage("1"),
		}
	})
	cfg := newTestDoctorConfig(t, t.TempDir(),
		"http://127.0.0.1:1", "http://127.0.0.1:1", socket)
	rep := gatherDoctorReport(context.Background(), cfg)

	if rep.Daemon.State != "locked" {
		t.Fatalf("precondition: Daemon.State = %q, want locked", rep.Daemon.State)
	}
	if rep.Wallet.State != "not_probed" {
		t.Errorf("Wallet.State: got %q want %q", rep.Wallet.State, "not_probed")
	}
}

func TestDoctor_RuntimesProbeParsesModels(t *testing.T) {
	shieldEnv(t)
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"},{"name":"phi3:latest"}]}`))
	}))
	defer ollamaSrv.Close()
	lmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"llama-3.2-1b-instruct"}]}`))
	}))
	defer lmSrv.Close()

	cfg := newTestDoctorConfig(t, t.TempDir(),
		ollamaSrv.URL, lmSrv.URL,
		shortAbsentSocket(t),
	)
	rep := gatherDoctorReport(context.Background(), cfg)

	if !rep.Runtimes.Ollama.Reachable {
		t.Fatalf("Ollama unreachable: %+v", rep.Runtimes.Ollama)
	}
	if got, want := rep.Runtimes.Ollama.Models, []string{"llama3.2:latest", "phi3:latest"}; !equalSlices(got, want) {
		t.Errorf("Ollama.Models: got %v want %v", got, want)
	}
	if !rep.Runtimes.LMStudio.Reachable {
		t.Fatalf("LM Studio unreachable: %+v", rep.Runtimes.LMStudio)
	}
	if got, want := rep.Runtimes.LMStudio.Models, []string{"llama-3.2-1b-instruct"}; !equalSlices(got, want) {
		t.Errorf("LMStudio.Models: got %v want %v", got, want)
	}
}

func TestDoctor_APIKeyEnvVarPresenceFlips(t *testing.T) {
	shieldEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-redacted")
	t.Setenv("OPENAI_API_KEY", "sk-openai-test-redacted")

	cfg := newTestDoctorConfig(t, t.TempDir(),
		"http://127.0.0.1:1", "http://127.0.0.1:1",
		shortAbsentSocket(t),
	)
	rep := gatherDoctorReport(context.Background(), cfg)
	if !rep.Env.AnthropicAPIKey {
		t.Error("AnthropicAPIKey should be true when env is set")
	}
	if !rep.Env.OpenAIAPIKey {
		t.Error("OpenAIAPIKey should be true when env is set")
	}
	if rep.Env.LMStudioAPIKey {
		t.Error("LMStudioAPIKey should be false (not set this case)")
	}

	// Doctor must NOT echo the value anywhere — render and grep.
	var buf bytes.Buffer
	if err := renderDoctorText(&buf, rep); err != nil {
		t.Fatalf("renderDoctorText: %v", err)
	}
	out := buf.String()
	for _, leak := range []string{"sk-ant-test-redacted", "sk-openai-test-redacted"} {
		if strings.Contains(out, leak) {
			t.Errorf("rendered output leaked API key: %q present", leak)
		}
	}
	if !strings.Contains(out, "ANTHROPIC_API_KEY") || !strings.Contains(out, "set") {
		t.Errorf("rendered output missing presence indicator; got:\n%s", out)
	}
}

func TestDoctor_TextRenderIsMonotonicAndIncludesHomeKeystoreDaemonRuntimes(t *testing.T) {
	shieldEnv(t)
	rep := doctorReport{
		Build:  doctorBuild{Version: "v0.1.0-test", Go: "go1.23", OS: "darwin", Arch: "arm64"},
		Home:   doctorHome{Resolved: "/tmp/home", Socket: "/tmp/home/daimon.sock", Keystore: fileStat{Present: true, Size: 124, Mode: "-rw-------"}},
		Daemon: doctorDaemon{State: "unlocked", DID: "did:key:zMock"},
		Env:    doctorEnv{AnthropicAPIKey: true},
		Runtimes: doctorRuntimes{
			Ollama:   runtimeStat{Endpoint: "http://localhost:11434", Reachable: true, Models: []string{"a"}},
			LMStudio: runtimeStat{Endpoint: "http://localhost:1234", Reachable: false, Error: "connection refused"},
		},
	}
	var buf bytes.Buffer
	if err := renderDoctorText(&buf, rep); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"daimon doctor",
		"v0.1.0-test",
		"DAIMON_HOME",
		"/tmp/home",
		"present (124 B",
		"running, unlocked",
		"did:key:zMock",
		"ANTHROPIC_API_KEY",
		"Ollama",
		"LM Studio",
		"connection refused",
		"Live round-trip readiness",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q; full output:\n%s", want, out)
		}
	}
}

func TestDoctor_JSONOutputIsValidAndStructured(t *testing.T) {
	shieldEnv(t)
	cfg := newTestDoctorConfig(t, t.TempDir(),
		"http://127.0.0.1:1", "http://127.0.0.1:1",
		shortAbsentSocket(t),
	)
	rep := gatherDoctorReport(context.Background(), cfg)
	out, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, k := range []string{"build", "home", "daemon", "env", "runtimes"} {
		if _, ok := back[k]; !ok {
			t.Errorf("JSON envelope missing top-level key %q; got keys %v", k, mapKeys(back))
		}
	}
}

func TestDoctor_HumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KiB"},
		{4096, "4.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d): got %q want %q", c.n, got, c.want)
		}
	}
}

// --- helpers ----------------------------------------------------------------

func startMockDaemon(t *testing.T, respond func(req jsonrpcRequest) jsonrpcResponse) string {
	t.Helper()
	// AF_UNIX sun_path on macOS is capped at 104 bytes; t.TempDir() on darwin
	// produces ~110-byte paths once you add a sock filename. Use the same
	// short-prefix MkdirTemp("","dmn") trick the internal/server tests use.
	dir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("Listen unix %s (len=%d): %v", socket, len(socket), err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
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
				_ = json.NewEncoder(c).Encode(respond(req))
			}(c)
		}
	}()
	return socket
}

func mustWriteFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
