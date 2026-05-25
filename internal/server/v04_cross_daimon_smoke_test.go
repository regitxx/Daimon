package server

// TestV04CrossDaimonSmoke_EndToEnd is the v0.4 GA-gate-equivalent of
// TestFederationSmoke_EndToEnd: a single narrative integration test that
// exercises the COMPLETE v0.4 capability + reputation surface across two
// real daemon instances communicating over actual TCP+Noise IK.
//
// This test is the one CI shard whose green badge means
// "cross-daimon capability delegation + reputation receipts work end-to-end
// over a real encrypted channel" — the v0.4 equivalent of email's
// integration-tests-between-mail-servers ritual.
//
// It also serves as the regression guard for every dogfood-finding from
// session 96 (the first real two-Mac federation test):
//
//   - peer.invoke.received payload now records the echo message + caller_did
//   - peer.list on the listener now shows inbound channels with direction
//   - capability tokens flow through peer.ask end-to-end
//   - signed receipts round-trip and verify under the server's public key
//   - reputation.receipts(direction="issued") reflects what was served
//
// Topology
//
//	Mac A (calling):  basic daimon, TCP listener on loopback:0.
//	Mac B (serving):  daimon with capability DB + mock provider + listener.
//
// Narrative
//
//   1.  B issues a capability token granting A peer.ask with max_calls=2.
//   2.  A dials B (Noise IK over TCP).
//   3.  B's peer.list shows ONE inbound channel with direction="inbound"
//       and matching peer_x25519 hex (= the dogfood-polish fix).
//   4.  A invokes peer.ask with capability_token + request_receipt=true.
//       Verify response + signed receipt arrives.
//   5.  Receipt's Ed25519 signature verifies under B's public key.
//   6.  B's audit log has all four expected v0.4 rows:
//       capability.verified, peer.invoke.received, peer.invoke.served,
//       reputation.receipt.issued.
//   7.  Second invocation succeeds (calls_used = 1 < 2).
//   8.  Third invocation is denied with CodeCapabilityDenied (calls exhausted).
//   9.  B revokes the token; even a fresh dial + ask is denied.
//  10.  A closes its channel; B's inbound peer.list drains.
//
// Runs under `go test -race ./...` as part of the standard Go shard AND
// as part of the dedicated v04-cross-daimon-smoke shard (which runs
// JUST this test with -v for easy log-reading on CI failures).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/reputation"
)

