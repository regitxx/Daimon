package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/capability"
)

// daimon.capability.* RPC handlers (v0.4 phase 42).
//
// All four verbs require a capDB to be attached to the server.
// If s.capDB is nil (daemon started without a capability.db path),
// every handler returns capabilityDBNotReady() — same pattern as
// walletNotReady() / addressBookNotReady().
//
// These verbs operate on tokens THIS daimon issues (the "issuer" role).
// The "verifier" role (serving daimon checking an inbound capability_token)
// is wired into peer.ask in phase 43.

func capabilityDBNotReady() *RPCError {
	return newError(CodeInvalidRequest, "capability store not loaded; check daemon logs for open failures")
}

// ---------------------------------------------------------------------------
// daimon.capability.issue
// ---------------------------------------------------------------------------

type capabilityIssueParams struct {
	Verbs           []string `json:"verbs"`
	GranteeDID      string   `json:"grantee_did,omitempty"`
	ValidUntil      string   `json:"valid_until,omitempty"` // RFC3339
	MaxCalls        int64    `json:"max_calls,omitempty"`
	ModelConstraint string   `json:"model_constraint,omitempty"`
}

type capabilityIssueResult struct {
	TokenID   string `json:"token_id"`
	Token     string `json:"token"`      // base64url-encoded Biscuit token
	ExpiresAt string `json:"expires_at"` // RFC3339; empty = no expiry
}

func (s *Server) handleCapabilityIssue(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.capDB == nil {
		return nil, capabilityDBNotReady()
	}
	if s.id == nil {
		return nil, newError(CodeIdentityLocked, "identity is locked")
	}

	var p capabilityIssueParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if len(p.Verbs) == 0 {
		return nil, newError(CodeInvalidParams, "verbs is required and must be non-empty")
	}

	// GranteeDID is stored for audit/list; TargetDID is what gets embedded in
	// the Biscuit token as right("verb", targetDID).  If no grantee is
	// specified, "any" is used so the token works against any serving daimon.
	targetDID := p.GranteeDID
	if targetDID == "" {
		targetDID = "any"
	}

	opts := capability.IssueOptions{
		Verbs:           p.Verbs,
		TargetDID:       targetDID,
		MaxCalls:        p.MaxCalls,
		ModelConstraint: p.ModelConstraint,
	}

	if p.ValidUntil != "" {
		t, err := time.Parse(time.RFC3339, p.ValidUntil)
		if err != nil {
			return nil, newError(CodeInvalidParams, fmt.Sprintf("valid_until: invalid RFC3339 timestamp: %v", err))
		}
		opts.ValidUntil = t
	}

	raw, err := capability.Issue(s.id.PrivateKey(), opts)
	if err != nil {
		return nil, newError(CodeInternalError, fmt.Sprintf("capability.issue: %v", err))
	}

	tokenID := ulid.Make().String()
	now := time.Now().UTC()

	record := capability.IssuedToken{
		TokenID:         tokenID,
		Verbs:           p.Verbs,
		GranteeDID:      p.GranteeDID,
		TargetDID:       targetDID,
		ValidUntil:      opts.ValidUntil,
		MaxCalls:        p.MaxCalls,
		ModelConstraint: p.ModelConstraint,
		TokenBytes:      raw,
		IssuedAt:        now,
	}
	if err := s.capDB.RecordIssued(ctx, record); err != nil {
		return nil, newError(CodeInternalError, fmt.Sprintf("capability.issue: persist: %v", err))
	}

	// Audit.
	s.auditCapability(ctx, activity.KindCapabilityIssued, map[string]any{
		"token_id":         tokenID,
		"verbs":            p.Verbs,
		"grantee_did":      p.GranteeDID,
		"valid_until":      p.ValidUntil,
		"max_calls":        p.MaxCalls,
		"model_constraint": p.ModelConstraint,
	})

	expiresAt := ""
	if !opts.ValidUntil.IsZero() {
		expiresAt = opts.ValidUntil.UTC().Format(time.RFC3339)
	}

	return capabilityIssueResult{
		TokenID:   tokenID,
		Token:     capability.Encode(raw),
		ExpiresAt: expiresAt,
	}, nil
}

// ---------------------------------------------------------------------------
// daimon.capability.list
// ---------------------------------------------------------------------------

type capabilityListParams struct {
	IncludeRevoked bool `json:"include_revoked,omitempty"`
}

type capabilityTokenWire struct {
	TokenID         string   `json:"token_id"`
	Verbs           []string `json:"verbs"`
	GranteeDID      string   `json:"grantee_did,omitempty"`
	TargetDID       string   `json:"target_did,omitempty"`
	ValidUntil      string   `json:"valid_until,omitempty"`
	MaxCalls        int64    `json:"max_calls,omitempty"`
	ModelConstraint string   `json:"model_constraint,omitempty"`
	IssuedAt        string   `json:"issued_at"`
	Revoked         bool     `json:"revoked"`
	RevokedAt       string   `json:"revoked_at,omitempty"`
}

