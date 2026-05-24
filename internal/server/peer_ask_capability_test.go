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
	"github.com/regitxx/Daimon/internal/capability"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/provider"
)

// ---------------------------------------------------------------------------
// Combined fixture: peer-ask + capability DB
// ---------------------------------------------------------------------------

// peerAskCapFixture is a serving-side daimon with an address book, a mock
// provider, a PeerListen TCP port, AND a capability.DB for token issuance
// and verification.
type peerAskCapFixture struct {
	*peerAskFixture
	capDB *capability.DB
}

// newPeerAskCapFixture constructs the combined fixture. It is identical to
// newPeerAskFixture but threads CapabilityDB through Options so that the
// server can both issue and verify capability tokens.
func newPeerAskCapFixture(t *testing.T) *peerAskCapFixture {
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

	capDB, err := capability.OpenDB(filepath.Join(dir, "capability.db"))
	if err != nil {
		t.Fatalf("capability.OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = capDB.Close() })

	reg := provider.NewRegistry()
	mock := &mockProvider{
		name:   "mock",
		models: []provider.Model{{ID: "mock-1", DisplayName: "Mock Provider"}},
	}
	reg.Register(mock)

	srv, err := New(Options{
		Identity:     id,
		Store:        store,
		Log:          alog,
		AddressBook:  ab,
		Providers:    reg,
		CapabilityDB: capDB,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	sockDir, err := os.MkdirTemp("", "dmncap")
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

	mock.resp = &provider.Response{
		Model:      "mock-1",
		Content:    "ok",
		StopReason: provider.StopReasonEndTurn,
		Usage:      provider.Usage{InputTokens: 1, OutputTokens: 1},
	}

	return &peerAskCapFixture{
		peerAskFixture: &peerAskFixture{
			peerFixture: &peerFixture{fixture: f, peerAddr: peerAddr},
			abook:       ab,
			mock:        mock,
		},
		capDB: capDB,
	}
}

// issueToken issues a capability token via B's local RPC socket and returns
// the token_id and base64url token string.
func issueToken(t *testing.T, b *peerAskCapFixture, verbs []string, extra map[string]any) (tokenID, token string) {
	t.Helper()
	p := map[string]any{"verbs": verbs}
	for k, v := range extra {
		p[k] = v
	}
	resp := b.call(t, "daimon.capability.issue", p)
	if resp.Error != nil {
		t.Fatalf("daimon.capability.issue error: %+v", resp.Error)
	}
	var r capabilityIssueResult
	b2, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(b2, &r)
	return r.TokenID, r.Token
}

// standardAskParams builds the peer.ask params map for the mock provider.
func standardAskParams(extra map[string]any) map[string]any {
	p := map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "ping"}},
		},
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestPeerAsk_CapabilityToken_OK verifies the happy path: a peer that is NOT
// in B's address book can invoke peer.ask by presenting a valid capability token
// that B itself issued.
func TestPeerAsk_CapabilityToken_OK(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	// B issues a token for "any" grantee (no address-book entry required).
	tokenID, token := issueToken(t, b, []string{"peer.ask"}, nil)

	// A dials B (A is not in B's address book).
	dial := a.callDial(t, b.id.DID(), b.peerAddr)

	// A presents the token alongside peer.ask params.
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token,
		"capability_token_id": tokenID,
	}))
	if resp.Error != nil {
		t.Fatalf("peer.ask with valid token: unexpected error: %+v", resp.Error)
	}

	var outer peerInvokeResult
	resultAs(t, resp, &outer)
	var ask peerAskResult
	_ = json.Unmarshal(outer.Result, &ask)
	if ask.Response == nil || ask.Response.Content != "ok" {
		t.Errorf("unexpected response: %+v", ask.Response)
	}

	// B must record a KindCapabilityVerified audit entry.
	queryAudit(t, b.alog, activity.KindCapabilityVerified, 1)
}

// TestPeerAsk_CapabilityToken_Revoked verifies that a revoked token is rejected
// even if its Biscuit signature is otherwise valid.
func TestPeerAsk_CapabilityToken_Revoked(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	tokenID, token := issueToken(t, b, []string{"peer.ask"}, nil)

	// Revoke the token via B's RPC.
	revResp := b.call(t, "daimon.capability.revoke", map[string]any{"token_id": tokenID})
	if revResp.Error != nil {
		t.Fatalf("revoke: %+v", revResp.Error)
	}

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token,
		"capability_token_id": tokenID,
	}))
	if resp.Error == nil {
		t.Fatal("expected error for revoked token, got nil")
	}
	// Error propagates from the peer channel as CodeInternalError.
	if resp.Error.Code != CodeInternalError {
		t.Errorf("code: got %d want CodeInternalError (%d)", resp.Error.Code, CodeInternalError)
	}

	// B must record KindCapabilityDenied.
	queryAudit(t, b.alog, activity.KindCapabilityDenied, 1)
}

