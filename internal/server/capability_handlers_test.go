package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/capability"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
)

// newCapabilityFixture opens a Server with a capability.DB threaded in.
func newCapabilityFixture(t *testing.T) *fixture {
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

	capDB, err := capability.OpenDB(filepath.Join(dir, "capability.db"))
	if err != nil {
		t.Fatalf("capability.OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = capDB.Close() })

	srv, err := New(Options{Identity: id, Store: store, Log: alog, CapabilityDB: capDB})
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
	t.Cleanup(func() { cancel(); _ = srv.Close() })
	go func() { _ = srv.Serve(ctx) }()
	waitForSocket(t, sockPath)

	return &fixture{t: t, id: id, mem: store, alog: alog, srv: srv, addr: sockPath}
}

// rpcCapability sends a daimon.capability.* RPC and unmarshals the result
// into out (pass nil to ignore).  Fails the test on any RPC error.
func rpcCapability(f *fixture, method string, params, out any) {
	f.t.Helper()
	resp := f.call(f.t, method, params)
	if resp.Error != nil {
		f.t.Fatalf("%s returned error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	if out == nil {
		return
	}
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, out); err != nil {
		f.t.Fatalf("unmarshal result: %v", err)
	}
}

// rpcCapabilityErr calls a capability verb and expects an error, returning the RPCError.
func rpcCapabilityErr(f *fixture, method string, params any) *RPCError {
	f.t.Helper()
	resp := f.call(f.t, method, params)
	if resp.Error == nil {
		f.t.Fatalf("%s expected error, got nil (result=%v)", method, resp.Result)
	}
	return resp.Error
}

// ---------------------------------------------------------------------------
// daimon.capability.issue
// ---------------------------------------------------------------------------

func TestCapability_Issue_Basic(t *testing.T) {
	f := newCapabilityFixture(t)

	var result capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{
		"verbs": []string{"peer.ask"},
	}, &result)

	if result.TokenID == "" {
		t.Error("token_id is empty")
	}
	if result.Token == "" {
		t.Error("token is empty")
	}
	if result.ExpiresAt != "" {
		t.Errorf("no expiry requested but ExpiresAt=%q", result.ExpiresAt)
	}

	// Token must be valid base64url.
	raw, err := capability.Decode(result.Token)
	if err != nil {
		t.Fatalf("Decode token: %v", err)
	}
	if len(raw) == 0 {
		t.Error("decoded token is empty")
	}
}

func TestCapability_Issue_WithExpiry(t *testing.T) {
	f := newCapabilityFixture(t)

	validUntil := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	var result capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{
		"verbs":       []string{"peer.ask"},
		"valid_until": validUntil,
	}, &result)

	if result.ExpiresAt != validUntil {
		t.Errorf("ExpiresAt: got %q want %q", result.ExpiresAt, validUntil)
	}
}

func TestCapability_Issue_WithGranteeDID(t *testing.T) {
	f := newCapabilityFixture(t)

	var result capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{
		"verbs":       []string{"peer.ask"},
		"grantee_did": "did:key:z6MkBob",
	}, &result)

	if result.TokenID == "" {
		t.Error("token_id empty")
	}
}

func TestCapability_Issue_WithMaxCallsAndModel(t *testing.T) {
	f := newCapabilityFixture(t)

	var result capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{
		"verbs":            []string{"peer.ask"},
		"max_calls":        50,
		"model_constraint": "claude-haiku-4-5",
	}, &result)

	if result.Token == "" {
		t.Error("token empty")
	}
}

func TestCapability_Issue_MissingVerbs(t *testing.T) {
	f := newCapabilityFixture(t)
	rpcErr := rpcCapabilityErr(f, "daimon.capability.issue", map[string]any{})
	if rpcErr.Code != CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %d", rpcErr.Code)
	}
}

func TestCapability_Issue_BadValidUntil(t *testing.T) {
	f := newCapabilityFixture(t)
	rpcErr := rpcCapabilityErr(f, "daimon.capability.issue", map[string]any{
		"verbs":       []string{"peer.ask"},
		"valid_until": "not-a-timestamp",
	})
	if rpcErr.Code != CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %d", rpcErr.Code)
	}
}

func TestCapability_Issue_NoCapDB(t *testing.T) {
	// Server without a CapabilityDB.
	f := newFixture(t) // standard fixture, no capDB
	rpcErr := rpcCapabilityErr(f, "daimon.capability.issue", map[string]any{
		"verbs": []string{"peer.ask"},
	})
	if rpcErr.Code != CodeInvalidRequest {
		t.Errorf("expected CodeInvalidRequest (no capDB), got %d", rpcErr.Code)
	}
}

// ---------------------------------------------------------------------------
// daimon.capability.list
// ---------------------------------------------------------------------------

func TestCapability_List_Empty(t *testing.T) {
	f := newCapabilityFixture(t)

	var result capabilityListResult
	rpcCapability(f, "daimon.capability.list", map[string]any{}, &result)

	if len(result.Tokens) != 0 {
		t.Errorf("expected empty list, got %d", len(result.Tokens))
	}
}

func TestCapability_List_ShowsIssued(t *testing.T) {
	f := newCapabilityFixture(t)

	for range 3 {
		rpcCapability(f, "daimon.capability.issue", map[string]any{
			"verbs": []string{"peer.ask"},
		}, nil)
	}

	var result capabilityListResult
	rpcCapability(f, "daimon.capability.list", map[string]any{}, &result)
	if len(result.Tokens) != 3 {
		t.Errorf("expected 3 tokens, got %d", len(result.Tokens))
	}
	for _, tok := range result.Tokens {
		if tok.TokenID == "" {
			t.Error("token_id empty in list result")
		}
		if tok.Revoked {
			t.Error("token should not be revoked")
		}
	}
}