type capabilityListResult struct {
	Tokens []capabilityTokenWire `json:"tokens"`
}

func issuedToWire(t capability.IssuedToken) capabilityTokenWire {
	w := capabilityTokenWire{
		TokenID:         t.TokenID,
		Verbs:           t.Verbs,
		GranteeDID:      t.GranteeDID,
		TargetDID:       t.TargetDID,
		MaxCalls:        t.MaxCalls,
		ModelConstraint: t.ModelConstraint,
		IssuedAt:        t.IssuedAt.UTC().Format(time.RFC3339),
		Revoked:         t.Revoked,
	}
	if !t.ValidUntil.IsZero() {
		w.ValidUntil = t.ValidUntil.UTC().Format(time.RFC3339)
	}
	if !t.RevokedAt.IsZero() {
		w.RevokedAt = t.RevokedAt.UTC().Format(time.RFC3339)
	}
	return w
}

func (s *Server) handleCapabilityList(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.capDB == nil {
		return nil, capabilityDBNotReady()
	}

	var p capabilityListParams
	if len(params) > 0 && string(params) != "null" {
		if rpcErr := decodeParams(params, &p); rpcErr != nil {
			return nil, rpcErr
		}
	}

	tokens, err := s.capDB.ListIssued(ctx, p.IncludeRevoked)
	if err != nil {
		return nil, newError(CodeInternalError, fmt.Sprintf("capability.list: %v", err))
	}

	result := capabilityListResult{Tokens: []capabilityTokenWire{}}
	for _, t := range tokens {
		result.Tokens = append(result.Tokens, issuedToWire(t))
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// daimon.capability.revoke
// ---------------------------------------------------------------------------

type capabilityRevokeParams struct {
	TokenID string `json:"token_id"`
}

func (s *Server) handleCapabilityRevoke(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.capDB == nil {
		return nil, capabilityDBNotReady()
	}

	var p capabilityRevokeParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.TokenID == "" {
		return nil, newError(CodeInvalidParams, "token_id is required")
	}

	if err := s.capDB.RevokeToken(ctx, p.TokenID); err != nil {
		return nil, newError(CodeNotFound, fmt.Sprintf("capability.revoke: %v", err))
	}

	// Audit.
	s.auditCapability(ctx, activity.KindCapabilityRevoked, map[string]any{
		"token_id": p.TokenID,
	})

	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// daimon.capability.attenuate
// ---------------------------------------------------------------------------

type capabilityAttenuateParams struct {
	Token           string  `json:"token"`                      // base64url input token
	ValidUntil      string  `json:"valid_until,omitempty"`      // RFC3339
	MaxCalls        int64   `json:"max_calls,omitempty"`
	ModelConstraint string  `json:"model_constraint,omitempty"`
}

type capabilityAttenuateResult struct {
	Token string `json:"token"` // base64url attenuated token
}

func (s *Server) handleCapabilityAttenuate(_ context.Context, params json.RawMessage) (any, *RPCError) {
	if s.capDB == nil {
		return nil, capabilityDBNotReady()
	}

	var p capabilityAttenuateParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Token == "" {
		return nil, newError(CodeInvalidParams, "token is required")
	}

	raw, err := capability.Decode(p.Token)
	if err != nil {
		return nil, newError(CodeInvalidParams, fmt.Sprintf("capability.attenuate: decode token: %v", err))
	}

	opts := capability.AttenuateOptions{
		MaxCalls:        p.MaxCalls,
		ModelConstraint: p.ModelConstraint,
	}
	if p.ValidUntil != "" {
		t, err := time.Parse(time.RFC3339, p.ValidUntil)
		if err != nil {
			return nil, newError(CodeInvalidParams, fmt.Sprintf("valid_until: invalid RFC3339 timestamp: %v", err))
		}
		opts.ValidUntil = &t
	}

	raw2, err := capability.Attenuate(raw, opts)
	if err != nil {
		return nil, newError(CodeInternalError, fmt.Sprintf("capability.attenuate: %v", err))
	}

	return capabilityAttenuateResult{Token: capability.Encode(raw2)}, nil
}

// ---------------------------------------------------------------------------
// Audit helper (shared with future handlers)
// ---------------------------------------------------------------------------

// auditCapability appends an activity-log entry for a capability verb.
// Errors are logged but not surfaced — mirrors auditPeer in peer_channel_handlers.go.
func (s *Server) auditCapability(ctx context.Context, kind activity.Kind, payload map[string]any) {
	if s.alog == nil {
		return
	}
	if _, err := s.alog.Append(ctx, kind, payload); err != nil && s.logger != nil {
		s.logger.Printf("audit %s failed: %v", kind, err)
	}
}
