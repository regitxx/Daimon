package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
)

// --- harness -----------------------------------------------------------------

// fixture builds a fresh identity + memory store + activity log + server
// listening on a temp Unix socket. The server runs in a background goroutine
// for the lifetime of the test.
type fixture struct {
	t    *testing.T
	id   *identity.Identity
	mem  *memory.Store
	alog *activity.Log
	srv  *Server
	addr string
}

func newFixture(t *testing.T) *fixture {
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

	srv, err := New(Options{Identity: id, Store: store, Log: alog})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	// Unix socket paths are capped at ~104 bytes on macOS (sun_path). Go's
	// t.TempDir() embeds the full test name and easily blows that budget,
	// so the socket lives in its own short directory while the heavier
	// per-test files stay under t.TempDir().
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

	// Wait briefly for the listener to be ready.
	waitForSocket(t, sockPath)

	return &fixture{t: t, id: id, mem: store, alog: alog, srv: srv, addr: sockPath}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("unix", path)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("socket %s never became ready", path)
}

// rpcClient is a minimal JSON-RPC 2.0 client for tests. It opens one
// connection per invocation; tests that need a long-lived connection use
// dialPersistent below.
func (f *fixture) call(t *testing.T, method string, params any) *Response {
	t.Helper()
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	return doCall(t, c, method, params, "1")
}

// dialPersistent returns a connection that the test will reuse for multiple
// requests. The caller is responsible for closing it.
func (f *fixture) dialPersistent(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func doCall(t *testing.T, c net.Conn, method string, params any, id string) *Response {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      id,
	}
	if params != nil {
		req["params"] = params
	}
	if err := json.NewEncoder(c).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &resp
}

// resultAs unmarshals resp.Result into v. Tests use this to type-narrow the
// any-typed result field.
func resultAs(t *testing.T, resp *Response, v any) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("rpc error: %+v", resp.Error)
	}
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal result: %v (raw=%s)", err, b)
	}
}

// --- lifecycle ---------------------------------------------------------------

func TestSocketModeIs0600(t *testing.T) {
	f := newFixture(t)
	info, err := os.Stat(f.addr)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode: got %o want 0600", info.Mode().Perm())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	f := newFixture(t)
	if err := f.srv.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := f.srv.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := os.Stat(f.addr); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket file should be removed after Close, got err=%v", err)
	}
}

func TestNewRequiresAllPrimitives(t *testing.T) {
	id, _ := identity.Generate()
	dir := t.TempDir()
	store, _ := memory.Open(filepath.Join(dir, "m.db"), id, memory.NullEmbedder{})
	defer store.Close()
	alog, _ := activity.Open(filepath.Join(dir, "a.log"), id)
	defer alog.Close()

	cases := []Options{
		{Store: store, Log: alog},
		{Identity: id, Log: alog},
		{Identity: id, Store: store},
	}
	for i, opts := range cases {
		if _, err := New(opts); err == nil {
			t.Errorf("case %d: New with missing field should error", i)
		}
	}
}

// --- per-method roundtrips ---------------------------------------------------

func TestIdentityGet(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.identity.get", nil)
	var out identityGetResult
	resultAs(t, resp, &out)

	if out.DID != f.id.DID() {
		t.Errorf("DID: got %q want %q", out.DID, f.id.DID())
	}
	if !strings.HasPrefix(out.DID, "did:key:") {
		t.Errorf("DID prefix: got %q", out.DID)
	}
	if out.PublicKey == "" {
		t.Error("public_key is empty")
	}
	if len(out.DIDMethods) == 0 || out.DIDMethods[0] != "did:key" {
		t.Errorf("did_methods: got %v", out.DIDMethods)
	}
}

