package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/regitxx/Daimon/internal/activity"
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

// ---------------------------------------------------------------------------
// daimon.peer.listen
// ---------------------------------------------------------------------------

// peerListenStartParams carries the optional bind address for the inbound
// Noise IK TCP listener.
type peerListenStartParams struct {
	// Addr is the TCP address to listen on, e.g. "0.0.0.0:9999" or
	// "tcp://0.0.0.0:9999". An empty string or omitted field defaults to
	// "0.0.0.0:0" (OS-assigned ephemeral port). The caller learns the
	// actual bound address from the returned endpoint field.
	Addr string `json:"addr,omitempty"`
}

// peerListenStartResult carries the actual bound TCP address.
type peerListenStartResult struct {
	// Endpoint is the URL where this daimon is now accepting inbound Noise IK
	// connections from remote daimons, in "tcp://host:port" form.
	Endpoint string `json:"endpoint"`
}

// handlePeerListenStart serves daimon.peer.listen. It calls s.PeerListen
// which is idempotent — calling it a second time while the listener is already
// running returns the existing bound address rather than binding again.
//
// Authorization: post-unlock only (s.id required by PeerListen — the daemon
// needs its Ed25519 private key to set up the Noise IK static key pair).
// The standard CodeIdentityLocked gate in dispatch covers the locked case.
func (s *Server) handlePeerListenStart(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p peerListenStartParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	addr := strings.TrimPrefix(p.Addr, "tcp://")
	if addr == "" {
		addr = "0.0.0.0:0"
	}

	bound, err := s.PeerListen(addr)
	if err != nil {
		return nil, newError(CodeInvalidRequest, fmt.Sprintf("peer.listen: %v", err))
	}
	endpoint := "tcp://" + bound

	// Audit: peer.listen.started — best-effort; never fails the RPC.
	if s.alog != nil {
		if _, aerr := s.alog.Append(ctx, activity.KindPeerListenStarted, map[string]any{
			"endpoint": endpoint,
		}); aerr != nil {
			s.logf("activity append (peer.listen.started): %v", aerr)
		}
	}

	return peerListenStartResult{Endpoint: endpoint}, nil
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

	// PublicEndpoint: report the peer server's TCP address if one is
	// running, formatted as "tcp://host:port". Empty string means "not
	// listening" which is a valid state (the daimon can dial peers but
	// cannot be dialed in this configuration).
	pubEndpoint := ""
	if addr := s.PeerAddr(); addr != "" {
		pubEndpoint = "tcp://" + addr
	}

	// Protocols: list the peer.* verbs this daimon serves over inbound
	// Noise channels. Phase 33 introduced peer.echo; phase 34 adds peer.ask;
	// phase 35 adds peer.pay.required (price discovery for peer.ask).
	// peer.ask requires address-book authorization; peer.pay.required and
	// peer.echo are universally available — their presence here signals
	// capability, not open access.
	protocols := []string{"peer.echo", "peer.ask", "peer.pay.required"}

	return federationConfigResult{
		DID:                      did,
		TransportPubKeyMultibase: identity.MultibaseFragment(did),
		DIDMethods:               []string{"did:key"},
		Protocols:                protocols,
		PublicEndpoint:           pubEndpoint,
		FederationVersion:        "v0.3-draft",
	}, nil
}
