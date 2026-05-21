package server

import (
	"strings"
	"testing"
)

// --- daimon.peer.listen -------------------------------------------------------


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

	// Phase 33: peer.echo is the first served verb. Protocols must contain it.
	if len(out.Protocols) == 0 {
		t.Error("protocols must not be empty at phase 33+")
	}
	found := false
	for _, p := range out.Protocols {
		if p == "peer.echo" {
			found = true
		}
	}
	if !found {
		t.Errorf("peer.echo missing from protocols: got %v", out.Protocols)
	}
	// No PeerListen called → no public endpoint.
	if out.PublicEndpoint != "" {
		t.Errorf("public_endpoint without PeerListen: got %q, want empty", out.PublicEndpoint)
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

// --- daimon.peer.listen tests -----------------------------------------------

func TestPeerListen_DefaultAddr_BindsAndReturnsTCPEndpoint(t *testing.T) {
	// daimon.peer.listen with no params → binds on 0.0.0.0:0 (OS-assigned
	// port) and returns the actual endpoint as "tcp://host:port".
	f := newFixture(t)
	resp := f.call(t, "daimon.peer.listen", nil)
	var out peerListenStartResult
	resultAs(t, resp, &out)

	if !strings.HasPrefix(out.Endpoint, "tcp://") {
		t.Errorf("endpoint prefix: got %q, want tcp://...", out.Endpoint)
	}
	// Should contain a colon (host:port) after the scheme.
	hostPort := strings.TrimPrefix(out.Endpoint, "tcp://")
	if !strings.Contains(hostPort, ":") {
		t.Errorf("endpoint missing port: got %q", out.Endpoint)
	}
}

func TestPeerListen_ExplicitAddr_BindsOnThatAddr(t *testing.T) {
	// daimon.peer.listen with addr "127.0.0.1:0" → binds on loopback
	// with an OS-assigned port, endpoint starts with "tcp://127.0.0.1:".
	f := newFixture(t)
	resp := f.call(t, "daimon.peer.listen", map[string]any{"addr": "127.0.0.1:0"})
	var out peerListenStartResult
	resultAs(t, resp, &out)

	if !strings.HasPrefix(out.Endpoint, "tcp://127.0.0.1:") {
		t.Errorf("endpoint: got %q, want tcp://127.0.0.1:...", out.Endpoint)
	}
}

func TestPeerListen_TCPSchemePrefix_IsStripped(t *testing.T) {
	// Convenience: if the caller passes "tcp://127.0.0.1:0" (with scheme
	// prefix), the handler strips the prefix before binding — same result
	// as "127.0.0.1:0" without a prefix.
	f := newFixture(t)
	resp := f.call(t, "daimon.peer.listen", map[string]any{"addr": "tcp://127.0.0.1:0"})
	var out peerListenStartResult
	resultAs(t, resp, &out)

	if !strings.HasPrefix(out.Endpoint, "tcp://127.0.0.1:") {
		t.Errorf("endpoint: got %q, want tcp://127.0.0.1:...", out.Endpoint)
	}
}

func TestPeerListen_Idempotent_ReturnsSameEndpoint(t *testing.T) {
	// Calling daimon.peer.listen a second time while already listening
	// must return the SAME bound endpoint (not try to bind again, which
	// would error on a port conflict). PeerListen is idempotent at the
	// Go level; the RPC handler must propagate that property.
	f := newFixture(t)

	resp1 := f.call(t, "daimon.peer.listen", nil)
	var out1 peerListenStartResult
	resultAs(t, resp1, &out1)

	resp2 := f.call(t, "daimon.peer.listen", nil)
	var out2 peerListenStartResult
	resultAs(t, resp2, &out2)

	if out1.Endpoint != out2.Endpoint {
		t.Errorf("idempotent: first=%q second=%q (must be equal)", out1.Endpoint, out2.Endpoint)
	}
}

func TestPeerListen_FederationConfigReflectsEndpoint(t *testing.T) {
	// After daimon.peer.listen succeeds, daimon.federation.config must
	// report the bound address in public_endpoint. This tests the full
	// observability loop: listen → config shows endpoint → peers can dial.
	f := newFixture(t)

	listenResp := f.call(t, "daimon.peer.listen", nil)
	var listenOut peerListenStartResult
	resultAs(t, listenResp, &listenOut)

	configResp := f.call(t, "daimon.federation.config", nil)
	var configOut federationConfigResult
	resultAs(t, configResp, &configOut)

	if configOut.PublicEndpoint != listenOut.Endpoint {
		t.Errorf("federation.config public_endpoint: got %q, want %q",
			configOut.PublicEndpoint, listenOut.Endpoint)
	}
	if !strings.HasPrefix(configOut.PublicEndpoint, "tcp://") {
		t.Errorf("public_endpoint prefix: got %q, want tcp://...", configOut.PublicEndpoint)
	}
}
