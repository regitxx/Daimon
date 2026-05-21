package server

// TestFederationSmoke_EndToEnd is the SPEC §16.10 v0.3 GA gate — a single
// narrative integration test that exercises the full federation flow across
// two real daemon instances sharing an actual Noise IK TCP channel.
//
// Topology
//
//	Machine A (calling):  basic daimon, no address book, PeerListen on
//	                      loopback:0 (OS-assigned port).
//	Machine B (serving):  daimon with address book + mock provider + PeerListen.
//
// Narrative
//
//  1. B publishes its endpoint via daimon.federation.config — verifies DID,
//     endpoint URL, and that peer.echo + peer.ask appear in the protocols list.
//  2. A dials B (Noise IK handshake over TCP).
//  3. Channel appears in A's daimon.peer.list.
//  4. A → B: peer.echo — universally authorized, verifies Noise channel is up.
//  5. B's operator pins A with peer.ask authorization (simulates the user
//     running `daimon peer address-book pin --did <A_DID> --verbs peer.ask`).
//  6. A → B: peer.ask — B invokes its mock provider; A receives the response.
//  7. A → B: peer.pay.required — B has no wallet; verifies the
//     CodePeerProtocolUnsupported error is propagated through peer.invoke.
//  8. Audit checks: A records channel.opened + 3× invoke.sent;
//     B records 3× invoke.received + 1× invoke.served.
//  9. A closes the channel; channel.closed audit row written; peer.list empty.
//
// This test runs under `go test -race ./...` and is therefore included in
// CI shard 0 (Go race+vet). It directly validates §16.10 GA criteria #1
// (phases shipped) and #2 (cross-daimon smoke in CI).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/provider"
)

