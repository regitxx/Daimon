// Package capability implements the Daimon v0.4 capability delegation primitive.
//
// It wraps github.com/biscuit-auth/biscuit-go/v2 to provide three operations:
//
//   - Issue    — mint a root Biscuit token signed with the daimon's Ed25519 key
//   - Attenuate — offline: add more restrictive checks to an existing token
//   - Verify   — check a token against the serving daimon's ambient context
//
// # Capability model
//
// A root token asserts one or more right facts in Biscuit Datalog:
//
//	right("peer.ask", "did:key:z6MkAlice...")  // grant to specific daimon
//	right("peer.ask", "any")                   // grant to any daimon in address book
//
// Optional checks embedded in the authority block:
//
//	check if time($t), $t <= 2026-12-31T00:00:00Z    // expiry
//	check if peer_ask_model($m), $m == "claude-haiku" // model allowlist
//	check if calls_used($n), $n < 100                 // call-count ceiling (stateful: enforced by serving daimon in v0.4+)
//
// During Verify the serving daimon provides ambient facts (current time, model
// used, call count) and a policy that allows only if the matching right is present.
//
// # Design reference
//
// SPEC §17 (forthcoming); design/v0.4-delegation.md §4–§5.
package capability

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	biscuit "github.com/biscuit-auth/biscuit-go/v2"
	"github.com/biscuit-auth/biscuit-go/v2/parser"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// IssueOptions configures the root Biscuit token.
type IssueOptions struct {
	// Verbs is the list of Daimon verbs to grant (e.g. "peer.ask").
	// At least one verb is required.
	Verbs []string

	// TargetDID restricts the grant to a specific serving daimon DID.
	// Pass the empty string (or omit) to grant against any target ("any").
	TargetDID string

	// ValidUntil is the hard expiry.  Zero means no expiry embedded in the token.
	ValidUntil time.Time

	// MaxCalls embeds a call-count ceiling check.
	// 0 means no ceiling.
	// NOTE: enforcement requires the serving daimon to inject the calls_used fact
	// from capability.db (phase 41).  The check is embedded now but advisory until
	// phase 41 ships.
	MaxCalls int64

	// ModelConstraint, if non-empty, embeds a check that the model used matches.
	ModelConstraint string
}

// AttenuateOptions describes the additional checks to embed in a new block.
// All non-zero fields are applied; zero fields are ignored (no tightening).
type AttenuateOptions struct {
	// ValidUntil replaces (tightens) the token's expiry.
	// Must not be after the existing expiry — Biscuit won't enforce ordering
	// of time checks, but a well-behaved caller should only tighten.
	ValidUntil *time.Time

	// MaxCalls sets or tightens a call-count ceiling.
	MaxCalls int64

	// ModelConstraint tightens the model allowlist.
	ModelConstraint string
}

// VerifyContext is the ambient state the serving daimon supplies at verification
// time.  All fields relevant to the checks embedded in the token must be set.
type VerifyContext struct {
	// Verb is the peer verb being invoked (e.g. "peer.ask").
	Verb string

	// TargetDID is the serving daimon's own DID.  The token must contain
	// right(verb, targetDID) OR right(verb, "any") for verification to pass.
	TargetDID string

	// Now is the current wall-clock time injected as the time() ambient fact.
	// If zero, time.Now() is used.
	Now time.Time

	// Model is the model name being used (e.g. "claude-opus-4-5").
	// If empty, no peer_ask_model fact is injected.
	Model string

	// CallsUsed is the number of calls already made under this token_id.
	// If zero, no calls_used fact is injected.
	// Required once the calls_used ceiling check is in use (phase 41).
	CallsUsed int64
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	// ErrNoVerbs is returned when IssueOptions.Verbs is empty.
	ErrNoVerbs = errors.New("capability: at least one verb is required")

	// ErrTokenExpired is returned when verification fails due to the expiry check.
	ErrTokenExpired = errors.New("capability: token has expired")

	// ErrDenied wraps the underlying Biscuit authorization failure.
	ErrDenied = errors.New("capability: authorization denied")
)

// ---------------------------------------------------------------------------
// Issue
// ---------------------------------------------------------------------------

// Issue mints a root Biscuit token signed with privKey.
// The token serialisation is returned as raw bytes (protobuf).
// Callers typically base64url-encode the result for transport.
func Issue(privKey ed25519.PrivateKey, opts IssueOptions) ([]byte, error) {
	if len(opts.Verbs) == 0 {
		return nil, ErrNoVerbs
	}

	target := opts.TargetDID
	if target == "" {
		target = "any"
	}

	// Build the Datalog source for the authority block.
	var src string
	for _, verb := range opts.Verbs {
		src += fmt.Sprintf(`right(%q, %q);`+"\n", verb, target)
	}

	// Time expiry check.
	if !opts.ValidUntil.IsZero() {
		ts := opts.ValidUntil.UTC().Truncate(time.Second).Format(time.RFC3339)
		src += fmt.Sprintf("check if time($t), $t <= %s;\n", ts)
	}

	// Model constraint check.
	if opts.ModelConstraint != "" {
		src += fmt.Sprintf(`check if peer_ask_model($m), $m == %q;`+"\n", opts.ModelConstraint)
	}

	// Call-count ceiling check (advisory until phase 41).
	if opts.MaxCalls > 0 {
		src += fmt.Sprintf("check if calls_used($n), $n < %d;\n", opts.MaxCalls)
	}

	block, err := parser.FromStringBlock(src)
	if err != nil {
		return nil, fmt.Errorf("capability: parse authority block: %w", err)
	}

	b := biscuit.NewBuilder(privKey)
	if err := b.AddBlock(block); err != nil {
		return nil, fmt.Errorf("capability: build authority block: %w", err)
	}

	tok, err := b.Build()
	if err != nil {
		return nil, fmt.Errorf("capability: build token: %w", err)
	}

	raw, err := tok.Serialize()
	if err != nil {
		return nil, fmt.Errorf("capability: serialize token: %w", err)
	}
	return raw, nil
}