func TestMemoryWriteReadRoundtrip(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.memory.write", map[string]any{
		"kind":     "fact",
		"content":  "Daimon is sovereign",
		"metadata": map[string]any{"src": "test"},
		"source":   "unit-test",
	})
	var w memoryWriteResult
	resultAs(t, resp, &w)
	if w.ID == "" {
		t.Fatal("write returned empty id")
	}

	resp = f.call(t, "daimon.memory.read", map[string]any{"id": w.ID})
	var got memory.Memory
	resultAs(t, resp, &got)
	if got.ID != w.ID {
		t.Errorf("id: got %q want %q", got.ID, w.ID)
	}
	if got.Content != "Daimon is sovereign" {
		t.Errorf("content: got %q", got.Content)
	}
	if got.Kind != memory.KindFact {
		t.Errorf("kind: got %q", got.Kind)
	}
	if len(got.Signature) == 0 {
		t.Error("signature missing on read")
	}

	// Write should also have produced an activity entry.
	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{Kind: activity.KindMemoryWrite})
	if err != nil {
		t.Fatalf("activity query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 memory.write activity entry, got %d", len(entries))
	}
}

func TestMemoryReadMissingReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.memory.read", map[string]any{"id": "01HXNOTREAL00000000000000"})
	if resp.Error == nil {
		t.Fatal("expected error for missing id")
	}
	if resp.Error.Code != CodeNotFound {
		t.Errorf("code: got %d want %d", resp.Error.Code, CodeNotFound)
	}
}

func TestMemoryWriteRejectsInvalidKind(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.memory.write", map[string]any{
		"kind":    "novel-kind",
		"content": "hello",
	})
	if resp.Error == nil {
		t.Fatal("expected error for invalid kind")
	}
	if resp.Error.Code != CodeInvalidKind {
		t.Errorf("code: got %d want %d", resp.Error.Code, CodeInvalidKind)
	}
}

func TestMemorySearchSubstring(t *testing.T) {
	f := newFixture(t)
	for _, content := range []string{
		"alpha beta gamma",
		"beta is the second",
		"unrelated text",
	} {
		f.call(t, "daimon.memory.write", map[string]any{
			"kind":    "fact",
			"content": content,
		})
	}
	resp := f.call(t, "daimon.memory.search", map[string]any{"query": "beta", "limit": 5})
	var results []scoredMemory
	resultAs(t, resp, &results)
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'beta', got %d", len(results))
	}
	for _, r := range results {
		if !strings.Contains(strings.ToLower(r.Content), "beta") {
			t.Errorf("result missing 'beta': %q", r.Content)
		}
	}
}

func TestMemoryDelete(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.memory.write", map[string]any{
		"kind": "fact", "content": "ephemeral",
	})
	var w memoryWriteResult
	resultAs(t, resp, &w)

	resp = f.call(t, "daimon.memory.delete", map[string]any{"id": w.ID})
	var d memoryDeleteResult
	resultAs(t, resp, &d)
	if !d.Deleted {
		t.Error("delete returned deleted=false on existing id")
	}

	// Idempotent: second delete is well-formed and returns deleted=false.
	resp = f.call(t, "daimon.memory.delete", map[string]any{"id": w.ID})
	resultAs(t, resp, &d)
	if d.Deleted {
		t.Error("second delete should return deleted=false")
	}
}

func TestMemoryExportImportRoundtrip(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		f.call(t, "daimon.memory.write", map[string]any{
			"kind":    "observation",
			"content": fmt.Sprintf("entry %d", i),
		})
	}
	resp := f.call(t, "daimon.memory.export", nil)
	var doc memory.ExportDocument
	resultAs(t, resp, &doc)
	if len(doc.Memories) != 3 {
		t.Fatalf("expected 3 memories in export, got %d", len(doc.Memories))
	}
	if len(doc.Signature) == 0 {
		t.Fatal("export document missing signature")
	}

	// Stand up a second fixture as a fresh principal and import the document.
	g := newFixture(t)
	resp = g.call(t, "daimon.memory.import", map[string]any{
		"document":         doc,
		"verify_signature": true,
	})
	var ir memoryImportResult
	resultAs(t, resp, &ir)
	if ir.Imported != 3 || ir.Skipped != 0 {
		t.Errorf("imported=%d skipped=%d, want 3/0", ir.Imported, ir.Skipped)
	}

	// Re-import should be idempotent (skip duplicates).
	resp = g.call(t, "daimon.memory.import", map[string]any{
		"document":         doc,
		"verify_signature": true,
	})
	resultAs(t, resp, &ir)
	if ir.Imported != 0 || ir.Skipped != 3 {
		t.Errorf("re-import imported=%d skipped=%d, want 0/3", ir.Imported, ir.Skipped)
	}
}

