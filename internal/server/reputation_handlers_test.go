package server

import (
	"encoding/json"
	"testing"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/reputation"
)

// ---------------------------------------------------------------------------
// daimon.reputation.receipts RPC tests
// ---------------------------------------------------------------------------

func TestReputation_Receipts_Empty(t *testing.T) {
	f := newCapabilityFixture(t)
	var result reputationReceiptsResult
	resp := f.call(t, "daimon.reputation.receipts", map[string]any{})
	if resp.Error != nil {
		t.Fatalf("reputation.receipts error: %+v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(b, &result)
	if len(result.Receipts) != 0 {
		t.Errorf("expected empty receipts, got %d", len(result.Receipts))
	}
}

func TestReputation_Receipts_NoDB(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.reputation.receipts", map[string]any{})
	if resp.Error == nil {
		t.Fatal("expected error when no capDB configured")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("expected CodeInvalidRequest, got %d", resp.Error.Code)
	}
}

func TestReputation_Receipts_BadDirection(t *testing.T) {
	f := newCapabilityFixture(t)
	resp := f.call(t, "daimon.reputation.receipts", map[string]any{
		"direction": "sideways",
	})
	if resp.Error == nil {
		t.Fatal("expected error for invalid direction")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %d", resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// peer.ask request_receipt integration tests
// ---------------------------------------------------------------------------

// TestPeerAsk_RequestReceipt_OK verifies the end-to-end receipt flow:
//   - A dials B (B has capDB + identity + mock provider)
//   - B pins A with peer.ask
//   - A sends peer.ask with request_receipt=true
//   - B returns a signed receipt in peerAskResult
//   - The receipt verifies under B's public key
//   - B audits KindReputationReceiptIssued
func TestPeerAsk_RequestReceipt_OK(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	// Pin A in B's address book.
	aMultibase := identity.MultibaseFragment(a.id.DID())
	_ = b.abook.Add(a.id.DID(), "A", aMultibase)
	_ = b.abook.Pin(a.id.DID(), []string{"peer.ask"})
	_ = b.abook.Save()

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
		"request_receipt": true,
	})
	if resp.Error != nil {
		t.Fatalf("peer.ask error: %+v", resp.Error)
	}

	// Unwrap peerInvokeResult → peerAskResult.
	var outer peerInvokeResult
	resultAs(t, resp, &outer)

	var ask peerAskResult
	if err := json.Unmarshal(outer.Result, &ask); err != nil {
		t.Fatalf("unmarshal peerAskResult: %v", err)
	}
	if ask.Response == nil {
		t.Fatal("response is nil")
	}
	if ask.Receipt == nil {
		t.Fatal("receipt is nil — expected a signed receipt")
	}

	rc := ask.Receipt
	if rc.ReceiptID == "" {
		t.Error("receipt_id empty")
	}
	if rc.ServerDID != b.id.DID() {
		t.Errorf("server_did: got %q want %q", rc.ServerDID, b.id.DID())
	}
	if rc.Verb != "peer.ask" {
		t.Errorf("verb: got %q want %q", rc.Verb, "peer.ask")
	}
	if rc.Signature == "" {
		t.Error("signature empty")
	}

	// Verify signature under B's public key.
	bPubKey := b.id.PublicKey()
	if err := reputation.Verify(bPubKey, rc); err != nil {
		t.Errorf("receipt signature invalid: %v", err)
	}

	// B must audit receipt issuance.
	queryAudit(t, b.alog, activity.KindReputationReceiptIssued, 1)
}

// TestPeerAsk_RequestReceipt_FalseNoReceipt confirms that omitting
// request_receipt (or setting it false) returns no receipt.
func TestPeerAsk_RequestReceipt_FalseNoReceipt(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	aMultibase := identity.MultibaseFragment(a.id.DID())
	_ = b.abook.Add(a.id.DID(), "A", aMultibase)
	_ = b.abook.Pin(a.id.DID(), []string{"peer.ask"})
	_ = b.abook.Save()

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
		// request_receipt omitted
	})
	if resp.Error != nil {
		t.Fatalf("peer.ask error: %+v", resp.Error)
	}

	var outer peerInvokeResult
	resultAs(t, resp, &outer)
	var ask peerAskResult
	_ = json.Unmarshal(outer.Result, &ask)
	if ask.Receipt != nil {
		t.Errorf("expected no receipt when request_receipt=false, got one")
	}
}

// TestPeerAsk_RequestReceipt_StoredInDB verifies that issued receipts appear
// in the daimon.reputation.receipts RPC response.
func TestPeerAsk_RequestReceipt_StoredInDB(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	aMultibase := identity.MultibaseFragment(a.id.DID())
	_ = b.abook.Add(a.id.DID(), "A", aMultibase)
	_ = b.abook.Pin(a.id.DID(), []string{"peer.ask"})
	_ = b.abook.Save()

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	_ = a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
		"request_receipt": true,
	})

	// Poll for the issued receipt to appear in B's reputation.receipts list.
	// The DB write is asynchronous.
	var result reputationReceiptsResult
	queryFn := func() bool {
		resp := b.call(t, "daimon.reputation.receipts", map[string]any{"direction": "issued"})
		if resp.Error != nil {
			return false
		}
		bb, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(bb, &result)
		return len(result.Receipts) >= 1
	}

	deadline := func() bool {
		for range 200 {
			if queryFn() {
				return true
			}
		}
		return false
	}
	if !deadline() {
		t.Fatalf("timed out waiting for receipt in DB; got %d receipts", len(result.Receipts))
	}

	r := result.Receipts[0]
	if r.Direction != "issued" {
		t.Errorf("direction: got %q want %q", r.Direction, "issued")
	}
	if r.Verb != "peer.ask" {
		t.Errorf("verb: got %q want %q", r.Verb, "peer.ask")
	}
	if r.Signature == "" {
		t.Error("signature empty in DB record")
	}
}

// TestPeerAsk_RequestReceipt_CapabilityTokenPath verifies receipts also work
// when authorization came from a Biscuit token rather than address-book pin.
func TestPeerAsk_RequestReceipt_CapabilityTokenPath(t *testing.T) {
	a := newPeerFixture(t)
	b := newPeerAskCapFixture(t)

	// A is NOT in B's address book.
	tokenID, token := issueToken(t, b, []string{"peer.ask"}, nil)

	dial := a.callDial(t, b.id.DID(), b.peerAddr)
	resp := a.callInvoke(t, dial.ChannelID, "peer.ask", map[string]any{
		"provider": "mock",
		"request": map[string]any{
			"model":    "mock-1",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		},
		"capability_token":    token,
		"capability_token_id": tokenID,
		"request_receipt":     true,
	})
	if resp.Error != nil {
		t.Fatalf("peer.ask error: %+v", resp.Error)
	}

	var outer peerInvokeResult
	resultAs(t, resp, &outer)
	var ask peerAskResult
	_ = json.Unmarshal(outer.Result, &ask)

	if ask.Receipt == nil {
		t.Fatal("expected receipt on capability-token path, got nil")
	}
	if err := reputation.Verify(b.id.PublicKey(), ask.Receipt); err != nil {
		t.Errorf("receipt signature invalid: %v", err)
	}
}