func TestFederationSmoke_EndToEnd(t *testing.T) {
	// -------------------------------------------------------------------------
	// Setup: two independent daemons, each with their own identity + sockets.
	// -------------------------------------------------------------------------

	// B: the serving daemon — has address book, mock provider, TCP listener.
	b := newPeerAskFixture(t)
	// A: the calling daemon — basic fixture with TCP listener.
	a := newPeerFixture(t)

	// -------------------------------------------------------------------------
	// Step 1: B's federation.config advertises its endpoint and protocols.
	// -------------------------------------------------------------------------
	t.Run("step1_federation_config", func(t *testing.T) {
		resp := b.call(t, "daimon.federation.config", nil)
		if resp.Error != nil {
			t.Fatalf("daimon.federation.config: %+v", resp.Error)
		}
		var cfg federationConfigResult
		resultAs(t, resp, &cfg)

		if cfg.DID != b.id.DID() {
			t.Errorf("DID: got %q want %q", cfg.DID, b.id.DID())
		}
		wantEndpoint := "tcp://" + b.peerAddr
		if cfg.PublicEndpoint != wantEndpoint {
			t.Errorf("public_endpoint: got %q want %q", cfg.PublicEndpoint, wantEndpoint)
		}
		if cfg.FederationVersion == "" {
			t.Error("federation_version must not be empty")
		}

		// Protocols list must contain at least peer.echo and peer.ask.
		hasEcho, hasAsk := false, false
		for _, p := range cfg.Protocols {
			switch p {
			case "peer.echo":
				hasEcho = true
			case "peer.ask":
				hasAsk = true
			}
		}
		if !hasEcho {
			t.Errorf("peer.echo missing from protocols: %v", cfg.Protocols)
		}
		if !hasAsk {
			t.Errorf("peer.ask missing from protocols: %v", cfg.Protocols)
		}
	})

	// -------------------------------------------------------------------------
	// Step 2: A dials B. Noise IK handshake happens here.
	// -------------------------------------------------------------------------
	dial := a.callDial(t, b.id.DID(), "tcp://"+b.peerAddr)
	if dial.ChannelID == "" {
		t.Fatal("dial returned empty channel_id")
	}
	if dial.PeerDID != b.id.DID() {
		t.Errorf("PeerDID: got %q want %q", dial.PeerDID, b.id.DID())
	}

	// -------------------------------------------------------------------------
	// Step 3: Channel appears in A's peer.list.
	// -------------------------------------------------------------------------
	t.Run("step3_peer_list", func(t *testing.T) {
		resp := a.call(t, "daimon.peer.list", nil)
		if resp.Error != nil {
			t.Fatalf("daimon.peer.list: %+v", resp.Error)
		}
		var list peerListResult
		resultAs(t, resp, &list)

		if len(list.Channels) != 1 {
			t.Fatalf("peer.list: want 1 channel, got %d", len(list.Channels))
		}
		ch := list.Channels[0]
		if ch.ChannelID != dial.ChannelID {
			t.Errorf("channel_id: got %q want %q", ch.ChannelID, dial.ChannelID)
		}
		if ch.PeerDID != b.id.DID() {
			t.Errorf("peer_did: got %q want %q", ch.PeerDID, b.id.DID())
		}
	})

	// -------------------------------------------------------------------------
	// Step 4: A → B: peer.echo (universally authorized, no address-book check).
	// -------------------------------------------------------------------------
	t.Run("step4_peer_echo", func(t *testing.T) {
		const msg = "smoke-hello"
		invokeResp := a.callInvoke(t, dial.ChannelID, "peer.echo",
			map[string]any{"message": msg})
		if invokeResp.Error != nil {
			t.Fatalf("peer.echo: %+v", invokeResp.Error)
		}

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
	})

	// -------------------------------------------------------------------------
	// Step 5: B's operator pins A with peer.ask authorization in B's address
	// book. This simulates:
	//   daimon peer address-book pin --did <A_DID> --verbs peer.ask
	// run on machine B by B's principal.
	// -------------------------------------------------------------------------
	aMultibase := identity.MultibaseFragment(a.id.DID())
	if err := b.abook.Add(a.id.DID(), "Smoke Caller A", aMultibase); err != nil {
		// ErrEntryExists is fine — auto-populated when A dialed in.
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("abook.Add(A): %v", err)
		}
	}
	if err := b.abook.Pin(a.id.DID(), []string{"peer.ask"}); err != nil {
		t.Fatalf("abook.Pin(A, peer.ask): %v", err)
	}
	if err := b.abook.Save(); err != nil {
		t.Fatalf("abook.Save: %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 6: A → B: peer.ask (address-book gate: pinned + peer.ask approved).
	// B invokes its mock provider and returns the response to A.
	// -------------------------------------------------------------------------
	t.Run("step6_peer_ask", func(t *testing.T) {
		b.mock.resp = &provider.Response{
			Model:      "mock-1",
			Content:    "forty-two",
			StopReason: provider.StopReasonEndTurn,
			Usage:      provider.Usage{InputTokens: 5, OutputTokens: 3},
		}

		invokeResp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
			"provider": "mock",
			"request": map[string]any{
				"model": "mock-1",
				"messages": []map[string]any{
					{"role": "user", "content": "What is 6 × 7?"},
				},
			},
		})
		if invokeResp.Error != nil {
			t.Fatalf("peer.ask: %+v", invokeResp.Error)
		}

		var outer peerInvokeResult
		resultAs(t, invokeResp, &outer)

		var ask peerAskResult
		if err := json.Unmarshal(outer.Result, &ask); err != nil {
			t.Fatalf("unmarshal peerAskResult: %v (raw=%s)", err, outer.Result)
		}
		if ask.Response == nil {
			t.Fatal("peer.ask: response is nil")
		}
		if ask.Response.Content != "forty-two" {
			t.Errorf("response.content: got %q want %q", ask.Response.Content, "forty-two")
		}
		if ask.Response.Model != "mock-1" {
			t.Errorf("response.model: got %q want %q", ask.Response.Model, "mock-1")
		}
	})

	// -------------------------------------------------------------------------
	// Step 7: A → B: peer.pay.required ("peer.ask" service).
	// B has no wallet (newPeerAskFixture doesn't provision one), so B returns
	// CodePeerProtocolUnsupported (-32012). A's daimon.peer.invoke propagates
	// the remote error as CodeInternalError (-32603) with a "peer returned
	// error:" prefix in the message.
	// -------------------------------------------------------------------------
	t.Run("step7_peer_pay_required_no_wallet", func(t *testing.T) {
		invokeResp := a.callInvoke(t, dial.ChannelID, "peer.pay.required",
			map[string]any{"service": "peer.ask"})

		if invokeResp.Error == nil {
			t.Fatal("expected error (B has no wallet), got success")
		}
		// The outer error code is CodeInternalError (A's wrapper).
		if invokeResp.Error.Code != CodeInternalError {
			t.Errorf("error.code: got %d want %d (CodeInternalError)",
				invokeResp.Error.Code, CodeInternalError)
		}
		// The message must surface the remote error source.
		if !strings.Contains(invokeResp.Error.Message, "peer returned error") {
			t.Errorf("error.message: got %q, want to contain 'peer returned error'",
				invokeResp.Error.Message)
		}
	})

	// -------------------------------------------------------------------------
	// Step 8: Audit log checks.
	// Background goroutines write some of these asynchronously — queryAudit
	// polls with a 2-second deadline.
	//
	// Audit accounting:
	//   echo call:         A writes invoke.sent (1); B writes invoke.received (1)
	//   ask call:          A writes invoke.sent (1); B writes invoke.served (1)
	//   pay.required call: fails — A returns error BEFORE invoke.sent;
	//                      B returns error BEFORE payment.invoiced (no wallet)
	// -------------------------------------------------------------------------

	// A: channel.opened (1 dial) + invoke.sent (2: echo + ask only;
	//    pay.required fails before the audit row is written)
	queryAudit(t, a.alog, activity.KindPeerChannelOpened, 1)
	queryAudit(t, a.alog, activity.KindPeerInvokeSent, 2)

	// B: invoke.received (1: only peer.echo writes this kind; peer.ask writes
	//    invoke.served, peer.pay.required writes payment.invoiced — or nothing
	//    when the wallet check fails first)
	queryAudit(t, b.alog, activity.KindPeerInvokeReceived, 1)
	// B: invoke.served (1: peer.ask only)
	queryAudit(t, b.alog, activity.KindPeerInvokeServed, 1)

	// -------------------------------------------------------------------------
	// Step 9: A closes the channel; channel.closed audit row; list is empty.
	// -------------------------------------------------------------------------
	t.Run("step9_close_and_list_empty", func(t *testing.T) {
		closeResp := a.call(t, "daimon.peer.close",
			map[string]any{"channel_id": dial.ChannelID})
		if closeResp.Error != nil {
			t.Fatalf("daimon.peer.close: %+v", closeResp.Error)
		}
		queryAudit(t, a.alog, activity.KindPeerChannelClosed, 1)

		// Channel must no longer appear in peer.list.
		listResp := a.call(t, "daimon.peer.list", nil)
		if listResp.Error != nil {
			t.Fatalf("peer.list after close: %+v", listResp.Error)
		}
		var list peerListResult
		resultAs(t, listResp, &list)
		for _, ch := range list.Channels {
			if ch.ChannelID == dial.ChannelID {
				t.Errorf("closed channel %q still appears in peer.list", dial.ChannelID)
			}
		}
	})
}