func TestContextGetReturnsRankedMemoriesUnderBudget(t *testing.T) {
	f := newFixture(t)
	for _, c := range []string{
		"alpha is the first",
		"beta is the second",
		"alpha and beta together",
		"gamma sits alone",
	} {
		f.call(t, "daimon.memory.write", map[string]any{"kind": "fact", "content": c})
	}
	resp := f.call(t, "daimon.context.get", map[string]any{"query": "alpha", "max_tokens": 200})
	var got contextGetResult
	resultAs(t, resp, &got)
	if got.Context == "" {
		t.Fatal("context is empty")
	}
	if len(got.MemoryIDs) == 0 {
		t.Fatal("memory_ids empty")
	}
	if got.TokenEstimate <= 0 || got.TokenEstimate > 200 {
		t.Errorf("token_estimate %d outside (0, 200]", got.TokenEstimate)
	}
	if !strings.Contains(got.Context, "alpha") {
		t.Errorf("context should reference alpha: %q", got.Context)
	}
}

func TestContextGetKindsFilter(t *testing.T) {
	f := newFixture(t)
	for _, m := range []map[string]any{
		{"kind": "fact", "content": "fact about alpha"},
		{"kind": "preference", "content": "prefer alpha"},
		{"kind": "task", "content": "ship alpha"},
	} {
		f.call(t, "daimon.memory.write", m)
	}
	resp := f.call(t, "daimon.context.get", map[string]any{
		"query": "alpha",
		"kinds": []string{"preference"},
	})
	var got contextGetResult
	resultAs(t, resp, &got)
	if len(got.MemoryIDs) != 1 {
		t.Fatalf("kinds filter should leave 1 result, got %d (context=%q)",
			len(got.MemoryIDs), got.Context)
	}
	if !strings.Contains(got.Context, "(preference)") {
		t.Errorf("context should label preference: %q", got.Context)
	}
}

// TestContextGet_AppendsContextPreviewed proves the SPEC §8.2 audit symmetry
// added in session 30: a successful standalone daimon.context.get appends a
// context.previewed entry whose payload carries {query, matched} — mirroring
// the self-audit shape of activity.queried (matched=N).
func TestContextGet_AppendsContextPreviewed(t *testing.T) {
	f := newFixture(t)
	for _, c := range []string{
		"alpha is the first",
		"alpha and beta together",
		"gamma sits alone",
	} {
		f.call(t, "daimon.memory.write", map[string]any{"kind": "fact", "content": c})
	}
	resp := f.call(t, "daimon.context.get", map[string]any{"query": "alpha"})
	if resp.Error != nil {
		t.Fatalf("context.get error: %+v", resp.Error)
	}
	var got contextGetResult
	resultAs(t, resp, &got)

	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{
		Kind: activity.KindContextPreviewed,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 context.previewed entry, got %d", len(entries))
	}
	var pl map[string]any
	if err := json.Unmarshal(entries[0].Payload, &pl); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if pl["query"] != "alpha" {
		t.Errorf("payload.query: got %v want %q", pl["query"], "alpha")
	}
	if v, _ := pl["matched"].(float64); int(v) != len(got.MemoryIDs) {
		t.Errorf("payload.matched: got %v want %d", pl["matched"], len(got.MemoryIDs))
	}
}

