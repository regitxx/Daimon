package capability_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/capability"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

// ---------------------------------------------------------------------------
// Issue
// ---------------------------------------------------------------------------

func TestIssue_RequiresVerbs(t *testing.T) {
	_, priv := newKey(t)
	_, err := capability.Issue(priv, capability.IssueOptions{})
	if err == nil {
		t.Fatal("expected error for empty Verbs, got nil")
	}
}

func TestIssue_BasicToken(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:     []string{"peer.ask"},
		TargetDID: "did:key:z6MkAlice",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("Issue returned empty token")
	}

	// Token must verify against the same public key.
	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "did:key:z6MkAlice",
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatalf("Verify after Issue: %v", err)
	}
}

func TestIssue_AnyTarget(t *testing.T) {
	pub, priv := newKey(t)
	// Empty TargetDID → right("peer.ask", "any")
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs: []string{"peer.ask"},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Should verify against any target DID.
	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "did:key:z6MkBob",
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatalf("Verify with any target: %v", err)
	}
}

func TestIssue_WrongVerb(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs: []string{"peer.ask"},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.echo", // not granted
		TargetDID: "did:key:z6MkBob",
		Now:       time.Now(),
	})
	if err == nil {
		t.Fatal("expected denial for wrong verb, got nil")
	}
}

func TestIssue_WrongKey(t *testing.T) {
	_, priv := newKey(t)
	pub2, _ := newKey(t) // different public key

	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs: []string{"peer.ask"},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub2, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "did:key:z6MkBob",
		Now:       time.Now(),
	})
	if err == nil {
		t.Fatal("expected denial for wrong public key, got nil")
	}
}

// ---------------------------------------------------------------------------
// ValidUntil (expiry check)
// ---------------------------------------------------------------------------

func TestIssue_NotExpired(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:      []string{"peer.ask"},
		ValidUntil: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatalf("should be valid (not expired): %v", err)
	}
}

func TestIssue_Expired(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:      []string{"peer.ask"},
		ValidUntil: time.Now().Add(-time.Second), // already expired
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
	})
	if err == nil {
		t.Fatal("expected denial for expired token, got nil")
	}
}

// ---------------------------------------------------------------------------
// Model constraint
// ---------------------------------------------------------------------------

func TestIssue_ModelConstraint_Pass(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:           []string{"peer.ask"},
		ModelConstraint: "claude-haiku-4-5",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
		Model:     "claude-haiku-4-5",
	})
	if err != nil {
		t.Fatalf("model matches constraint: %v", err)
	}
}

func TestIssue_ModelConstraint_Fail(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:           []string{"peer.ask"},
		ModelConstraint: "claude-haiku-4-5",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
		Model:     "claude-opus-4-5", // wrong model
	})
	if err == nil {
		t.Fatal("expected denial for disallowed model, got nil")
	}
}

// ---------------------------------------------------------------------------
// Call-count ceiling
// ---------------------------------------------------------------------------

func TestIssue_CallsUsed_UnderCeiling(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:    []string{"peer.ask"},
		MaxCalls: 10,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
		CallsUsed: 5, // under ceiling
	})
	if err != nil {
		t.Fatalf("calls_used under ceiling should pass: %v", err)
	}
}

func TestIssue_CallsUsed_AtCeiling(t *testing.T) {
	pub, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:    []string{"peer.ask"},
		MaxCalls: 10,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	err = capability.Verify(raw, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
		CallsUsed: 10, // at ceiling (check is $n < MaxCalls, so 10 < 10 == false)
	})
	if err == nil {
		t.Fatal("expected denial at ceiling, got nil")
	}
}

// ---------------------------------------------------------------------------
// Attenuate
// ---------------------------------------------------------------------------

func TestAttenuate_TightensExpiry(t *testing.T) {
	pub, priv := newKey(t)

	// Issue with long expiry.
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:      []string{"peer.ask"},
		ValidUntil: time.Now().Add(30 * 24 * time.Hour), // 30 days
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Attenuate to today only.
	tighter := time.Now().Add(1 * time.Hour)
	raw2, err := capability.Attenuate(raw, capability.AttenuateOptions{
		ValidUntil: &tighter,
	})
	if err != nil {
		t.Fatalf("Attenuate: %v", err)
	}
	if len(raw2) <= len(raw) {
		// Attenuated token should be strictly larger (extra block appended).
		t.Fatalf("attenuated token not larger: %d vs %d", len(raw2), len(raw))
	}

	// Should still verify within the tighter window.
	err = capability.Verify(raw2, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatalf("attenuated token should still verify within window: %v", err)
	}
}

func TestAttenuate_TightenedExpiryPreventsLateUse(t *testing.T) {
	pub, priv := newKey(t)

	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs:      []string{"peer.ask"},
		ValidUntil: time.Now().Add(30 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Attenuate to 2 seconds in the past.
	past := time.Now().Add(-2 * time.Second)
	raw2, err := capability.Attenuate(raw, capability.AttenuateOptions{
		ValidUntil: &past,
	})
	if err != nil {
		t.Fatalf("Attenuate: %v", err)
	}

	// Should be denied because the tighter expiry has passed.
	err = capability.Verify(raw2, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
	})
	if err == nil {
		t.Fatal("expected denial after attenuated expiry, got nil")
	}
}

func TestAttenuate_NoOp(t *testing.T) {
	pub, priv := newKey(t)

	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs: []string{"peer.ask"},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Attenuate with no options — should return same bytes.
	raw2, err := capability.Attenuate(raw, capability.AttenuateOptions{})
	if err != nil {
		t.Fatalf("Attenuate (no-op): %v", err)
	}

	err = capability.Verify(raw2, pub, capability.VerifyContext{
		Verb:      "peer.ask",
		TargetDID: "any",
		Now:       time.Now(),
	})
	if err != nil {
		t.Fatalf("Verify after no-op attenuate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Encode / Decode round-trip
// ---------------------------------------------------------------------------

func TestEncodeDecode_RoundTrip(t *testing.T) {
	_, priv := newKey(t)
	raw, err := capability.Issue(priv, capability.IssueOptions{
		Verbs: []string{"peer.ask"},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	enc := capability.Encode(raw)
	dec, err := capability.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(dec) != string(raw) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(dec), len(raw))
	}
}
