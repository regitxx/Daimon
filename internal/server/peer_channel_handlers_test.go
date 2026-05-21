package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/addressbook"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/provider"
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

// =============================================================================
// Phase 34: peer.ask tests
// =============================================================================

// peerAskFixture extends peerFixture with an address book and a mock provider
// registry. B-side daemons that serve peer.ask need both.
type peerAskFixture struct {
	*peerFixture
	abook *addressbook.Book
	mock  *mockProvider
}

// newPeerAskFixture builds a fixture with:
//   - identity + memory + activity log (from newFixture)
//   - address book (random encryption key, in-memory)
//   - mock provider registry (provider name "mock", model "mock-1")
//   - TCP peer listener on a random port
func newPeerAskFixture(t *testing.T) *peerAskFixture {
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

	abKey := make([]byte, 32)
	_, _ = rand.Read(abKey)
	ab, err := addressbook.Open(filepath.Join(dir, "address_book.enc"), abKey)
	if err != nil {
		t.Fatalf("addressbook.Open: %v", err)
	}
	t.Cleanup(func() { _ = ab.Close() })

	reg := provider.NewRegistry()
	mock := &mockProvider{
		name:   "mock",
		models: []provider.Model{{ID: "mock-1", DisplayName: "Mock Provider"}},
	}
	reg.Register(mock)

	srv, err := New(Options{
		Identity:    id,
		Store:       store,
		Log:         alog,
		AddressBook: ab,
		Providers:   reg,
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
	peerAddr, err := srv.PeerListen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("PeerListen: %v", err)
	}

	return &peerAskFixture{
		peerFixture: &peerFixture{fixture: f, peerAddr: peerAddr},
		abook:       ab,
		mock:        mock,
	}
}

// --- peer.ask: happy path -----------------------------------------------------

// TestPeerAsk_AuthorizedPeer is the cross-daimon smoke for phase 34:
//   - A dials B (B has address book + mock provider + PeerListen)
//   - B's operator pins A with peer.ask in B's address book
//   - A invokes peer.ask on B via daimon.peer.invoke
//   - B calls its mock provider and returns the response to A
//   - A's outer result wraps B's response
//   - B's audit log has peer.invoke.served row
func TestPeerAsk_AuthorizedPeer(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskFixture(t)

	// A dials B: this auto-adds B to A's address book (best-effort, no book on A).
	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	channelID := dial.ChannelID

	// B's operator pins A with peer.ask authorization in B's address book.
	// We do this directly on the book (simulates `daimon.peer.address_book.pin`).
	// A's DID + its TransportPubKeyMultibase (from the did:key fragment).
	aMultibase := identity.MultibaseFragment(a.id.DID())
	if err := b.abook.Add(a.id.DID(), "Daemon A", aMultibase); err != nil && err.Error() != "address book: entry already exists" {
		t.Fatalf("abook.Add(A): %v", err)
	}
	if err := b.abook.Pin(a.id.DID(), []string{"peer.ask"}); err != nil {
		t.Fatalf("abook.Pin(A, peer.ask): %v", err)
	}
	if err := b.abook.Save(); err != nil {
		t.Fatalf("abook.Save: %v", err)
	}

	// Set a canned response on B's mock provider.
	b.mock.resp = &provider.Response{
		Model:      "mock-1",
		Content:    "four",
		StopReason: provider.StopReasonEndTurn,
		Usage:      provider.Usage{InputTokens: 10, OutputTokens: 1},
	}

	// A invokes peer.ask on B.
	invokeResp := a.callInvoke(t, channelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "what is 2+2?"}},
		},
	})
	if invokeResp.Error != nil {
		t.Fatalf("peer.ask invoke error: %+v", invokeResp.Error)
	}

	// A's result is the peerInvokeResult envelope wrapping B's peerAskResult.
	var outer peerInvokeResult
	resultAs(t, invokeResp, &outer)

	var ask peerAskResult
	if err := json.Unmarshal(outer.Result, &ask); err != nil {
		t.Fatalf("unmarshal peerAskResult: %v (raw=%s)", err, outer.Result)
	}
	if ask.Response == nil {
		t.Fatal("peerAskResult.Response is nil")
	}
	if ask.Response.Content != "four" {
		t.Errorf("content: got %q want %q", ask.Response.Content, "four")
	}
	if ask.Response.Model != "mock-1" {
		t.Errorf("model: got %q want %q", ask.Response.Model, "mock-1")
	}

	// Verify the request that reached B's mock provider.
	if len(b.mock.lastReq.Messages) != 1 {
		t.Fatalf("mock received %d messages, want 1", len(b.mock.lastReq.Messages))
	}
	if b.mock.lastReq.Messages[0].Content != "what is 2+2?" {
		t.Errorf("mock message content: got %q", b.mock.lastReq.Messages[0].Content)
	}

	// B must audit peer.invoke.served (background goroutine — poll).
	queryAudit(t, b.alog, activity.KindPeerInvokeServed, 1)
}

