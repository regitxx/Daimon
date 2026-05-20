package server

import (
	"context"
	"encoding/json"

	"github.com/regitxx/Daimon/internal/identity"
)

// federationConfigResult is the response shape for
// daimon.federation.config. Lets a client introspect what this
// daimon advertises over federation: its DID, transport pubkey,
// supported DID methods, served protocols, and (when configured)
// its public network endpoint.
//
// Phase 30 of v0.3 (see design/v0.3-federation.md) introduces
// this verb as the introspection primitive that subsequent
// federation tooling will build on. Clients use it to:
//
//   - Discover the daimon's own DID so they can address it from
//     other daimons (e.g. "tell me the DID I should pin")
//   - Discover what protocols the daimon serves (peer.echo,
//     peer.ask, peer.pay, …) so they can branch before invoking
//     a verb the daimon doesn't expose
//   - Confirm what DID methods this daimon resolves outbound
//     (did:key always; did:web when phase 30+ tooling is wired
//     into a public endpoint)
//
// At v0.3 phase 30 baseline, the protocols list is empty (no
// peer.* verbs land until later phases) and the public_endpoint
// field is empty (no transport layer until phase 31). The
// verb's value at this stage is proving the RPC dispatch path
// works + giving SDKs a stable shape to introspect against.
type federationConfigResult struct {
	// DID is this daimon's own DID. Always present.
	DID string `json:"did"`

	// TransportPubKeyMultibase is the Ed25519 public key the daimon
	// uses for both identity (signing the audit chain) AND
	// transport authentication (the static key for Noise IK
	// handshakes once phase 31 lands). One key for both purposes
	// per design Decision 1 ("cryptographic continuity via
	// did:key"). Format: z6Mk... multibase string.
	TransportPubKeyMultibase string `json:"transport_pubkey_multibase"`

	// DIDMethods is the array of DID methods the daimon can resolve
	// outbound, plus its own method (always first). Determines
	// what peer DIDs this daimon can dial.
	//
	// Currently {"did:key"} only — did:web is the design Decision 2
	// next-phase addition but isn't wired into outbound resolution
	// until a federation transport layer can actually use the URL.
	// Until then, advertising did:web in this field would be a
	// lie ("I support this method") when the daemon can't actually
	// dial a did:web endpoint.
	DIDMethods []string `json:"did_methods"`

	// Protocols is the array of A2A protocol verbs this daimon
	// serves over the federation channel. Empty at phase 30
	// baseline; populated as `peer.*` verbs land in phases 33+.
	Protocols []string `json:"protocols"`

	// PublicEndpoint is the URL where this daimon listens for
	// inbound federation traffic (per design Decision 1, a
	// QUIC+Noise endpoint like quic://daimon.example.com:4242).
	// Empty until phase 31 lands the transport layer.
	// omitempty in JSON: an empty endpoint stays out of the
	// serialized output entirely, so SDKs don't confuse "no
	// endpoint configured" with "endpoint configured to empty
	// string".
	PublicEndpoint string `json:"public_endpoint,omitempty"`

	// FederationVersion identifies the federation protocol
	// vocabulary this daimon implements. Allows future
	// SDK callers to fail-fast against a daimon that's older
	// than they require. Currently "v0.3-draft" for the
	// design-doc-implementation pre-GA phase.
	FederationVersion string `json:"federation_version"`
}

// handleFederationConfig answers daimon.federation.config. Pure
// read — no audit-log row, no state mutation. The information
// returned is derived entirely from the daemon's unlocked
// identity + (eventually) its on-disk federation config; nothing
// here goes over the network or touches another daimon.
//
// Available pre-OR-post unlock? Currently post-unlock only —
// the dispatcher's CodeIdentityLocked gate applies because we
// reach for s.id.DID(). If a use case appears for pre-unlock
// introspection (e.g. a deployment script wanting to verify
// the daemon-spawned binary supports federation before
// unlocking), we'd carve out a pre-unlock allowlist in
// jsonrpc.go. For now, post-unlock is the consistent posture.
func (s *Server) handleFederationConfig(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	did := s.id.DID()
	return federationConfigResult{
		DID:                      did,
		TransportPubKeyMultibase: identity.MultibaseFragment(did),
		DIDMethods:               []string{"did:key"},
		Protocols:                []string{},
		PublicEndpoint:           "",
		FederationVersion:        "v0.3-draft",
	}, nil
}
