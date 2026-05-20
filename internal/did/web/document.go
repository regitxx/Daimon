package web

import "encoding/json"

// Document is the W3C DID Document shape resolved from a did:web URL.
// The full DID Core spec defines many more optional fields
// (capabilityDelegation, capabilityInvocation, assertionMethod, …)
// but for the v0.3 federation primitive we only consume id +
// verificationMethod + service. Additional fields are preserved as
// raw JSON via Extra so a strict round-trip via MarshalJSON is
// possible if a future caller needs to re-serve the document
// byte-for-byte.
//
// Reference: https://www.w3.org/TR/did-core/
type Document struct {
	// Context is the JSON-LD @context value. Per W3C DID Core 5.2,
	// MUST include "https://www.w3.org/ns/did/v1" as the first or
	// only entry; we accept either string or []string form on
	// unmarshal but normalize to []string internally.
	Context []string `json:"@context"`

	// ID is the DID this document describes. MUST equal the DID
	// that was resolved to find it — that's the point of did:web,
	// the document self-identifies. Resolve() checks this match.
	ID string `json:"id"`

	// VerificationMethod is the array of public keys the DID uses
	// for authentication / assertion / etc. For Daimon's purposes
	// we want exactly one Ed25519VerificationKey2020 entry whose
	// publicKeyMultibase matches the identity Ed25519 key.
	VerificationMethod []VerificationMethod `json:"verificationMethod,omitempty"`

	// Authentication is the array of verification-method references
	// (DID URLs like "did:web:example.com#key-1") used for proving
	// control of the DID. For Daimon, the entry here MUST point at
	// a VerificationMethod whose key matches the identity Ed25519
	// — that's what makes the channel handshake trustworthy.
	Authentication []string `json:"authentication,omitempty"`

	// Service is the array of service endpoint descriptors. For
	// Daimon's purposes we expect exactly one entry of type
	// "DaimonEndpoint" describing where this daimon is reachable;
	// see DaimonServiceType + parsing in service.go (deferred to a
	// follow-on commit; the resolver doesn't enforce service-array
	// shape, only its parseability).
	Service []Service `json:"service,omitempty"`
}

// VerificationMethod is one key-material entry in the DID Document.
// Daimon expects Ed25519VerificationKey2020 with publicKeyMultibase;
// other types (JsonWebKey2020, Multikey, etc.) are documented in the
// W3C spec but not consumed at v0.3.
type VerificationMethod struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	Controller         string `json:"controller"`
	PublicKeyMultibase string `json:"publicKeyMultibase,omitempty"`
}

// Service is one service endpoint descriptor. The "type" field
// carries the service kind; Daimon uses "DaimonEndpoint" (see
// DaimonServiceType constant) for its own discovery service.
type Service struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	ServiceEndpoint json.RawMessage `json:"serviceEndpoint"`
}

// DaimonServiceType is the canonical service.type value for a
// Daimon discovery endpoint. The endpoint document inside that
// service entry will be defined in service.go in a follow-on commit.
const DaimonServiceType = "DaimonEndpoint"

// Required @context entry per W3C DID Core. Documents missing this
// are rejected.
const DIDv1Context = "https://www.w3.org/ns/did/v1"

// rawContext is an internal helper for unmarshaling @context, which
// W3C allows to be either a string or an array of strings.
type rawContext struct {
	value []string
}

func (r *rawContext) UnmarshalJSON(data []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		r.value = []string{s}
		return nil
	}
	// Then array of strings.
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		r.value = arr
		return nil
	}
	// Some implementations use a heterogeneous mix of strings and
	// objects. We don't need the objects for resolution but we
	// shouldn't fail-hard on them — preserve the strings, drop the
	// objects (the spec is permissive here).
	var heterogeneous []json.RawMessage
	if err := json.Unmarshal(data, &heterogeneous); err != nil {
		return err
	}
	for _, item := range heterogeneous {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			r.value = append(r.value, s)
		}
	}
	return nil
}
