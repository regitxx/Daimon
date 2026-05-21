package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/addressbook"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/wallet"
)

// peerFixture wraps a standard fixture and also starts a TCP peer listener.
type peerFixture struct {
	*fixture
	peerAddr string // bound TCP address, e.g. "127.0.0.1:49152"
}

func newPeerFixture(t *testing.T) *peerFixture {
	t.Helper()
	f := newFixture(t)
	addr, err := f.srv.PeerListen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("PeerListen: %v", err)
	}
	return &peerFixture{fixture: f, peerAddr: addr}
}

// --- helpers -----------------------------------------------------------------

// callPeerDial issues a daimon.peer.dial RPC through f's Unix socket.
func (f *peerFixture) callDial(t *testing.T, did, endpoint string) peerDialResult {
	t.Helper()
	resp := f.call(t, "daimon.peer.dial", map[string]any{
		"did":      did,
		"endpoint": endpoint,
	})
	if resp.Error != nil {
		t.Fatalf("daimon.peer.dial error: %+v", resp.Error)
	}
	var r peerDialResult
	resultAs(t, resp, &r)
	return r
}

// callInvoke issues a daimon.peer.invoke RPC through f's Unix socket.
func (f *peerFixture) callInvoke(t *testing.T, channelID, method string, params any) *Response {
	t.Helper()
	p := map[string]any{
		"channel_id": channelID,
		"method":     method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal invoke params: %v", err)
		}
		p["params"] = json.RawMessage(raw)
	}
	return f.call(t, "daimon.peer.invoke", p)
}