// ---------------------------------------------------------------------------
// Attenuate
// ---------------------------------------------------------------------------

// Attenuate adds a new, more-restrictive block to an existing token.
// The operation is performed offline — no contact with the issuer is required.
// The returned bytes are a new serialized Biscuit token that is a strict
// subset of the input token's authority.
func Attenuate(token []byte, opts AttenuateOptions) ([]byte, error) {
	tok, err := biscuit.Unmarshal(token)
	if err != nil {
		return nil, fmt.Errorf("capability: unmarshal token: %w", err)
	}

	var src string

	if opts.ValidUntil != nil {
		ts := opts.ValidUntil.UTC().Truncate(time.Second).Format(time.RFC3339)
		src += fmt.Sprintf("check if time($t), $t <= %s;\n", ts)
	}

	if opts.ModelConstraint != "" {
		src += fmt.Sprintf(`check if peer_ask_model($m), $m == %q;`+"\n", opts.ModelConstraint)
	}

	if opts.MaxCalls > 0 {
		src += fmt.Sprintf("check if calls_used($n), $n < %d;\n", opts.MaxCalls)
	}

	if src == "" {
		// No checks to add — return a copy of the original serialized token.
		return token, nil
	}

	block, err := parser.FromStringBlock(src)
	if err != nil {
		return nil, fmt.Errorf("capability: parse attenuation block: %w", err)
	}

	bb := tok.CreateBlock()
	for _, f := range block.Facts {
		if err := bb.AddFact(f); err != nil {
			return nil, fmt.Errorf("capability: add fact to attenuation block: %w", err)
		}
	}
	for _, c := range block.Checks {
		if err := bb.AddCheck(c); err != nil {
			return nil, fmt.Errorf("capability: add check to attenuation block: %w", err)
		}
	}

	tok2, err := tok.Append(rand.Reader, bb.Build())
	if err != nil {
		return nil, fmt.Errorf("capability: append attenuation block: %w", err)
	}

	raw, err := tok2.Serialize()
	if err != nil {
		return nil, fmt.Errorf("capability: serialize attenuated token: %w", err)
	}
	return raw, nil
}

// ---------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------

// Verify checks that token:
//  1. Has a valid signature chain rooted at rootPubKey.
//  2. Grants right(ctx.Verb, ctx.TargetDID) or right(ctx.Verb, "any").
//  3. Passes all embedded Datalog checks given the ambient ctx.
//
// On success nil is returned.  On failure ErrDenied (or a more specific
// error wrapping it) is returned.
func Verify(token []byte, rootPubKey ed25519.PublicKey, ctx VerifyContext) error {
	tok, err := biscuit.Unmarshal(token)
	if err != nil {
		return fmt.Errorf("%w: unmarshal: %v", ErrDenied, err)
	}

	authorizer, err := tok.Authorizer(rootPubKey)
	if err != nil {
		return fmt.Errorf("%w: create authorizer: %v", ErrDenied, err)
	}

	// Inject ambient facts.
	now := ctx.Now
	if now.IsZero() {
		now = time.Now()
	}

	timeFact, err := parser.FromStringFact(fmt.Sprintf("time(%s)", now.UTC().Truncate(time.Second).Format(time.RFC3339)))
	if err != nil {
		return fmt.Errorf("capability: build time fact: %w", err)
	}
	authorizer.AddFact(timeFact)

	if ctx.Model != "" {
		modelFact, err := parser.FromStringFact(fmt.Sprintf(`peer_ask_model(%q)`, ctx.Model))
		if err != nil {
			return fmt.Errorf("capability: build model fact: %w", err)
		}
		authorizer.AddFact(modelFact)
	}

	if ctx.CallsUsed > 0 {
		callsFact, err := parser.FromStringFact(fmt.Sprintf("calls_used(%d)", ctx.CallsUsed))
		if err != nil {
			return fmt.Errorf("capability: build calls_used fact: %w", err)
		}
		authorizer.AddFact(callsFact)
	}

	// Policy: allow if right(verb, targetDID) OR right(verb, "any").
	// We add two separate allow-policies; Biscuit stops at the first match.
	allowSpecific, err := parser.FromStringPolicy(
		fmt.Sprintf(`allow if right(%q, %q)`, ctx.Verb, ctx.TargetDID),
	)
	if err != nil {
		return fmt.Errorf("capability: build allow-specific policy: %w", err)
	}
	allowAny, err := parser.FromStringPolicy(
		fmt.Sprintf(`allow if right(%q, "any")`, ctx.Verb),
	)
	if err != nil {
		return fmt.Errorf("capability: build allow-any policy: %w", err)
	}
	authorizer.AddPolicy(allowSpecific)
	authorizer.AddPolicy(allowAny)

	if err := authorizer.Authorize(); err != nil {
		return fmt.Errorf("%w: %v", ErrDenied, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Encoding helpers
// ---------------------------------------------------------------------------

// Encode base64url-encodes raw token bytes for wire transport.
func Encode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode base64url-decodes a wire token string to raw bytes.
func Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