// --- peer.ask: authorization failures ----------------------------------------

// TestPeerAsk_NoAddressBook verifies that a daemon with no address book
// returns CodePeerProtocolUnsupported for peer.ask.
func TestPeerAsk_NoAddressBook(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerFixture(t) // no address book

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "ping"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error when server has no address book")
	}
	// peer.ask is not supported (address book not configured).
	if resp.Error.Code != CodePeerProtocolUnsupported && resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want CodePeerProtocolUnsupported (%d) or CodeInternalError (%d)",
			resp.Error.Code, CodePeerProtocolUnsupported, CodeInternalError)
	}
}

// TestPeerAsk_PeerNotInAddressBook verifies that a peer who is not in B's
// address book receives CodePeerUnauthorized.
func TestPeerAsk_PeerNotInAddressBook(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskFixture(t) // has address book, but A is not in it

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "ping"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error when peer not in address book")
	}
	if resp.Error.Code != CodeInternalError {
		// The error propagates through peerInvokeResult as CodeInternalError
		// (peer returned error → we wrap it).
		t.Errorf("code: got %d want CodeInternalError (%d)", resp.Error.Code, CodeInternalError)
	}
}

// TestPeerAsk_PeerInBookButNotAuthorized verifies CodePeerUnauthorized when
// the peer IS in the address book but "peer.ask" is not in ApprovedVerbs.
func TestPeerAsk_PeerInBookButNotAuthorized(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)

	// Add A to B's book but only authorize peer.echo — NOT peer.ask.
	aMultibase := identity.MultibaseFragment(a.id.DID())
	_ = b.abook.Add(a.id.DID(), "", aMultibase)
	_ = b.abook.Pin(a.id.DID(), []string{"peer.echo"})
	_ = b.abook.Save()

	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "ping"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error when peer.ask not in ApprovedVerbs")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want CodeInternalError (%d)", resp.Error.Code, CodeInternalError)
	}
}

// --- peer.ask: param validation ----------------------------------------------

// TestPeerAsk_MissingProvider verifies CodeInvalidParams when provider is omitted.
func TestPeerAsk_MissingProvider(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	aMultibase := identity.MultibaseFragment(a.id.DID())
	_ = b.abook.Add(a.id.DID(), "", aMultibase)
	_ = b.abook.Pin(a.id.DID(), []string{"peer.ask"})
	_ = b.abook.Save()

	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "ping"}},
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing provider")
	}
	// Error propagates as CodeInternalError (peer's error wrapped by handlePeerInvoke).
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want CodeInternalError (%d)", resp.Error.Code, CodeInternalError)
	}
}

// --- address book auto-populate on dial -------------------------------------

// TestPeerDial_AutoPopulatesAddressBook verifies that a successful dial
// adds the peer to the dialing daimon's address book as FirstSeen.
func TestPeerDial_AutoPopulatesAddressBook(t *testing.T) {
	a := newPeerAskFixture(t) // A has an address book
	b := newPeerFixture(t)

	// Before dial: A's book is empty.
	if n := a.abook.Len(); n != 0 {
		t.Fatalf("expected empty book before dial, got %d entries", n)
	}

	// A dials B.
	a.callDial(t, b.id.DID(), b.peerAddr)

	// After dial: B's DID should be in A's book as FirstSeen.
	entry, ok := a.abook.Lookup(b.id.DID())
	if !ok {
		t.Fatal("expected B's DID in A's address book after dial")
	}
	if entry.Status != addressbook.FirstSeen {
		t.Errorf("entry status: got %v want FirstSeen", entry.Status)
	}
	want := identity.MultibaseFragment(b.id.DID())
	if entry.TransportPubKeyMultibase != want {
		t.Errorf("TransportPubKeyMultibase: got %q want %q", entry.TransportPubKeyMultibase, want)
	}
}

