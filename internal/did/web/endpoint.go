package web

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// DaimonEndpoint is the Daimon-specific service-array entry inside a
// W3C DID Document. Lives at .service[i].serviceEndpoint when
// .service[i].type == "DaimonEndpoint" (DaimonServiceType in
// document.go).
//
// The shape sits inside a standard W3C DID Document so general
// did:web consumers can parse the document without choking on our
// extension — they just won't understand the DaimonEndpoint service
// type and will treat it as opaque. Daimon-aware clients
// (Resolver.ResolveDaimon below) pull this struct out + verify the
// signature.
//
// design/v0.3-federation.md Decision 2 names the fields below as
// the v0.3 federation discovery contract. Adding a field later
// (e.g. "supported_chains" once federation x402 lands more chains)
// requires the SignedEndpoint canonical-JSON serialization to
// remain backwards-compatible — see signaturePayload's docs.
type DaimonEndpoint struct {
	// URL where this daimon listens for federation traffic. Per
	// design Decision 1, this is a QUIC+Noise endpoint (e.g.
	// quic://daimon.example.com:4242). v0.3 supports a single
	// URL per daimon; multiple endpoints (multi-homing, A/B
	// rollouts) deferred to v0.3.x.
	URL string `json:"url"`

	// TransportPubKey is the daimon's Ed25519 public key as a
	// multibase string (z6Mk... form, matching the
	// verificationMethod entry up at the document level). Included
	// here as a convenience for clients that pre-Noise resolve
	// (so they have everything they need from one service entry).
	// MUST match the verificationMethod[*].publicKeyMultibase at
	// the document level — Daimon's verify path checks this.
	TransportPubKey string `json:"transport_pubkey"`

	// Protocols is the array of supported A2A verb namespaces
	// this daimon will serve over the channel. Examples:
	// "peer.echo", "peer.ask", "peer.pay". A client checks this
	// before invoking a verb to give a clean "this peer doesn't
	// serve that protocol" error rather than a -32601
	// MethodNotFound mid-call.
	Protocols []string `json:"protocols"`

	// PaymentAddress is the on-chain address this daimon accepts
	// inbound x402 payments at. Format: "<chain>:<address>"
	// (e.g. "evm:base:0x9858EfFD..."). Optional; daimons that don't
	// accept payments omit the field. Format-validation deferred
	// to the verify-payment-request step, not here.
	PaymentAddress string `json:"payment_address,omitempty"`
}

// SignedEndpoint wraps DaimonEndpoint with an Ed25519 signature over
// the canonical-JSON serialization of the endpoint fields. The
// signature is a Daimon-specific extension to W3C did:web — general
// W3C consumers parse the DaimonEndpoint as opaque, Daimon-aware
// clients verify the signature against the document's
// verificationMethod pubkey before trusting the URL / protocols /
// payment_address fields.
//
// Trust model (per design Decision 5, TOFU + pinning):
//   - First time a client resolves a DID, it records (DID, pubkey)
//     as a tuple in its address book.
//   - On subsequent resolves, the pubkey MUST match what was pinned.
//     A mismatch is either key rotation (which v0.3 doesn't support
//     for did:web — that's a v0.3.x extension) or compromise.
//   - The signature on the SignedEndpoint chains the URL +
//     transport_pubkey + protocols to the DID's identity key, so
//     a server compromise that swaps the URL but keeps the same
//     pubkey is detectable: the signature wouldn't verify.
type SignedEndpoint struct {
	Endpoint  DaimonEndpoint `json:"endpoint"`
	Signature string         `json:"signature"` // base64url-encoded Ed25519 signature
}

// signaturePayload returns the deterministic byte sequence that's
// signed by SignEndpoint and verified by VerifyEndpoint. The shape
// is intentionally minimal + canonical: a single SHA-256 hash of
// the field tuple (url, transport_pubkey, sorted-protocols-array,
// payment_address) joined with NUL bytes.
//
// Canonical-JSON would be the more conventional choice (RFC 8785
// JCS — what W3C Data Integrity uses), but JCS is ~150 LoC of
// careful sort + escape logic and Daimon's signature is consumed
// only by Daimons, not general W3C verifiers. The NUL-joined
// SHA-256 over a fixed field order is deterministic, simple to
// audit, and impossible to ambiguate.
//
// **Format stability is part of the protocol contract**: any
// future change to this function's output for the same input
// invalidates every previously-signed endpoint. Adding a new
// optional DaimonEndpoint field MUST extend this function in a
// backwards-compatible way (empty-string default for absent
// fields), so verifiers on older binaries still produce the same
// payload for the older documents.
func signaturePayload(e DaimonEndpoint) []byte {
	// Sort protocols so the same set in different document orderings
	// produces the same signature. The on-wire JSON can carry them
	// in any order; the signature payload normalizes.
	sortedProtocols := append([]string(nil), e.Protocols...)
	sort.Strings(sortedProtocols)

	var b []byte
	b = append(b, []byte("daimon-endpoint-v1")...)
	b = append(b, 0)
	b = append(b, []byte(e.URL)...)
	b = append(b, 0)
	b = append(b, []byte(e.TransportPubKey)...)
	b = append(b, 0)
	for _, p := range sortedProtocols {
		b = append(b, []byte(p)...)
		b = append(b, 0)
	}
	b = append(b, 0) // protocols/payment separator
	b = append(b, []byte(e.PaymentAddress)...)

	h := sha256.Sum256(b)
	return h[:]
}