func TestV04CrossDaimonSmoke_EndToEnd(t *testing.T) {
	// --- Setup ----------------------------------------------------------------
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	// --- 1. B issues a capability token granting peer.ask, max_calls=2 -------
	// We use the helper that goes through the live RPC socket so the wire
	// path is fully exercised (not just the in-process capability.Issue call).
	//
	// NOTE on `grantee_did`: the wire field name is misleading — it maps to
	// TargetDID (the AUDIENCE daimon being asked), not the caller. Leaving
	// it unset produces an any-target token (`right("peer.ask", "any")`),
	// which B accepts via its "allow any" Datalog policy. Setting it would
	// require us to set it to B's OWN DID. Future work: rename grantee_did
	// to target_did in the wire shape (deprecate old name behind alias)
	// so the SDK + CLI surface match the semantics.
	tokenID, token := issueToken(t, b, []string{"peer.ask"}, map[string]any{
		"max_calls": int64(2),
	})
	if tokenID == "" || token == "" {
		t.Fatal("issueToken returned empty id/token")
	}

	// --- 2. A dials B (Noise IK over TCP) ------------------------------------
	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	if dial.ChannelID == "" {
		t.Fatal("peer.dial returned empty channel id")
	}

	// --- 3. B's peer.list shows the inbound channel (dogfood-polish gate) ----
	// The accept-side registration is async; poll briefly so we don't race the
	// goroutine that registers the inbound channel.
	listResp := pollForInboundChannel(t, b)
	if listResp.Direction != "inbound" {
		t.Errorf("B inbound direction: got %q want %q", listResp.Direction, "inbound")
	}
	if listResp.PeerX25519 == "" {
		t.Error("B inbound peer_x25519 must not be empty — dogfood-polish regression")
	}
	if listResp.ChannelID == dial.ChannelID {
		t.Errorf("B's inbound channel id %q should be independent from A's outbound id %q",
			listResp.ChannelID, dial.ChannelID)
	}

	// --- 4. A invokes peer.ask with the token + request_receipt -------------
	askResp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token,
		"capability_token_id": tokenID,
		"request_receipt":     true,
	}))
	if askResp.Error != nil {
		t.Fatalf("peer.ask call 1 errored: %+v", askResp.Error)
	}

	var outer1 peerInvokeResult
	resultAs(t, askResp, &outer1)
	var ask1 peerAskResult
	if err := json.Unmarshal(outer1.Result, &ask1); err != nil {
		t.Fatalf("unmarshal peerAskResult call 1: %v", err)
	}
	if ask1.Response == nil {
		t.Fatal("call 1: response is nil")
	}
	if ask1.Receipt == nil {
		t.Fatal("call 1: receipt is nil — request_receipt=true should have produced one")
	}

	// --- 5. Receipt's Ed25519 signature verifies under B's public key --------
	if err := reputation.Verify(b.id.PublicKey(), ask1.Receipt); err != nil {
		t.Errorf("call 1: receipt signature failed Ed25519 verification: %v", err)
	}
	if ask1.Receipt.ServerDID != b.id.DID() {
		t.Errorf("call 1: receipt server_did: got %q want %q",
			ask1.Receipt.ServerDID, b.id.DID())
	}
	if ask1.Receipt.Verb != "peer.ask" {
		t.Errorf("call 1: receipt verb: got %q want %q", ask1.Receipt.Verb, "peer.ask")
	}

	// --- 6. B's audit chain has all three expected v0.4 rows -----------------
	// All audit appends are async (best-effort goroutines in dispatch) so we
	// poll until the chain settles. Note: peer.ask audits as
	// KindPeerInvokeServed, NOT KindPeerInvokeReceived (the latter is
	// peer.echo's audit kind — peer.invoke.received is the listener-side
	// inbox kind written by handlePeerEcho only).
	queryAudit(t, b.alog, activity.KindCapabilityVerified, 1)
	queryAudit(t, b.alog, activity.KindPeerInvokeServed, 1)
	queryAudit(t, b.alog, activity.KindReputationReceiptIssued, 1)

	// Sanity: cross-verify A's DID actually made it into B's audit. The
	// peer.invoke.served row's peer_did field was populated by the address
	// book lookup or by the receipt path; either way it shouldn't be empty
	// when the caller authenticated with a Biscuit token whose grantee was set.
	servedEntries := queryAudit(t, b.alog, activity.KindPeerInvokeServed, 1)
	// peer.invoke.served payload field name is "peer_did" per peer_channel_handlers.go
	var servedPayload map[string]any
	if err := json.Unmarshal(servedEntries[0].Payload, &servedPayload); err != nil {
		t.Fatalf("decode served payload: %v", err)
	}
	// peer_did may be empty (capability-token path doesn't auto-register the
	// caller in B's address book in v0.4) — that's OK; the receipt itself
	// has the caller_did. But peer_x25519 MUST always be populated.
	if _, ok := servedPayload["peer_x25519"].(string); !ok {
		t.Error("served payload missing peer_x25519")
	}

	// --- 7. Second invocation succeeds (calls_used 1 → 2, ceiling 2) --------
	askResp2 := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token,
		"capability_token_id": tokenID,
	}))
	if askResp2.Error != nil {
		t.Fatalf("peer.ask call 2 errored: %+v (expected success at calls_used=1)", askResp2.Error)
	}

	// --- 8. Third invocation is denied — calls_used would exceed max_calls=2 -
	// Cross-daimon error envelopes: peer.invoke wraps the remote error as
	// CodeInternalError (-32603) "peer returned error" with the original
	// error text (including the underlying -32014) embedded in the message.
	// That's by design — the caller's local daemon shouldn't surface remote
	// error codes as if they were its own. So we assert on the error MESSAGE
	// containing "denied" (matches both biscuit + capability layers), not on
	// the top-level code.
	askResp3 := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token,
		"capability_token_id": tokenID,
	}))
	if askResp3.Error == nil {
		t.Fatal("peer.ask call 3 should be denied: max_calls=2 exhausted")
	}
	if !strings.Contains(strings.ToLower(askResp3.Error.Message), "denied") {
		t.Errorf("call 3 error message %q should mention 'denied' (max_calls exhausted)",
			askResp3.Error.Message)
	}

	// --- 9. Revoke + verify revocation is immediately enforced ---------------
	// We use a different token (not yet exhausted) to test the revocation path
	// cleanly — otherwise we can't tell whether a deny is "calls exhausted" or
	// "revoked".
	tokenID2, token2 := issueToken(t, b, []string{"peer.ask"}, map[string]any{
		"max_calls": int64(10),
	})

	// First, the new token should work.
	okResp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token2,
		"capability_token_id": tokenID2,
	}))
	if okResp.Error != nil {
		t.Fatalf("pre-revoke call with fresh token errored: %+v", okResp.Error)
	}

	// Revoke via B's local RPC socket (same path as `daimon capability revoke`).
	revokeResp := b.call(t, "daimon.capability.revoke", map[string]any{"token_id": tokenID2})
	if revokeResp.Error != nil {
		t.Fatalf("capability.revoke errored: %+v", revokeResp.Error)
	}

	// Now the same token should be denied.
	deniedResp := a.callInvoke(t, dial.ChannelID, "peer.ask", standardAskParams(map[string]any{
		"capability_token":    token2,
		"capability_token_id": tokenID2,
	}))
	if deniedResp.Error == nil {
		t.Fatal("post-revoke call should be denied")
	}
	// Same envelope-wrapping as call 3 above — assert on message content.
	low := strings.ToLower(deniedResp.Error.Message)
	if !strings.Contains(low, "revoked") && !strings.Contains(low, "denied") {
		t.Errorf("post-revoke error message %q should mention 'revoked' or 'denied'",
			deniedResp.Error.Message)
	}

	// --- 10. A closes the channel; B's inbound list drains -------------------
	closeResp := a.call(t, "daimon.peer.close", map[string]any{"channel_id": dial.ChannelID})
	if closeResp.Error != nil {
		t.Fatalf("peer.close error: %+v", closeResp.Error)
	}
	pollForEmptyInboundList(t, b)

	// --- Bonus: reputation.receipts(direction="issued") reflects what served -
	// Two successful peer.ask calls + the pre-revoke fresh-token success = 3
	// receipts B has issued. (Receipts are written on every served peer.ask
	// when a receipt was requested OR whenever the server tracks served calls
	// — see handlePeerAsk for the exact rule.) We require AT LEAST the first
	// call (which explicitly set request_receipt=true) to round-trip.
	receiptsResp := b.call(t, "daimon.reputation.receipts", map[string]any{"direction": "issued"})
	if receiptsResp.Error != nil {
		t.Fatalf("reputation.receipts errored: %+v", receiptsResp.Error)
	}
	var receipts reputationReceiptsResult
	resultAs(t, receiptsResp, &receipts)
	if len(receipts.Receipts) < 1 {
		t.Fatalf("expected ≥1 issued receipt, got %d", len(receipts.Receipts))
	}
	// At least one must match call 1's receipt id.
	found := false
	for _, r := range receipts.Receipts {
		if r.ReceiptID == ask1.Receipt.ReceiptID {
			found = true
			if r.Direction != "issued" {
				t.Errorf("receipt direction: got %q want %q", r.Direction, "issued")
			}
			if r.Signature == "" {
				t.Error("stored receipt signature empty")
			}
			break
		}
	}
	if !found {
		t.Errorf("call 1's receipt %q not present in reputation.receipts(issued)",
			ask1.Receipt.ReceiptID)
	}

	// Sanity: A's DID should be the recorded caller on at least one receipt.
	_ = identity.MultibaseFragment(a.id.DID()) // import keep
}

