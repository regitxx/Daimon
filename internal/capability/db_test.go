package capability_test

import (
	"context"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/capability"
)

// openTestDB opens an in-memory DB for testing.
func openTestDB(t *testing.T) *capability.DB {
	t.Helper()
	db, err := capability.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---------------------------------------------------------------------------
// Issued tokens
// ---------------------------------------------------------------------------

func TestDB_IssuedTokens_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	tok := capability.IssuedToken{
		TokenID:         "token-001",
		Verbs:           []string{"peer.ask"},
		GranteeDID:      "did:key:z6MkBob",
		TargetDID:       "did:key:z6MkAlice",
		ValidUntil:      time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second),
		MaxCalls:        50,
		ModelConstraint: "claude-haiku-4-5",
		TokenBytes:      []byte("fake-biscuit-bytes"),
		IssuedAt:        time.Now().UTC().Truncate(time.Second),
	}

	if err := db.RecordIssued(ctx, tok); err != nil {
		t.Fatalf("RecordIssued: %v", err)
	}

	got, err := db.LookupIssued(ctx, "token-001")
	if err != nil {
		t.Fatalf("LookupIssued: %v", err)
	}

	if got.TokenID != tok.TokenID {
		t.Errorf("TokenID: got %q want %q", got.TokenID, tok.TokenID)
	}
	if len(got.Verbs) != 1 || got.Verbs[0] != "peer.ask" {
		t.Errorf("Verbs: got %v want [peer.ask]", got.Verbs)
	}
	if got.GranteeDID != tok.GranteeDID {
		t.Errorf("GranteeDID: got %q want %q", got.GranteeDID, tok.GranteeDID)
	}
	if got.TargetDID != tok.TargetDID {
		t.Errorf("TargetDID: got %q want %q", got.TargetDID, tok.TargetDID)
	}
	if !got.ValidUntil.Equal(tok.ValidUntil) {
		t.Errorf("ValidUntil: got %v want %v", got.ValidUntil, tok.ValidUntil)
	}
	if got.MaxCalls != 50 {
		t.Errorf("MaxCalls: got %d want 50", got.MaxCalls)
	}
	if got.ModelConstraint != "claude-haiku-4-5" {
		t.Errorf("ModelConstraint: got %q", got.ModelConstraint)
	}
	if string(got.TokenBytes) != "fake-biscuit-bytes" {
		t.Errorf("TokenBytes mismatch")
	}
	if got.Revoked {
		t.Errorf("Revoked should be false")
	}
}

func TestDB_IssuedTokens_NoExpiry(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	tok := capability.IssuedToken{
		TokenID:    "token-noexp",
		Verbs:      []string{"peer.ask"},
		TargetDID:  "any",
		TokenBytes: []byte("x"),
		IssuedAt:   time.Now().UTC(),
	}
	if err := db.RecordIssued(ctx, tok); err != nil {
		t.Fatalf("RecordIssued: %v", err)
	}
	got, err := db.LookupIssued(ctx, "token-noexp")
	if err != nil {
		t.Fatalf("LookupIssued: %v", err)
	}
	if !got.ValidUntil.IsZero() {
		t.Errorf("ValidUntil should be zero for no-expiry token, got %v", got.ValidUntil)
	}
}