// SignEndpoint signs the endpoint document with the supplied
// Ed25519 private key. The returned SignedEndpoint contains the
// original endpoint + a base64url-encoded signature over
// signaturePayload(endpoint).
//
// The private key is used directly — no key derivation, no AAD.
// The signature is over a SHA-256 hash so the cost is constant in
// endpoint size.
func SignEndpoint(priv ed25519.PrivateKey, e DaimonEndpoint) (*SignedEndpoint, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("daimon-endpoint sign: private key wrong size: got %d, want %d", len(priv), ed25519.PrivateKeySize)
	}
	if e.URL == "" {
		return nil, errors.New("daimon-endpoint sign: URL is required")
	}
	if e.TransportPubKey == "" {
		return nil, errors.New("daimon-endpoint sign: transport_pubkey is required")
	}
	sig := ed25519.Sign(priv, signaturePayload(e))
	return &SignedEndpoint{
		Endpoint:  e,
		Signature: base64.RawURLEncoding.EncodeToString(sig),
	}, nil
}

// VerifyEndpoint asserts that the SignedEndpoint's signature was
// produced by the holder of pub for the embedded endpoint.
// Returns nil on success, an error otherwise.
//
// This is the "I trust this DID, does this endpoint document
// match?" check. The expected workflow:
//
//  1. Client resolves a DID → Document
//  2. Client extracts the Ed25519 pubkey from
//     Document.VerificationMethod[i].PublicKeyMultibase (the
//     authoritative key, since the DID self-identifies via the
//     document)
//  3. Client finds the service entry with type ==
//     DaimonServiceType, JSON-unmarshals its serviceEndpoint into
//     SignedEndpoint
//  4. Client calls VerifyEndpoint(signed, pub) — if it returns nil,
//     the endpoint document is genuine
//
// VerifyEndpoint does NOT check that the endpoint's
// TransportPubKey matches pub — that's a separate check the
// caller does (and the v0.3 address book records the tuple).
// Here we only verify the signature.
func VerifyEndpoint(signed *SignedEndpoint, pub ed25519.PublicKey) error {
	if signed == nil {
		return errors.New("daimon-endpoint verify: nil signed endpoint")
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("daimon-endpoint verify: public key wrong size: got %d, want %d", len(pub), ed25519.PublicKeySize)
	}
	sig, err := base64.RawURLEncoding.DecodeString(signed.Signature)
	if err != nil {
		return fmt.Errorf("daimon-endpoint verify: decode signature: %w", err)
	}
	if !ed25519.Verify(pub, signaturePayload(signed.Endpoint), sig) {
		return errors.New("daimon-endpoint verify: signature does not verify")
	}
	return nil
}

// ExtractSignedEndpoint pulls the DaimonEndpoint service entry out
// of a parsed W3C DID Document, JSON-unmarshaling its
// serviceEndpoint field into a SignedEndpoint.
//
// Returns a sentinel ErrNoDaimonEndpoint if the document has no
// service entry of type DaimonServiceType, so callers can branch
// on "this is a valid W3C DID but it's not advertising a Daimon
// endpoint" cleanly.
func ExtractSignedEndpoint(doc *Document) (*SignedEndpoint, error) {
	if doc == nil {
		return nil, errors.New("daimon-endpoint extract: nil document")
	}
	for _, svc := range doc.Service {
		if svc.Type != DaimonServiceType {
			continue
		}
		var signed SignedEndpoint
		if err := json.Unmarshal(svc.ServiceEndpoint, &signed); err != nil {
			return nil, fmt.Errorf("daimon-endpoint extract: parse service entry %q: %w", svc.ID, err)
		}
		return &signed, nil
	}
	return nil, ErrNoDaimonEndpoint
}

// ErrNoDaimonEndpoint is returned by ExtractSignedEndpoint when the
// document has no service entry of type DaimonServiceType. Callers
// can use errors.Is to distinguish this from JSON-parse failures.
var ErrNoDaimonEndpoint = errors.New("daimon-endpoint extract: no service entry with type \"DaimonEndpoint\"")