// TestPeerAsk_CapabilityToken_Expired verifies that an expired token is rejected.
func TestPeerAsk_CapabilityToken_Expired(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	// Issue a token that expired one second ago.
	past := time.Now().Add(-time.Second).UTC().Truncate(time.Second).Format(time.RFC3339)
	_, token := issueToken(t, b, []string{"peer.ask"}, map[string]any{
		"valid_until": past,
	})

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token": token,
		// no token_id — attenuated tokens may not have one
	}))
	if resp.Error == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

// TestPeerAsk_CapabilityToken_WrongVerb verifies that a token issued for a
// different verb (e.g. "peer.echo") cannot authorize peer.ask.
func TestPeerAsk_CapabilityToken_WrongVerb(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	_, token := issueToken(t, b, []string{"peer.echo"}, nil)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token": token,
	}))
	if resp.Error == nil {
		t.Fatal("expected error for wrong-verb token, got nil")
	}
}

// TestPeerAsk_CapabilityToken_BlockedCannotBypass verifies that a peer who is
// explicitly in B's address book WITHOUT peer.ask cannot bypass the denial
// by presenting an otherwise-valid capability token.
func TestPeerAsk_CapabilityToken_BlockedCannotBypass(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	// Add A to B's book but authorize only peer.echo, NOT peer.ask.
	aMultibase := identity.MultibaseFragment(a.id.DID())
	_ = b.abook.Add(a.id.DID(), "Daemon A", aMultibase)
	_ = b.abook.Pin(a.id.DID(), []string{"peer.echo"})
	_ = b.abook.Save()

	tokenID, token := issueToken(t, b, []string{"peer.ask"}, nil)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token,
		"capability_token_id": tokenID,
	}))
	if resp.Error == nil {
		t.Fatal("expected error: blocked peer should not bypass with token")
	}
}

// TestPeerAsk_CapabilityToken_NoDB verifies that a daimon without a
// capability.DB rejects any request that presents a capability_token.
// The peer must not accidentally be allowed through the address-book path
// either (A is not in B's book).
func TestPeerAsk_CapabilityToken_NoDB(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskFixture(t) // standard fixture: address book but NO capDB

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	// Present a plausible-looking (but fake) base64url token.
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token": "dGVzdA", // base64url("test") — invalid Biscuit but well-formed base64
	}))
	if resp.Error == nil {
		t.Fatal("expected error when server has no capDB")
	}
}

// TestPeerAsk_CapabilityToken_MaxCallsEnforced verifies that a token with
// max_calls=2 is denied on the third call.
func TestPeerAsk_CapabilityToken_MaxCallsEnforced(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	tokenID, token := issueToken(t, b, []string{"peer.ask"}, map[string]any{
		"max_calls": 2,
	})

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	invoke := func() *Response {
		return a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
			"capability_token":    token,
			"capability_token_id": tokenID,
		}))
	}

	// First two calls must succeed.
	if resp := invoke(); resp.Error != nil {
		t.Fatalf("call 1: unexpected error: %+v", resp.Error)
	}
	if resp := invoke(); resp.Error != nil {
		t.Fatalf("call 2: unexpected error: %+v", resp.Error)
	}

	// Third call must fail (calls_used == max_calls).
	if resp := invoke(); resp.Error == nil {
		t.Fatal("call 3: expected error (max_calls exceeded), got nil")
	}

	// Two verified + one denied.
	queryAudit(t, b.alog, activity.KindCapabilityVerified, 2)
	queryAudit(t, b.alog, activity.KindCapabilityDenied, 1)
}

// TestPeerAsk_CapabilityToken_PinnedPeerStillWorksWithoutToken confirms that
// the existing address-book path is unaffected by Phase 43 changes.
func TestPeerAsk_CapabilityToken_PinnedPeerStillWorksWithoutToken(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	// Pin A in B's address book with peer.ask.
	aMultibase := identity.MultibaseFragment(a.id.DID())
	_ = b.abook.Add(a.id.DID(), "Daemon A", aMultibase)
	_ = b.abook.Pin(a.id.DID(), []string{"peer.ask"})
	_ = b.abook.Save()

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	// No capability_token in params.
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(nil))
	if resp.Error != nil {
		t.Fatalf("pinned peer without token: unexpected error: %+v", resp.Error)
	}
}
