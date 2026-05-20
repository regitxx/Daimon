package server

import (
	"strings"
	"testing"
)

// daimon.federation.config introspects the daimon's federation-
// layer advertisement. The verb is read-only, lives outside the
// wallet/payment path, and is the foundation phase 30 of v0.3
// introduces for clients that want to discover what they're
// talking to before invoking peer.* verbs.

func TestHandleFederationConfig_ReturnsBaseline(t *testing.T) {
	f := newFixture(t)
	resp := f.call(t, "daimon.federation.config", nil)
	var out federationConfigResult
	resultAs(t, resp, &out)

	// DID is always present and matches the daemon's identity.
	if out.DID != f.id.DID() {
		t.Errorf("DID: got %q, want %q", out.DID, f.id.DID())
	}
	if !strings.HasPrefix(out.DID, "did:key:") {
		t.Errorf("DID prefix: got %q, want did:key:...", out.DID)
	}

	// TransportPubKeyMultibase matches the did:key fragment — same
	// key used for identity AND transport (design Decision 1:
	// cryptographic continuity).
	if out.TransportPubKeyMultibase == "" {
		t.Error("transport_pubkey_multibase is empty")
	}
	// The multibase fragment is the part after "did:key:" in the
	// daimon's DID. They MUST match exactly — different values
	// would mean we have two pubkeys floating around, which
	// breaks the design invariant.
	want := strings.TrimPrefix(f.id.DID(), "did:key:")
	if out.TransportPubKeyMultibase != want {
		t.Errorf("transport_pubkey_multibase: got %q, want %q (must equal multibase fragment of DID)",
			out.TransportPubKeyMultibase, want)
	}

	// At phase 30 baseline: did:key only (did:web added when
	// outbound resolution lands a transport in phase 31).
	if len(out.DIDMethods) != 1 || out.DIDMethods[0] != "did:key" {
		t.Errorf("did_methods: got %v, want [did:key]", out.DIDMethods)
	}

	// Phase 30 baseline: no peer.* verbs yet, no public endpoint.
	if len(out.Protocols) != 0 {
		t.Errorf("protocols at baseline: got %v, want empty", out.Protocols)
	}
	if out.PublicEndpoint != "" {
		t.Errorf("public_endpoint at baseline: got %q, want empty", out.PublicEndpoint)
	}

	// Federation version tag — pinned to "v0.3-draft" until
	// design review concludes and we're shipping toward GA.
	if out.FederationVersion != "v0.3-draft" {
		t.Errorf("federation_version: got %q, want v0.3-draft", out.FederationVersion)
	}
}

func TestHandleFederationConfig_NotInWalletNotReadyTable(t *testing.T) {
	// federation.config does NOT depend on the wallet keystore.
	// A daemon with wallet RPCs disabled (the silent-failure mode
	// the recover cross-check + doctor diagnostic fight against)
	// MUST still answer federation.config. This is a regression
	// guard: if a future refactor accidentally adds wstore-nil
	// gating to the federation handler, this test trips.
	f := newFixture(t)
	// f.wstore is nil by default (newFixture vs newWalletFixture);
	// just confirm the call succeeds.
	resp := f.call(t, "daimon.federation.config", nil)
	if resp.Error != nil {
		t.Errorf("federation.config with nil wstore: got error %v, want nil", resp.Error)
	}
}