// TestContextGet_EmptyMatchStillAppends guards the failure-vs-empty-match
// distinction: a query that matches no memories is not an error — the daimon
// honestly answers "no relevant context" — and the audit row still appends
// with matched=0. Only an actual retrieval error (search-layer failure) skips
// the append, by the same success-only-append rule activity.verified uses.
func TestContextGet_EmptyMatchStillAppends(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.context.get", map[string]any{"query": "nothingever"})
	if resp.Error != nil {
		t.Fatalf("context.get error: %+v", resp.Error)
	}
	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{
		Kind: activity.KindContextPreviewed,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("empty-match should still append; got %d entries", len(entries))
	}
	var pl map[string]any
	if err := json.Unmarshal(entries[0].Payload, &pl); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if v, _ := pl["matched"].(float64); int(v) != 0 {
		t.Errorf("payload.matched on empty match: got %v want 0", pl["matched"])
	}
	if pl["query"] != "nothingever" {
		t.Errorf("payload.query: got %v want %q", pl["query"], "nothingever")
	}
}

// TestContextGet_ErrorPathDoesNotAppend mirrors session 28's verify-fails-no-
// extend rule for the new context.previewed audit row: when the underlying
// retrieval errors, the daimon does NOT extend the audit chain. Closing the
// store mid-test makes search return a "database is closed" error which
// surfaces through mapMemoryError as CodeInternalError; the success path's
// alog.Append never runs.
func TestContextGet_ErrorPathDoesNotAppend(t *testing.T) {
	f := newFixture(t)
	// Close the memory store so the next search call errors. The activity log
	// is left open so we can query it post-failure.
	if err := f.mem.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	resp := f.call(t, "daimon.context.get", map[string]any{"query": "anything"})
	if resp.Error == nil {
		t.Fatal("expected context.get to error after store close")
	}
	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{
		Kind: activity.KindContextPreviewed,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("error path must not append context.previewed; got %d entries", len(entries))
	}
}

func TestActivityAppendAndQuery(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.activity.append", map[string]any{
		"kind":    "memory.write",
		"payload": map[string]any{"id": "mem-1"},
	})
	var a activityAppendResult
	resultAs(t, resp, &a)
	if a.ID == "" || !strings.HasPrefix(a.Hash, "blake3:") {
		t.Fatalf("append result malformed: %+v", a)
	}

	resp = f.call(t, "daimon.activity.query", map[string]any{"kind": "memory.write"})
	var entries []activity.Entry
	resultAs(t, resp, &entries)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != a.ID {
		t.Errorf("query returned wrong entry: got %q want %q", entries[0].ID, a.ID)
	}

	// query.queried should now appear in the log under its own kind.
	resp = f.call(t, "daimon.activity.query", map[string]any{"kind": "activity.queried"})
	resultAs(t, resp, &entries)
	if len(entries) == 0 {
		t.Error("activity.queried should be auto-logged after a query")
	}
}