// queryAudit polls the activity log until it finds at least n entries of the
// given kind, or times out after 2 s. Background goroutines in handlePeerEcho
// may append slightly after the RPC returns, so a short wait is necessary.
func queryAudit(t *testing.T, alog *activity.Log, kind activity.Kind, wantAtLeast int) []*activity.Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := alog.Query(context.Background(), activity.QueryOptions{Kind: kind})
		if err != nil {
			t.Fatalf("activity query kind=%s: %v", kind, err)
		}
		if len(entries) >= wantAtLeast {
			return entries
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d audit entries kind=%s, got %d", wantAtLeast, kind, len(entries))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- core integration test ---------------------------------------------------

// TestPeerEcho_TwoDaemons is the cross-daimon smoke test for phase 33:
//   - Spawn two daemons (A and B), each listening on a random TCP port.
//   - A dials B using B's DID and peer address.
//   - A invokes peer.echo on B with message "hello from A".
//   - Verify the echo result carries B's DID and the original message.
//   - Verify A's audit log records peer.channel.opened and peer.invoke.sent.
//   - Verify B's audit log records peer.invoke.received.
//   - A closes the channel; peer.channel.closed appended to A's log.
func TestPeerEcho_TwoDaemons(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerFixture(t)

	// Dial: A → B
	dial := a.callDial(t, b.id.DID(), "tcp://"+b.peerAddr)
	if dial.ChannelID == "" {
		t.Fatal("dial returned empty channel_id")
	}
	if dial.PeerDID != b.id.DID() {
		t.Errorf("PeerDID: got %q want %q", dial.PeerDID, b.id.DID())
	}

	// Invoke peer.echo on B through A.
	const msg = "hello from A"
	invokeResp := a.callInvoke(t, dial.ChannelID, "peer.echo", map[string]any{"message": msg})
	if invokeResp.Error != nil {
		t.Fatalf("peer.echo invoke error: %+v", invokeResp.Error)
	}

	// The result wraps the peer's response under a "result" key.
	var outer peerInvokeResult
	resultAs(t, invokeResp, &outer)

	var echo peerEchoResult
	if err := json.Unmarshal(outer.Result, &echo); err != nil {
		t.Fatalf("unmarshal echo result: %v (raw=%s)", err, outer.Result)
	}
	if echo.Message != msg {
		t.Errorf("echo.Message: got %q want %q", echo.Message, msg)
	}
	if echo.FromDID != b.id.DID() {
		t.Errorf("echo.FromDID: got %q want %q", echo.FromDID, b.id.DID())
	}

	// --- Audit checks ---------------------------------------------------------

	// A must have peer.channel.opened.
	openedA := queryAudit(t, a.alog, activity.KindPeerChannelOpened, 1)
	if len(openedA) != 1 {
		t.Fatalf("A audit: want 1 peer.channel.opened, got %d", len(openedA))
	}

	// A must have peer.invoke.sent.
	sentA := queryAudit(t, a.alog, activity.KindPeerInvokeSent, 1)
	if len(sentA) != 1 {
		t.Fatalf("A audit: want 1 peer.invoke.sent, got %d", len(sentA))
	}

	// B must have peer.invoke.received (background goroutine, so we poll).
	queryAudit(t, b.alog, activity.KindPeerInvokeReceived, 1)

	// --- Close channel --------------------------------------------------------

	closeResp := a.call(t, "daimon.peer.close", map[string]any{"channel_id": dial.ChannelID})
	if closeResp.Error != nil {
		t.Fatalf("peer.close error: %+v", closeResp.Error)
	}

	// A must have peer.channel.closed.
	queryAudit(t, a.alog, activity.KindPeerChannelClosed, 1)

	// The channel should no longer appear in the list.
	listResp := a.call(t, "daimon.peer.list", nil)
	if listResp.Error != nil {
		t.Fatalf("peer.list error: %+v", listResp.Error)
	}
	var list peerListResult
	resultAs(t, listResp, &list)
	for _, ch := range list.Channels {
		if ch.ChannelID == dial.ChannelID {
			t.Errorf("channel %s still appears in list after close", dial.ChannelID)
		}
	}
}

// TestPeerEcho_MultipleMessages sends three echo messages on the same channel
// to verify the channel stays healthy across multiple round-trips.
func TestPeerEcho_MultipleMessages(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	channelID := dial.ChannelID

	messages := []string{"alpha", "beta", "gamma"}
	for _, msg := range messages {
		invokeResp := a.callInvoke(t, channelID, "peer.echo", map[string]any{"message": msg})
		if invokeResp.Error != nil {
			t.Fatalf("peer.echo %q: error %+v", msg, invokeResp.Error)
		}
		var outer peerInvokeResult
		resultAs(t, invokeResp, &outer)
		var echo peerEchoResult
		if err := json.Unmarshal(outer.Result, &echo); err != nil {
			t.Fatalf("unmarshal echo result: %v", err)
		}
		if echo.Message != msg {
			t.Errorf("message %q: echo got %q", msg, echo.Message)
		}
		if echo.FromDID != b.id.DID() {
			t.Errorf("message %q: from_did got %q want %q", msg, echo.FromDID, b.id.DID())
		}
	}

	// Three invocations → three peer.invoke.sent entries on A.
	queryAudit(t, a.alog, activity.KindPeerInvokeSent, 3)
}

// --- federation config (PeerListen integration) ------------------------------

// TestFederationConfig_WithPeerListener verifies that once PeerListen is
// called, daimon.federation.config reports the TCP endpoint and peer.echo.
func TestFederationConfig_WithPeerListener(t *testing.T) {
	f := newPeerFixture(t)

	resp := f.call(t, "daimon.federation.config", nil)
	if resp.Error != nil {
		t.Fatalf("federation.config error: %+v", resp.Error)
	}
	var cfg federationConfigResult
	resultAs(t, resp, &cfg)

	if cfg.DID == "" {
		t.Error("DID must be non-empty")
	}
	if cfg.PublicEndpoint == "" {
		t.Error("PublicEndpoint should be set once PeerListen is called")
	}
	want := "tcp://" + f.peerAddr
	if cfg.PublicEndpoint != want {
		t.Errorf("PublicEndpoint: got %q want %q", cfg.PublicEndpoint, want)
	}
	if len(cfg.Protocols) == 0 {
		t.Fatal("Protocols must not be empty")
	}
	found := false
	for _, p := range cfg.Protocols {
		if p == "peer.echo" {
			found = true
		}
	}
	if !found {
		t.Errorf("peer.echo not in protocols list: %v", cfg.Protocols)
	}
}

// --- parameter validation ----------------------------------------------------

func TestPeerDial_MissingDID(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.peer.dial", map[string]any{"endpoint": "127.0.0.1:1234"})
	if resp.Error == nil {
		t.Fatal("expected error for missing did")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d want CodeInvalidParams", resp.Error.Code)
	}
}

func TestPeerDial_MissingEndpoint(t *testing.T) {
	f := newPeerFixture(t)
	id, _ := identity.Generate()
	resp := f.call(t, "daimon.peer.dial", map[string]any{"did": id.DID()})
	if resp.Error == nil {
		t.Fatal("expected error for missing endpoint")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d want CodeInvalidParams", resp.Error.Code)
	}
}

func TestPeerDial_BadDID(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.peer.dial", map[string]any{
		"did":      "did:web:example.com",
		"endpoint": "127.0.0.1:1234",
	})
	if resp.Error == nil {
		t.Fatal("expected error for unsupported DID method")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d want CodeInvalidParams", resp.Error.Code)
	}
}

func TestPeerDial_UnreachableEndpoint(t *testing.T) {
	f := newPeerFixture(t)
	id, _ := identity.Generate()
	// Port 1 is almost certainly not a daimon; expect CodePeerUnreachable.
	resp := f.call(t, "daimon.peer.dial", map[string]any{
		"did":      id.DID(),
		"endpoint": "127.0.0.1:1",
	})
	if resp.Error == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
	if resp.Error.Code != CodePeerUnreachable {
		t.Errorf("code: got %d want CodePeerUnreachable (%d)", resp.Error.Code, CodePeerUnreachable)
	}
}