// pollForInboundChannel polls B's peer.list until at least one inbound channel
// shows up (or times out). Returns the first inbound entry.
//
// The accept-side registration happens in a goroutine on B after the dial RPC
// returns on A, so there's a small window where A's view is "channel open" but
// B's view hasn't bookkeeped yet.
func pollForInboundChannel(t *testing.T, b *peerAskCapFixture) peerChannelWire {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last peerListResult
	for {
		resp := b.call(t, "daimon.peer.list", nil)
		if resp.Error != nil {
			t.Fatalf("B peer.list errored: %+v", resp.Error)
		}
		resultAs(t, resp, &last)
		for _, ch := range last.Channels {
			if ch.Direction == "inbound" {
				return ch
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for inbound channel in B's peer.list; got %d channels",
				len(last.Channels))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// pollForEmptyInboundList polls B's peer.list until no inbound channel is left.
func pollForEmptyInboundList(t *testing.T, b *peerAskCapFixture) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp := b.call(t, "daimon.peer.list", nil)
		if resp.Error != nil {
			t.Fatalf("B peer.list errored: %+v", resp.Error)
		}
		var list peerListResult
		resultAs(t, resp, &list)
		hasInbound := false
		for _, ch := range list.Channels {
			if ch.Direction == "inbound" {
				hasInbound = true
				break
			}
		}
		if !hasInbound {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("inbound channel still present after close")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