func TestDB_ListIssued_FilterRevoked(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"tk-a", "tk-b", "tk-c"} {
		if err := db.RecordIssued(ctx, capability.IssuedToken{
			TokenID:    id,
			Verbs:      []string{"peer.ask"},
			TargetDID:  "any",
			TokenBytes: []byte("x"),
			IssuedAt:   time.Now().UTC(),
		}); err != nil {
			t.Fatalf("RecordIssued %s: %v", id, err)
		}
	}

	if err := db.RevokeToken(ctx, "tk-b"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	active, err := db.ListIssued(ctx, false)
	if err != nil {
		t.Fatalf("ListIssued(false): %v", err)
	}
	if len(active) != 2 {
		t.Errorf("active count: got %d want 2", len(active))
	}

	all, err := db.ListIssued(ctx, true)
	if err != nil {
		t.Fatalf("ListIssued(true): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("all count: got %d want 3", len(all))
	}
	for _, tok := range all {
		if tok.TokenID == "tk-b" && !tok.Revoked {
			t.Errorf("tk-b should be revoked")
		}
		if tok.TokenID != "tk-b" && tok.Revoked {
			t.Errorf("%s should not be revoked", tok.TokenID)
		}
	}
}

func TestDB_RevokeToken_Idempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := db.RecordIssued(ctx, capability.IssuedToken{
		TokenID:    "idem",
		Verbs:      []string{"peer.ask"},
		TargetDID:  "any",
		TokenBytes: []byte("x"),
		IssuedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordIssued: %v", err)
	}

	if err := db.RevokeToken(ctx, "idem"); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := db.RevokeToken(ctx, "idem"); err != nil {
		t.Fatalf("second revoke (idempotent) returned error: %v", err)
	}
}

func TestDB_RevokeToken_NotFound(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	err := db.RevokeToken(ctx, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown token_id, got nil")
	}
}

func TestDB_IsRevoked(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := db.RecordIssued(ctx, capability.IssuedToken{
		TokenID:    "chk",
		Verbs:      []string{"peer.ask"},
		TargetDID:  "any",
		TokenBytes: []byte("x"),
		IssuedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordIssued: %v", err)
	}

	rev, err := db.IsRevoked(ctx, "chk")
	if err != nil || rev {
		t.Errorf("before revoke: IsRevoked=%v err=%v", rev, err)
	}

	if err := db.RevokeToken(ctx, "chk"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	rev, err = db.IsRevoked(ctx, "chk")
	if err != nil || !rev {
		t.Errorf("after revoke: IsRevoked=%v err=%v", rev, err)
	}

	// Unknown token_id → not revoked, no error.
	rev, err = db.IsRevoked(ctx, "ghost")
	if err != nil || rev {
		t.Errorf("unknown token: IsRevoked=%v err=%v", rev, err)
	}
}

// ---------------------------------------------------------------------------
// Received tokens
// ---------------------------------------------------------------------------

func TestDB_ReceivedTokens_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	recv := capability.ReceivedToken{
		TokenID:    "rt-001",
		IssuerDID:  "did:key:z6MkAlice",
		Verbs:      []string{"peer.ask"},
		TokenBytes: []byte("biscuit-raw"),
		ReceivedAt: time.Now().UTC().Truncate(time.Second),
		ValidUntil: time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second),
	}

	if err := db.RecordReceived(ctx, recv); err != nil {
		t.Fatalf("RecordReceived: %v", err)
	}

	list, err := db.ListReceived(ctx)
	if err != nil {
		t.Fatalf("ListReceived: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 received token, got %d", len(list))
	}
	got := list[0]
	if got.TokenID != recv.TokenID {
		t.Errorf("TokenID: got %q", got.TokenID)
	}
	if got.IssuerDID != recv.IssuerDID {
		t.Errorf("IssuerDID: got %q", got.IssuerDID)
	}
	if !got.ValidUntil.Equal(recv.ValidUntil) {
		t.Errorf("ValidUntil: got %v want %v", got.ValidUntil, recv.ValidUntil)
	}
}

func TestDB_RecordReceived_Upsert(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	base := capability.ReceivedToken{
		TokenID:    "rt-upsert",
		IssuerDID:  "did:key:z6MkAlice",
		Verbs:      []string{"peer.ask"},
		TokenBytes: []byte("v1"),
		ReceivedAt: time.Now().UTC(),
	}
	if err := db.RecordReceived(ctx, base); err != nil {
		t.Fatalf("first RecordReceived: %v", err)
	}

	// Update with new bytes — same token_id.
	base.TokenBytes = []byte("v2")
	if err := db.RecordReceived(ctx, base); err != nil {
		t.Fatalf("second RecordReceived (upsert): %v", err)
	}

	list, err := db.ListReceived(ctx)
	if err != nil {
		t.Fatalf("ListReceived: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 row after upsert, got %d", len(list))
	}
	if string(list[0].TokenBytes) != "v2" {
		t.Errorf("expected v2 bytes after upsert")
	}
}

// ---------------------------------------------------------------------------
// Receipts
// ---------------------------------------------------------------------------

func TestDB_Receipts_IssuedAndReceived(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	issued := capability.Receipt{
		ReceiptID:    "rcpt-001",
		Direction:    capability.ReceiptIssued,
		ServedAt:     time.Now().UTC().Truncate(time.Second),
		ServedVerb:   "peer.ask",
		CallerDID:    "did:key:z6MkBob",
		ServerDID:    "did:key:z6MkAlice",
		Provider:     "claude",
		Model:        "claude-opus-4-5",
		InputTokens:  14,
		OutputTokens: 23,
		DurationMS:   1240,
		Signature:    []byte("sig-bytes"),
	}
	received := capability.Receipt{
		ReceiptID:    "rcpt-002",
		Direction:    capability.ReceiptReceived,
		ServedAt:     time.Now().UTC().Truncate(time.Second),
		ServedVerb:   "peer.ask",
		CallerDID:    "did:key:z6MkMe",
		ServerDID:    "did:key:z6MkCarol",
		InputTokens:  8,
		OutputTokens: 12,
		DurationMS:   800,
	}

	for _, r := range []capability.Receipt{issued, received} {
		if err := db.RecordReceipt(ctx, r); err != nil {
			t.Fatalf("RecordReceipt %s: %v", r.ReceiptID, err)
		}
	}

	// Filter: issued only.
	issuedList, err := db.ListReceipts(ctx, capability.ReceiptIssued)
	if err != nil {
		t.Fatalf("ListReceipts issued: %v", err)
	}
	if len(issuedList) != 1 || issuedList[0].ReceiptID != "rcpt-001" {
		t.Errorf("issued list wrong: %+v", issuedList)
	}
	if string(issuedList[0].Signature) != "sig-bytes" {
		t.Errorf("Signature not persisted")
	}

	// Filter: received only.
	receivedList, err := db.ListReceipts(ctx, capability.ReceiptReceived)
	if err != nil {
		t.Fatalf("ListReceipts received: %v", err)
	}
	if len(receivedList) != 1 || receivedList[0].ReceiptID != "rcpt-002" {
		t.Errorf("received list wrong: %+v", receivedList)
	}

	// No filter: both.
	all, err := db.ListReceipts(ctx, "")
	if err != nil {
		t.Fatalf("ListReceipts all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("all receipts: got %d want 2", len(all))
	}
}

func TestDB_Receipts_Upsert(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	r := capability.Receipt{
		ReceiptID:  "rcpt-up",
		Direction:  capability.ReceiptIssued,
		ServedAt:   time.Now().UTC(),
		ServedVerb: "peer.ask",
		CallerDID:  "did:key:z6MkA",
		ServerDID:  "did:key:z6MkB",
	}
	if err := db.RecordReceipt(ctx, r); err != nil {
		t.Fatalf("RecordReceipt: %v", err)
	}
	r.InputTokens = 99
	if err := db.RecordReceipt(ctx, r); err != nil {
		t.Fatalf("RecordReceipt upsert: %v", err)
	}
	all, _ := db.ListReceipts(ctx, "")
	if len(all) != 1 || all[0].InputTokens != 99 {
		t.Errorf("upsert did not update: %+v", all)
	}
}

// ---------------------------------------------------------------------------
// Call-count tracking
// ---------------------------------------------------------------------------

func TestDB_CallCounts_Increment(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	n, err := db.IncrementCalls(ctx, "tk-calls")
	if err != nil {
		t.Fatalf("IncrementCalls: %v", err)
	}
	if n != 1 {
		t.Errorf("first increment: got %d want 1", n)
	}

	n, err = db.IncrementCalls(ctx, "tk-calls")
	if err != nil {
		t.Fatalf("IncrementCalls #2: %v", err)
	}
	if n != 2 {
		t.Errorf("second increment: got %d want 2", n)
	}

	n, err = db.CallsUsed(ctx, "tk-calls")
	if err != nil {
		t.Fatalf("CallsUsed: %v", err)
	}
	if n != 2 {
		t.Errorf("CallsUsed: got %d want 2", n)
	}
}

func TestDB_CallsUsed_Unknown(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	n, err := db.CallsUsed(ctx, "ghost")
	if err != nil || n != 0 {
		t.Errorf("unknown token: n=%d err=%v", n, err)
	}
}

func TestDB_CallCounts_MultipleTokens(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := db.IncrementCalls(ctx, "tk-a"); err != nil {
			t.Fatalf("IncrementCalls tk-a: %v", err)
		}
	}
	for i := 0; i < 7; i++ {
		if _, err := db.IncrementCalls(ctx, "tk-b"); err != nil {
			t.Fatalf("IncrementCalls tk-b: %v", err)
		}
	}

	a, _ := db.CallsUsed(ctx, "tk-a")
	b, _ := db.CallsUsed(ctx, "tk-b")
	if a != 3 {
		t.Errorf("tk-a: got %d want 3", a)
	}
	if b != 7 {
		t.Errorf("tk-b: got %d want 7", b)
	}
}

// ---------------------------------------------------------------------------
// Verify + DB integration: calls_used Datalog enforcement
// ---------------------------------------------------------------------------

// TestVerify_WithCallsUsedFromDB demonstrates the full loop:
// Issue a token with MaxCalls=3, use DB.IncrementCalls() three times,
// then verify that the fourth call is denied because calls_used(3) >= 3.
func TestVerify_WithCallsUsedFromDB(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	pub, priv := newKey(t)

	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:    []string{"peer.ask"},
		MaxCalls: 3,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	tokenID := "integration-token"

	// First three calls pass.
	for i := 0; i < 3; i++ {
		used, err := db.IncrementCalls(ctx, tokenID)
		if err != nil {
			t.Fatalf("IncrementCalls #%d: %v", i+1, err)
		}
		// calls_used should be < 3 for calls 1 and 2; at call 3 it equals 3 and should fail.
		vctx := capability.VerifyContext{
			Verb:      "peer.ask",
			TargetDID: "any",
			Now:       time.Now(),
			CallsUsed: used,
		}
		err = capability.Verify(raw, pub, vctx)
		if i < 2 && err != nil {
			t.Errorf("call %d should pass: %v", i+1, err)
		}
		if i == 2 && err == nil {
			t.Errorf("call 3 (at ceiling) should be denied")
		}
	}
}