func TestActivityAppendRejectsEmptyKind(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.activity.append", map[string]any{"payload": map[string]any{}})
	if resp.Error == nil {
		t.Fatal("expected error for empty kind")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestActivityVerify_HappyPathReturnsCountAndOK(t *testing.T) {
	f := newFixture(t)
	// Seed three entries via the public RPC surface so the writes land through
	// the same path daimond uses in production.
	for i := 0; i < 3; i++ {
		resp := f.call(t, "daimon.activity.append", map[string]any{
			"kind":    "memory.write",
			"payload": map[string]any{"id": fmt.Sprintf("mem-%d", i)},
		})
		if resp.Error != nil {
			t.Fatalf("seed append %d: %+v", i, resp.Error)
		}
	}
	resp := f.call(t, "daimon.activity.verify", nil)
	var got activityVerifyResult
	resultAs(t, resp, &got)
	if !got.OK {
		t.Errorf("ok: got false want true")
	}
	if got.Verified != 3 {
		t.Errorf("verified: got %d want 3", got.Verified)
	}

	// Post-append symmetry: a successful verify writes its own
	// activity.verified entry, so a follow-up query for that kind returns one
	// row whose payload says verified=3 (the count from the verify above).
	resp = f.call(t, "daimon.activity.query", map[string]any{"kind": "activity.verified"})
	var verified []activity.Entry
	resultAs(t, resp, &verified)
	if len(verified) != 1 {
		t.Fatalf("expected 1 activity.verified entry; got %d", len(verified))
	}
	var pl map[string]any
	if err := json.Unmarshal(verified[0].Payload, &pl); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if v, _ := pl["verified"].(float64); int(v) != 3 {
		t.Errorf("payload verified: got %v want 3", pl["verified"])
	}
}

func TestActivityVerify_TamperedChainReturnsError(t *testing.T) {
	f := newFixture(t)
	// Seed two entries through the RPC surface.
	for i := 0; i < 2; i++ {
		resp := f.call(t, "daimon.activity.append", map[string]any{
			"kind":    "memory.write",
			"payload": map[string]any{"id": fmt.Sprintf("mem-%d", i)},
		})
		if resp.Error != nil {
			t.Fatalf("seed append %d: %+v", i, resp.Error)
		}
	}

	// Close the server's log handle and tamper with entry #1's on-disk
	// payload — anything other than the original ciphertext fails AEAD
	// authentication on the next Verify.
	logPath := f.alog.Path()
	if err := f.alog.Close(); err != nil {
		t.Fatalf("close alog: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines; got %d", len(lines))
	}
	var e activity.Entry
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatalf("unmarshal entry #1: %v", err)
	}
	// Replace the encrypted payload with a JSON-shaped value the AEAD layer
	// will reject before the chain check runs (mirrors
	// TestVerifyDetectsTamperedPayload in internal/activity).
	e.Payload = json.RawMessage(`{"i":99}`)
	tampered, err := json.Marshal(&e)
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}
	lines[1] = string(tampered)
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write tampered log: %v", err)
	}

	// Reopen under the same identity so the server can reach the file.
	reopened, err := activity.Open(logPath, f.id)
	if err != nil {
		t.Fatalf("reopen alog: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	f.srv.alog = reopened

	resp := f.call(t, "daimon.activity.verify", nil)
	if resp.Error == nil {
		t.Fatal("expected verify to fail on tampered chain")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want %d", resp.Error.Code, CodeInternalError)
	}
	// Failure path MUST NOT extend a corrupt chain: no activity.verified
	// entry should appear after the failed verify.
	resp2 := f.call(t, "daimon.activity.query", map[string]any{"kind": "activity.verified"})
	var verified []activity.Entry
	resultAs(t, resp2, &verified)
	if len(verified) != 0 {
		t.Errorf("expected NO activity.verified entries after failed verify; got %d", len(verified))
	}
}

func TestActivityVerify_EmptyLogIsOK(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.activity.verify", nil)
	var got activityVerifyResult
	resultAs(t, resp, &got)
	if !got.OK {
		t.Errorf("empty log should verify ok")
	}
	if got.Verified != 0 {
		t.Errorf("verified: got %d want 0", got.Verified)
	}
	// The verify itself appended an activity.verified entry; subsequent verify
	// should now report 1.
	resp = f.call(t, "daimon.activity.verify", nil)
	resultAs(t, resp, &got)
	if got.Verified != 1 {
		t.Errorf("second verify count: got %d want 1", got.Verified)
	}
}

// --- protocol framing edge cases --------------------------------------------

func TestParseErrorClosesConnection(t *testing.T) {
	f := newFixture(t)
	c := f.dialPersistent(t)
	defer c.Close()

	if _, err := io.WriteString(c, "{not valid json"); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Fatalf("expected parse error, got %+v", resp)
	}
	if string(resp.ID) != "null" {
		t.Errorf("parse-error id: got %s want null", resp.ID)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.does.not.exist", nil)
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("code: got %d want %d", resp.Error.Code, CodeMethodNotFound)
	}
}