func TestCapability_List_ExcludesRevokedByDefault(t *testing.T) {
	f := newCapabilityFixture(t)

	// Issue two tokens.
	var r1, r2 capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{"verbs": []string{"peer.ask"}}, &r1)
	rpcCapability(f, "daimon.capability.issue", map[string]any{"verbs": []string{"peer.ask"}}, &r2)

	// Revoke one.
	rpcCapability(f, "daimon.capability.revoke", map[string]any{"token_id": r1.TokenID}, nil)

	// Default list: only active.
	var active capabilityListResult
	rpcCapability(f, "daimon.capability.list", map[string]any{}, &active)
	if len(active.Tokens) != 1 {
		t.Errorf("default list: got %d want 1", len(active.Tokens))
	}

	// include_revoked=true: both.
	var all capabilityListResult
	rpcCapability(f, "daimon.capability.list", map[string]any{"include_revoked": true}, &all)
	if len(all.Tokens) != 2 {
		t.Errorf("include_revoked list: got %d want 2", len(all.Tokens))
	}
}

// ---------------------------------------------------------------------------
// daimon.capability.revoke
// ---------------------------------------------------------------------------

func TestCapability_Revoke_OK(t *testing.T) {
	f := newCapabilityFixture(t)

	var issued capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{"verbs": []string{"peer.ask"}}, &issued)

	// Revoke it.
	rpcCapability(f, "daimon.capability.revoke", map[string]any{"token_id": issued.TokenID}, nil)

	// Confirm it appears revoked in the list.
	var all capabilityListResult
	rpcCapability(f, "daimon.capability.list", map[string]any{"include_revoked": true}, &all)
	found := false
	for _, tok := range all.Tokens {
		if tok.TokenID == issued.TokenID {
			found = true
			if !tok.Revoked {
				t.Error("token should be revoked in list")
			}
			if tok.RevokedAt == "" {
				t.Error("revoked_at should be set")
			}
		}
	}
	if !found {
		t.Error("revoked token not in include_revoked list")
	}
}

func TestCapability_Revoke_NotFound(t *testing.T) {
	f := newCapabilityFixture(t)
	rpcErr := rpcCapabilityErr(f, "daimon.capability.revoke", map[string]any{
		"token_id": "ghost-token",
	})
	if rpcErr.Code != CodeNotFound {
		t.Errorf("expected CodeNotFound, got %d", rpcErr.Code)
	}
}

func TestCapability_Revoke_MissingTokenID(t *testing.T) {
	f := newCapabilityFixture(t)
	rpcErr := rpcCapabilityErr(f, "daimon.capability.revoke", map[string]any{})
	if rpcErr.Code != CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %d", rpcErr.Code)
	}
}

// ---------------------------------------------------------------------------
// daimon.capability.attenuate
// ---------------------------------------------------------------------------

func TestCapability_Attenuate_TightensExpiry(t *testing.T) {
	f := newCapabilityFixture(t)

	// Issue with long expiry.
	var issued capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{
		"verbs":       []string{"peer.ask"},
		"valid_until": time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339),
	}, &issued)

	// Attenuate to 1 hour.
	tighter := time.Now().Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	var att capabilityAttenuateResult
	rpcCapability(f, "daimon.capability.attenuate", map[string]any{
		"token":       issued.Token,
		"valid_until": tighter,
	}, &att)

	if att.Token == "" {
		t.Error("attenuated token is empty")
	}
	// Attenuated token should be larger than the original.
	orig, _ := capability.Decode(issued.Token)
	atten, _ := capability.Decode(att.Token)
	if len(atten) <= len(orig) {
		t.Errorf("attenuated token should be larger: orig=%d atten=%d", len(orig), len(atten))
	}
}

func TestCapability_Attenuate_MissingToken(t *testing.T) {
	f := newCapabilityFixture(t)
	rpcErr := rpcCapabilityErr(f, "daimon.capability.attenuate", map[string]any{
		"valid_until": time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	if rpcErr.Code != CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %d", rpcErr.Code)
	}
}

func TestCapability_Attenuate_InvalidToken(t *testing.T) {
	f := newCapabilityFixture(t)
	rpcErr := rpcCapabilityErr(f, "daimon.capability.attenuate", map[string]any{
		"token": "this-is-not-a-biscuit",
	})
	// Decode will fail → CodeInvalidParams; Unmarshal fail → CodeInternalError.
	// Either indicates the bad input was rejected.
	if rpcErr.Code == 0 {
		t.Error("expected error for invalid token")
	}
}

func TestCapability_Attenuate_NoOp(t *testing.T) {
	f := newCapabilityFixture(t)

	var issued capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{
		"verbs": []string{"peer.ask"},
	}, &issued)

	// Attenuate with no options — should return valid token (same bytes).
	var att capabilityAttenuateResult
	rpcCapability(f, "daimon.capability.attenuate", map[string]any{
		"token": issued.Token,
	}, &att)

	if att.Token == "" {
		t.Error("attenuated token empty")
	}
}

// ---------------------------------------------------------------------------
// Audit log integration
// ---------------------------------------------------------------------------

func TestCapability_Audit_IssueAndRevoke(t *testing.T) {
	f := newCapabilityFixture(t)

	var issued capabilityIssueResult
	rpcCapability(f, "daimon.capability.issue", map[string]any{
		"verbs": []string{"peer.ask"},
	}, &issued)

	rpcCapability(f, "daimon.capability.revoke", map[string]any{
		"token_id": issued.TokenID,
	}, nil)

	// Poll for both audit entries.
	queryAudit(t, f.alog, activity.KindCapabilityIssued, 1)
	queryAudit(t, f.alog, activity.KindCapabilityRevoked, 1)
}