// TestPeerDial_SecondDialTouchesExistingEntry verifies that a second dial to
// the same peer updates LastSeen without error (Touch idempotent on same key).
func TestPeerDial_SecondDialTouchesExistingEntry(t *testing.T) {
	a := newPeerAskFixture(t)
	b := newPeerFixture(t)

	a.callDial(t, b.id.DID(), b.peerAddr)
	first, _ := a.abook.Lookup(b.id.DID())
	firstSeen := first.LastSeen

	// Brief sleep to ensure LastSeen changes (nanosecond precision).
	time.Sleep(2 * time.Millisecond)
	a.callDial(t, b.id.DID(), b.peerAddr)

	second, ok := a.abook.Lookup(b.id.DID())
	if !ok {
		t.Fatal("entry disappeared after second dial")
	}
	if second.LastSeen.Before(firstSeen) {
		t.Errorf("LastSeen went backwards: first=%v second=%v", firstSeen, second.LastSeen)
	}
}

// =============================================================================
// Phase 35: peer.pay.required tests
// =============================================================================

// peerPayFixture extends peerAskFixture with a real wallet store so the
// serving daimon has a payment address to advertise.
type peerPayFixture struct {
	*peerAskFixture
	wstore  *wallet.Store
	payAddr string // the EVM address in the Base Sepolia wallet
}

// newPeerPayFixture builds a fixture with:
//   - identity + memory + activity + address book + mock provider (from newPeerAskFixture)
//   - wallet store with one evm:base-sepolia wallet
//   - TCP peer listener on a random port
func newPeerPayFixture(t *testing.T) *peerPayFixture {
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

	abKey := make([]byte, 32)
	_, _ = rand.Read(abKey)
	ab, err := addressbook.Open(filepath.Join(dir, "address_book.enc"), abKey)
	if err != nil {
		t.Fatalf("addressbook.Open: %v", err)
	}
	t.Cleanup(func() { _ = ab.Close() })

	// Wallet store with an EVM Base Sepolia wallet for payment address.
	wstore, _, err := wallet.Open(filepath.Join(dir, "wallet.ks"), []byte("testpass"))
	if err != nil {
		t.Fatalf("wallet.Open: %v", err)
	}
	t.Cleanup(func() { _ = wstore.Close() })
	w, err := wstore.CreateWallet("evm:base-sepolia")
	if err != nil {
		t.Fatalf("wstore.CreateWallet: %v", err)
	}

	reg := provider.NewRegistry()
	mock := &mockProvider{
		name:   "mock",
		models: []provider.Model{{ID: "mock-1", DisplayName: "Mock Provider"}},
	}
	reg.Register(mock)

	srv, err := New(Options{
		Identity:    id,
		Store:       store,
		Log:         alog,
		AddressBook: ab,
		Wallet:      wstore,
		Providers:   reg,
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
	peerAddr, err := srv.PeerListen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("PeerListen: %v", err)
	}

	return &peerPayFixture{
		peerAskFixture: &peerAskFixture{
			peerFixture: &peerFixture{fixture: f, peerAddr: peerAddr},
			abook:       ab,
			mock:        mock,
		},
		wstore:  wstore,
		payAddr: w.Address,
	}
}

// --- peer.pay.required: happy path -------------------------------------------

// TestPeerPayRequired_ReturnsRequirements is the happy path for phase 35:
// a peer calls peer.pay.required on a daimon that has a wallet configured,
// and receives valid USDC-on-Base-Sepolia payment requirements.
func TestPeerPayRequired_ReturnsRequirements(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerPayFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.pay.required", map[string]any{
		"service": "peer.ask",
	})
	if resp.Error != nil {
		t.Fatalf("peer.pay.required error: %+v", resp.Error)
	}

	var outer peerInvokeResult
	resultAs(t, resp, &outer)

	var pay peerPayRequiredResult
	if err := json.Unmarshal(outer.Result, &pay); err != nil {
		t.Fatalf("unmarshal peerPayRequiredResult: %v (raw=%s)", err, outer.Result)
	}
	if len(pay.Requirements) == 0 {
		t.Fatal("expected at least one payment requirement")
	}
	r := pay.Requirements[0]
	if r.PayTo != b.payAddr {
		t.Errorf("PayTo: got %q want %q", r.PayTo, b.payAddr)
	}
	if r.Scheme != "exact" {
		t.Errorf("Scheme: got %q want %q", r.Scheme, "exact")
	}
	if r.Network != "base-sepolia" {
		t.Errorf("Network: got %q want %q", r.Network, "base-sepolia")
	}
	if r.Resource != "peer.ask" {
		t.Errorf("Resource: got %q want %q", r.Resource, "peer.ask")
	}
	if r.MaxAmountRequired == "" {
		t.Error("MaxAmountRequired must not be empty")
	}
	if r.MaxTimeoutSeconds <= 0 {
		t.Errorf("MaxTimeoutSeconds: got %d, want > 0", r.MaxTimeoutSeconds)
	}
	if r.Asset == "" {
		t.Error("Asset (USDC contract address) must not be empty")
	}
}