// Provider methods are registered unconditionally now that the adapter
// primitive has landed; their behaviour without a registry attached is
// covered by TestProviderList_Empty and TestProviderInvoke_NoRegistry in
// provider_handlers_test.go.

func TestInvalidJSONRPCVersionRejected(t *testing.T) {
	f := newFixture(t)
	c := f.dialPersistent(t)
	defer c.Close()

	req := map[string]any{
		"jsonrpc": "1.0",
		"method":  "daimon.identity.get",
		"id":      "x",
	}
	_ = json.NewEncoder(c).Encode(req)
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidRequest {
		t.Fatalf("expected invalid-request, got %+v", resp)
	}
	if string(resp.ID) != `"x"` {
		t.Errorf("id: got %s want \"x\"", resp.ID)
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	f := newFixture(t)
	c := f.dialPersistent(t)
	defer c.Close()

	// Notification: no "id" field. Server must not reply.
	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  "daimon.identity.get",
	}
	_ = json.NewEncoder(c).Encode(notif)

	// Send a real request after; if the server had spuriously responded to
	// the notification we would decode that here instead of the real reply.
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  "daimon.identity.get",
		"id":      "after-notif",
	}
	_ = json.NewEncoder(c).Encode(req)

	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(resp.ID) != `"after-notif"` {
		t.Errorf("response was for the notification, not the follow-up: id=%s", resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("follow-up errored: %+v", resp.Error)
	}
}

func TestPersistentConnectionMultipleRequests(t *testing.T) {
	f := newFixture(t)
	c := f.dialPersistent(t)
	defer c.Close()

	enc := json.NewEncoder(c)
	dec := json.NewDecoder(c)
	for i := 0; i < 5; i++ {
		req := map[string]any{
			"jsonrpc": "2.0",
			"method":  "daimon.identity.get",
			"id":      fmt.Sprintf("req-%d", i),
		}
		if err := enc.Encode(req); err != nil {
			t.Fatalf("encode %d: %v", i, err)
		}
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		want := fmt.Sprintf(`"req-%d"`, i)
		if string(resp.ID) != want {
			t.Errorf("req %d: id mismatch got %s want %s", i, resp.ID, want)
		}
		if resp.Error != nil {
			t.Errorf("req %d: error %+v", i, resp.Error)
		}
	}
}

// --- concurrency -------------------------------------------------------------

func TestConcurrentClients(t *testing.T) {
	f := newFixture(t)
	const clients = 8
	const writesPerClient = 10
	var wg sync.WaitGroup
	wg.Add(clients)
	for ci := 0; ci < clients; ci++ {
		go func(ci int) {
			defer wg.Done()
			for wi := 0; wi < writesPerClient; wi++ {
				resp := f.call(t, "daimon.memory.write", map[string]any{
					"kind":    "observation",
					"content": fmt.Sprintf("client=%d write=%d", ci, wi),
				})
				if resp.Error != nil {
					t.Errorf("client %d write %d: %+v", ci, wi, resp.Error)
					return
				}
			}
		}(ci)
	}
	wg.Wait()

	// All writes landed.
	resp := f.call(t, "daimon.memory.search", map[string]any{
		"query": "client=", "limit": 1000,
	})
	var results []scoredMemory
	resultAs(t, resp, &results)
	if len(results) != clients*writesPerClient {
		t.Errorf("expected %d memories, got %d", clients*writesPerClient, len(results))
	}

	// Activity log chain should still verify under concurrent appends.
	if n, err := f.alog.Verify(context.Background()); err != nil {
		t.Errorf("activity.Verify failed: %v (verified %d entries)", err, n)
	}
}
