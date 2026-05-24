// Package reputation implements the Daimon v0.4 reputation primitive:
// signed proof-of-service receipts that a serving daimon attaches to
// peer.ask responses when the caller requests one.
//
// # Receipt model
//
// A receipt is a deterministic JSON envelope signed with the serving
// daimon's Ed25519 private key.  Any party holding the server's DID
// (and therefore its public key via did:key) can verify authenticity
// without contacting the server.
//
// The signed payload is the receipt fields EXCLUDING Signature,
// serialised to JSON in declaration order (Go's encoding/json
// behaviour) and signed with ed25519.Sign.  Signature is then
// base64url-encoded and appended for transport.
//
// # Design reference
//
// design/v0.4-delegation.md §8; SPEC §17 (forthcoming).
package reputation

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Receipt type
// ---------------------------------------------------------------------------

// Receipt is the signed proof-of-service returned by the serving daimon
// when request_receipt=true appears in peer.ask params.
//
// All fields except Signature are part of the canonical payload that is
// signed; verifiers must not modify them before calling Verify.
type Receipt struct {
	// ReceiptID is a ULID identifying this receipt.
	ReceiptID string `json:"receipt_id"`

	// ServedAt is the wall-clock time (UTC, RFC3339) when the serving
	// daimon completed the provider invocation.
	ServedAt string `json:"served_at"`

	// Verb is the peer verb that was served (always "peer.ask" in v0.4).
	Verb string `json:"verb"`

	// ServerDID is the serving daimon's did:key.  Holders can derive the
	// Ed25519 public key from it to verify the Signature.
	ServerDID string `json:"server_did"`

	// CallerDID is the calling peer's did:key if it was in the serving
	// daimon's address book, otherwise empty.
	CallerDID string `json:"caller_did,omitempty"`

	// Provider is the provider name used (e.g. "claude", "ollama").
	Provider string `json:"provider"`

	// Model is the provider model that responded (e.g. "claude-haiku-4-5").
	Model string `json:"model"`

	// InputTokens is the provider-reported input token count.
	InputTokens int64 `json:"input_tokens"`

	// OutputTokens is the provider-reported output token count.
	OutputTokens int64 `json:"output_tokens"`

	// DurationMS is the end-to-end provider round-trip in milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// Signature is the base64url-encoded Ed25519 signature over the
	// canonical JSON of all fields above (in declaration order, no
	// indentation, no trailing newline).
	Signature string `json:"signature"`
}

// ---------------------------------------------------------------------------
// Canonical payload
// ---------------------------------------------------------------------------

// payload is the signable subset of Receipt — identical layout but with
// Signature omitted so the marshalled bytes are stable.
type payload struct {
	ReceiptID    string `json:"receipt_id"`
	ServedAt     string `json:"served_at"`
	Verb         string `json:"verb"`
	ServerDID    string `json:"server_did"`
	CallerDID    string `json:"caller_did,omitempty"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	DurationMS   int64  `json:"duration_ms"`
}

func canonicalBytes(r *Receipt) ([]byte, error) {
	p := payload{
		ReceiptID:    r.ReceiptID,
		ServedAt:     r.ServedAt,
		Verb:         r.Verb,
		ServerDID:    r.ServerDID,
		CallerDID:    r.CallerDID,
		Provider:     r.Provider,
		Model:        r.Model,
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		DurationMS:   r.DurationMS,
	}
	return json.Marshal(p)
}

// ---------------------------------------------------------------------------
// Sign
// ---------------------------------------------------------------------------

// Sign fills r.Signature with the Ed25519 signature of the canonical
// receipt payload, then returns r unchanged (for chaining).
// r.ReceiptID, r.ServedAt, r.Verb, r.ServerDID, r.Provider, and r.Model
// must be set before calling; other fields are optional but stable.
func Sign(privKey ed25519.PrivateKey, r *Receipt) error {
	msg, err := canonicalBytes(r)
	if err != nil {
		return fmt.Errorf("reputation: marshal receipt payload: %w", err)
	}
	sig := ed25519.Sign(privKey, msg)
	r.Signature = base64.RawURLEncoding.EncodeToString(sig)
	return nil
}

// ---------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------

// Verify checks that r.Signature is a valid Ed25519 signature of the
// canonical receipt payload under pubKey.
// Returns nil on success, non-nil on any failure (bad encoding, bad sig, etc.).
func Verify(pubKey ed25519.PublicKey, r *Receipt) error {
	sigBytes, err := base64.RawURLEncoding.DecodeString(r.Signature)
	if err != nil {
		return fmt.Errorf("reputation: decode signature: %w", err)
	}
	msg, err := canonicalBytes(r)
	if err != nil {
		return fmt.Errorf("reputation: marshal receipt payload: %w", err)
	}
	if !ed25519.Verify(pubKey, msg, sigBytes) {
		return fmt.Errorf("reputation: signature verification failed")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// FormatTime formats t as RFC3339 UTC truncated to seconds — the canonical
// form used in receipt ServedAt fields.
func FormatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}
