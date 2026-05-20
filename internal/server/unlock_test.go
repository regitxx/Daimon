package server

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/addressbook"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/wallet"
)

// --- locked-mode harness -----------------------------------------------------

// lockedFixture is the serve-mode counterpart to newFixture. It builds a
// server with an Unlock callback and starts in the locked state. The callback
// records how many times it was invoked so tests can assert deduplication.
type lockedFixture struct {
	t       *testing.T
	srv     *Server
	addr    string
	unlocks struct {
		sync.Mutex
		n            int
		err          error
		id           *identity.Identity
		store        *memory.Store
		alog         *activity.Log
		gotPassword  string
	}
}

// newLockedFixture: builds the trio up-front (so the test can pre-create an
// identity and then assert that unlock returns the same DID), but does NOT
// pass them to server.New — instead it wraps them in an Unlock callback so the
// server starts locked and the trio only becomes visible after a successful
// daimon.identity.unlock.
func newLockedFixture(t *testing.T, password string) *lockedFixture {
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

	f := &lockedFixture{t: t}
	f.unlocks.id = id
	f.unlocks.store = store
	f.unlocks.alog = alog

	unlock := func(_ context.Context, pw string) (*identity.Identity, *memory.Store, *activity.Log, *wallet.Store, *wallet.Mnemonic, *addressbook.Book, error) {
		f.unlocks.Lock()
		defer f.unlocks.Unlock()
		f.unlocks.n++
		f.unlocks.gotPassword = pw
		if pw != password {
			return nil, nil, nil, nil, nil, nil, identity.ErrWrongPassword
		}
		if f.unlocks.err != nil {
			return nil, nil, nil, nil, nil, nil, f.unlocks.err
		}
		return f.unlocks.id, f.unlocks.store, f.unlocks.alog, nil, nil, nil, nil
	}

	srv, err := New(Options{Unlock: unlock})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	f.srv = srv

	sockDir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "s.sock")
	if err := srv.Listen(sockPath); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	f.addr = sockPath

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() { _ = srv.Serve(ctx) }()
	waitForSocket(t, sockPath)
	return f
}

func (f *lockedFixture) call(t *testing.T, method string, params any) *Response {
	t.Helper()
	c, err := net.Dial("unix", f.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	return doCall(t, c, method, params, "1")
}

// --- construction ------------------------------------------------------------

func TestNew_DemoModeRequiresAllThree(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"missing identity", Options{}},
		{"missing store", Options{Identity: mustID(t)}},
		{"missing log", Options{Identity: mustID(t), Store: mustStore(t, mustID(t))}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.opts); err == nil {
				t.Fatal("expected error from incomplete demo-mode Options")
			}
		})
	}
}