func TestPeerClose_NotFound(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.peer.close", map[string]any{"channel_id": "01HXNOTREAL00000000000000"})
	if resp.Error == nil {
		t.Fatal("expected error for unknown channel")
	}
	if resp.Error.Code != CodeNotFound {
		t.Errorf("code: got %d want CodeNotFound", resp.Error.Code)
	}
}

func TestPeerClose_MissingChannelID(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.peer.close", map[string]any{})
	if resp.Error == nil {
		t.Fatal("expected error for missing channel_id")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d want CodeInvalidParams", resp.Error.Code)
	}
}

func TestPeerInvoke_ChannelNotFound(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.peer.invoke", map[string]any{
		"channel_id": "01HXNOTREAL00000000000000",
		"method":     "peer.echo",
	})
	if resp.Error == nil {
		t.Fatal("expected error for unknown channel_id")
	}
	if resp.Error.Code != CodeNotFound {
		t.Errorf("code: got %d want CodeNotFound", resp.Error.Code)
	}
}

func TestPeerInvoke_MissingMethod(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.peer.invoke", map[string]any{
		"channel_id": "01HXNOTREAL00000000000000",
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing method")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code: got %d want CodeInvalidParams", resp.Error.Code)
	}
}

// --- daimon.peer.list --------------------------------------------------------

func TestPeerList_Empty(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.peer.list", nil)
	if resp.Error != nil {
		t.Fatalf("peer.list error: %+v", resp.Error)
	}
	var r peerListResult
	resultAs(t, resp, &r)
	// Channels must not be nil (JSON {"channels":[]}, not {"channels":null}).
	if r.Channels == nil {
		t.Error("Channels field must not be nil; got null in JSON")
	}
	if len(r.Channels) != 0 {
		t.Errorf("want 0 channels, got %d", len(r.Channels))
	}
}

func TestPeerList_ShowsOpenChannel(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)

	resp := a.call(t, "daimon.peer.list", nil)
	if resp.Error != nil {
		t.Fatalf("peer.list error: %+v", resp.Error)
	}
	var r peerListResult
	resultAs(t, resp, &r)

	if len(r.Channels) != 1 {
		t.Fatalf("want 1 channel, got %d", len(r.Channels))
	}
	ch := r.Channels[0]
	if ch.ChannelID != dial.ChannelID {
		t.Errorf("ChannelID: got %q want %q", ch.ChannelID, dial.ChannelID)
	}
	if ch.PeerDID != b.id.DID() {
		t.Errorf("PeerDID: got %q want %q", ch.PeerDID, b.id.DID())
	}
	if ch.OpenedAt == "" {
		t.Error("OpenedAt must not be empty")
	}
}

// TestPeerDial_WrongKey verifies that dialing with the right address but the
// wrong expected static key (a freshly-generated key that doesn't match B's
// actual Noise static) results in CodePeerUnreachable (Noise handshake fails).
func TestPeerDial_WrongKey(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerFixture(t) //nolint:unused — starts listening, we use b.peerAddr only

	// Use a fresh identity — its DID encodes a completely different Ed25519 key,
	// so Ed25519PublicToX25519 will derive a wrong X25519 static for B.
	imposter, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}

	resp := a.call(t, "daimon.peer.dial", map[string]any{
		"did":      imposter.DID(),  // wrong key — not B's
		"endpoint": b.peerAddr,     // correct address — B is actually listening here
	})
	if resp.Error == nil {
		t.Fatal("expected error when static key mismatch")
	}
	if resp.Error.Code != CodePeerUnreachable {
		t.Errorf("code: got %d want CodePeerUnreachable (%d)", resp.Error.Code, CodePeerUnreachable)
	}
}

// --- PeerListen without identity ---------------------------------------------

// TestPeerListen_RequiresIdentity verifies that PeerListen fails cleanly when
// the server hasn't been loaded with an identity (nil id). We construct a
// server manually with only memory/log to simulate the locked-mode case.
func TestPeerListen_RequiresIdentity(t *testing.T) {
	id, _ := identity.Generate()
	dir := t.TempDir()
	store, _ := memory.Open(filepath.Join(dir, "m.db"), id, memory.NullEmbedder{})
	defer store.Close()
	alog, _ := activity.Open(filepath.Join(dir, "a.log"), id)
	defer alog.Close()

	// Server built without Identity (simulates locked/serve mode).
	srv, err := New(Options{Store: store, Log: alog, Unlock: func(_ context.Context, _ string) (
		*identity.Identity, *memory.Store, *activity.Log, *wallet.Store, *wallet.Mnemonic, *addressbook.Book, error,
	) {
		return nil, nil, nil, nil, nil, nil, nil
	}})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	defer srv.Close()

	_, listenErr := srv.PeerListen("127.0.0.1:0")
	if listenErr == nil {
		t.Fatal("PeerListen should fail without an identity")
	}
}