// --- peer.pay.required: error paths ------------------------------------------

// TestPeerPayRequired_NoWallet verifies CodePeerProtocolUnsupported (surfaced
// as CodeInternalError through the invoke wrapper) when the serving daimon
// has no wallet configured.
func TestPeerPayRequired_NoWallet(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerFixture(t) // no wallet store

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.pay.required", map[string]any{
		"service": "peer.ask",
	})
	if resp.Error == nil {
		t.Fatal("expected error when serving daimon has no wallet")
	}
	// The peer's CodePeerProtocolUnsupported propagates back as CodeInternalError
	// through handlePeerInvoke's "peer returned error" wrapping.
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want CodeInternalError (%d)", resp.Error.Code, CodeInternalError)
	}
}

// TestPeerPayRequired_UnknownService verifies CodeInvalidParams for services
// that have no payment requirements in v0.3 (only peer.ask is payable).
func TestPeerPayRequired_UnknownService(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerPayFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	// peer.echo is free — asking for its payment requirements is an error.
	resp := a.callInvoke(t, dial.ChannelID, "peer.pay.required", map[string]any{
		"service": "peer.echo",
	})
	if resp.Error == nil {
		t.Fatal("expected error for non-payable service")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want CodeInternalError (%d)", resp.Error.Code, CodeInternalError)
	}
}

// TestPeerPayRequired_MissingService verifies CodeInvalidParams when the
// service field is omitted from the request.
func TestPeerPayRequired_MissingService(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerPayFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.pay.required", map[string]any{})
	if resp.Error == nil {
		t.Fatal("expected error for missing service field")
	}
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want CodeInternalError (%d)", resp.Error.Code, CodeInternalError)
	}
}

// --- peer.pay.required: audit ------------------------------------------------

// TestPeerPayRequired_AuditsInvoiced verifies that a successful call to
// peer.pay.required appends a peer.payment.invoiced row to the SERVING
// daimon's audit log.
func TestPeerPayRequired_AuditsInvoiced(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerPayFixture(t)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.pay.required", map[string]any{
		"service": "peer.ask",
	})
	if resp.Error != nil {
		t.Fatalf("peer.pay.required error: %+v", resp.Error)
	}

	// The audit write is async (background goroutine); queryAudit polls
	// until it arrives or the 2-second timeout fires.
	queryAudit(t, b.alog, activity.KindPeerPaymentInvoiced, 1)
}

// --- federation config: peer.pay.required in protocols -----------------------

func TestFederationConfig_IncludesPeerPayRequired(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.federation.config", nil)
	if resp.Error != nil {
		t.Fatalf("federation.config error: %+v", resp.Error)
	}
	var cfg federationConfigResult
	resultAs(t, resp, &cfg)

	found := false
	for _, p := range cfg.Protocols {
		if p == "peer.pay.required" {
			found = true
		}
	}
	if !found {
		t.Errorf("peer.pay.required not in protocols list: %v", cfg.Protocols)
	}
}

// --- federation config: peer.ask in protocols --------------------------------

func TestFederationConfig_IncludesPeerAsk(t *testing.T) {
	f := newPeerFixture(t)
	resp := f.call(t, "daimon.federation.config", nil)
	if resp.Error != nil {
		t.Fatalf("federation.config error: %+v", resp.Error)
	}
	var cfg federationConfigResult
	resultAs(t, resp, &cfg)

	found := false
	for _, p := range cfg.Protocols {
		if p == "peer.ask" {
			found = true
		}
	}
	if !found {
		t.Errorf("peer.ask not in protocols: %v", cfg.Protocols)
	}
}