func TestNew_ServeModeAllowsMissingTrio(t *testing.T) {
	srv, err := New(Options{
		Unlock: func(context.Context, string) (*identity.Identity, *memory.Store, *activity.Log, *wallet.Store, *wallet.Mnemonic, *addressbook.Book, error) {
			return nil, nil, nil, nil, nil, nil, errors.New("never called")
		},
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	if srv.IsUnlocked() {
		t.Fatal("server constructed in serve mode must start locked")
	}
}

func TestNew_DemoModeStartsUnlocked(t *testing.T) {
	id := mustID(t)
	srv, err := New(Options{
		Identity: id,
		Store:    mustStore(t, id),
		Log:      mustLog(t, id),
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	if !srv.IsUnlocked() {
		t.Fatal("server constructed in demo mode must start unlocked")
	}
}

// --- locked-state gate -------------------------------------------------------

func TestLocked_RejectsAllExceptUnlock(t *testing.T) {
	f := newLockedFixture(t, "correct-horse")

	// Spot-check three diverse methods plus a hand-picked notification path.
	for _, method := range []string{
		"daimon.identity.get",
		"daimon.memory.write",
		"daimon.provider.list",
	} {
		t.Run(method, func(t *testing.T) {
			resp := f.call(t, method, map[string]any{"kind": "fact", "content": "x"})
			if resp.Error == nil {
				t.Fatalf("expected error, got result %v", resp.Result)
			}
			if resp.Error.Code != CodeIdentityLocked {
				t.Errorf("code: got %d, want %d", resp.Error.Code, CodeIdentityLocked)
			}
		})
	}
}

func TestLocked_UnlockRequiresPassword(t *testing.T) {
	f := newLockedFixture(t, "correct-horse")
	resp := f.call(t, "daimon.identity.unlock", map[string]any{})
	if resp.Error == nil {
		t.Fatalf("expected error, got result %v", resp.Result)
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d, want %d (invalid params)", resp.Error.Code, CodeInvalidParams)
	}
}

func TestUnlock_WrongPasswordKeepsLocked(t *testing.T) {
	f := newLockedFixture(t, "correct-horse")
	resp := f.call(t, "daimon.identity.unlock", map[string]any{"password": "battery-staple"})
	if resp.Error == nil {
		t.Fatal("expected error from wrong password")
	}
	if resp.Error.Code != CodeIdentityLocked {
		t.Errorf("code: got %d, want %d", resp.Error.Code, CodeIdentityLocked)
	}
	if f.srv.IsUnlocked() {
		t.Fatal("wrong password must not unlock the server")
	}

	// Subsequent calls still rejected.
	resp = f.call(t, "daimon.identity.get", nil)
	if resp.Error == nil || resp.Error.Code != CodeIdentityLocked {
		t.Fatalf("identity.get post-failed-unlock: expected CodeIdentityLocked, got %+v", resp.Error)
	}
}

func TestUnlock_SuccessTransitionsAndPermits(t *testing.T) {
	f := newLockedFixture(t, "correct-horse")
	resp := f.call(t, "daimon.identity.unlock", map[string]any{"password": "correct-horse"})
	if resp.Error != nil {
		t.Fatalf("unlock failed: %+v", resp.Error)
	}
	var got identityUnlockResult
	resultAs(t, resp, &got)
	if got.DID == "" {
		t.Error("unlock result.did should be populated")
	}
	if !f.srv.IsUnlocked() {
		t.Fatal("server should be unlocked after successful unlock")
	}

	// The post-unlock smoke test: identity.get now succeeds.
	resp = f.call(t, "daimon.identity.get", nil)
	if resp.Error != nil {
		t.Fatalf("identity.get post-unlock: %+v", resp.Error)
	}
	var idResp identityGetResult
	resultAs(t, resp, &idResp)
	if idResp.DID != got.DID {
		t.Errorf("identity.get DID does not match unlock DID: %q vs %q", idResp.DID, got.DID)
	}
}

func TestUnlock_IdempotentAfterSuccess(t *testing.T) {
	f := newLockedFixture(t, "correct-horse")
	r1 := f.call(t, "daimon.identity.unlock", map[string]any{"password": "correct-horse"})
	if r1.Error != nil {
		t.Fatalf("first unlock: %+v", r1.Error)
	}
	// Calling unlock again on an already-unlocked server should not error and
	// should not invoke the keystore-loading callback again.
	r2 := f.call(t, "daimon.identity.unlock", map[string]any{"password": "anything"})
	if r2.Error != nil {
		t.Fatalf("second unlock: %+v", r2.Error)
	}
	f.unlocks.Lock()
	defer f.unlocks.Unlock()
	if f.unlocks.n != 1 {
		t.Errorf("unlock callback should run exactly once across two unlock RPCs; got %d", f.unlocks.n)
	}
}

func TestUnlock_DemoModeServerRejects(t *testing.T) {
	// A server constructed without an Unlock callback already has its trio;
	// calling unlock against it is a client error (CodeInvalidRequest).
	f := newFixture(t)
	resp := f.call(t, "daimon.identity.unlock", map[string]any{"password": "anything"})
	if resp.Error == nil {
		t.Fatal("expected error from unlock against demo-mode server")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("code: got %d, want %d", resp.Error.Code, CodeInvalidRequest)
	}
}

// --- helpers (locked-mode flavour) -------------------------------------------

func mustID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	return id
}

func mustStore(t *testing.T, id *identity.Identity) *memory.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.Open(filepath.Join(dir, "memory.db"), id, memory.NullEmbedder{})
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustLog(t *testing.T, id *identity.Identity) *activity.Log {
	t.Helper()
	dir := t.TempDir()
	alog, err := activity.Open(filepath.Join(dir, "activity.log"), id)
	if err != nil {
		t.Fatalf("activity.Open: %v", err)
	}
	t.Cleanup(func() { _ = alog.Close() })
	return alog
}
