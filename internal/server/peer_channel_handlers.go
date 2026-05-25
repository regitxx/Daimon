package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/addressbook"
	"github.com/regitxx/Daimon/internal/capability"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/payment"
	"github.com/regitxx/Daimon/internal/provider"
	"github.com/regitxx/Daimon/internal/reputation"
	"github.com/regitxx/Daimon/internal/transport"
)

// Peer channel RPC handlers — the client-facing surface for federation.
//
// Design (see design/v0.3-federation.md §Decision 3):
//
//   - daimon.peer.dial    — open a Noise IK channel to a remote daimon
//   - daimon.peer.close   — close an open channel
//   - daimon.peer.list    — enumerate open channels
//   - daimon.peer.invoke  — invoke a peer.* method on a remote daimon
//
// The wire framing is JSON-RPC 2.0 messages sent as length-prefixed frames
// over the Noise-encrypted TCP connection (internal/transport.Conn). One
// request per frame, one response per frame — no streaming over the peer
// channel in v0.3.
//
// Also in this file: peer.echo — the first peer-served verb (phase 33).
// peer.echo is handled on the INBOUND side (when a remote daimon calls us);
// the four daimon.peer.* verbs are handled on the OUTBOUND side (when a
// local client issues RPCs to us and we proxy them through a Noise channel).

// peerNotReady returns the standard "not loaded" error for the peer channel
// surface. At present this is only possible in demo mode when the identity
// is present but there's a bug — in serve mode, PeerListen validates that
// the identity is unlocked. Defensive guard kept for clarity.
func peerNotReady() *RPCError {
	return newError(CodeInvalidRequest, "peer channel surface not available")
}

// --- daimon.peer.dial --------------------------------------------------------

type peerDialParams struct {
	// DID is the remote daimon's DID. For did:key DIDs, the Ed25519 public
	// key is embedded in the DID and used directly for Noise IK authentication
	// (via Ed25519PublicToX25519). No additional address-book lookup required.
	DID string `json:"did"`

	// Endpoint is the remote daimon's TCP address, e.g. "tcp://host:port" or
	// "host:port". Required: there is no automatic resolution in v0.3.
	Endpoint string `json:"endpoint"`
}

type peerDialResult struct {
	ChannelID string `json:"channel_id"`
	PeerDID   string `json:"peer_did"`
	OpenedAt  string `json:"opened_at"`
}

func (s *Server) handlePeerDial(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.id == nil {
		return nil, peerNotReady()
	}
	var p peerDialParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.DID == "" {
		return nil, newError(CodeInvalidParams, "did is required")
	}
	if p.Endpoint == "" {
		return nil, newError(CodeInvalidParams, "endpoint is required")
	}

	// Resolve the peer's Ed25519 public key from the DID.
	// v0.3 supports did:key only; did:web resolution is phase 34+.
	edPub, err := identity.PublicKeyFromDID(p.DID)
	if err != nil {
		return nil, newError(CodeInvalidParams, fmt.Sprintf("cannot resolve DID to public key: %v", err))
	}

	// Derive the peer's X25519 static key (needed for Noise IK initiator).
	remotePub, err := transport.Ed25519PublicToX25519(edPub)
	if err != nil {
		return nil, newError(CodeInvalidParams, fmt.Sprintf("DID key conversion: %v", err))
	}

	// Normalize endpoint: strip "tcp://" scheme if present.
	addr := strings.TrimPrefix(p.Endpoint, "tcp://")

	conn, err := transport.Dial("tcp", addr, s.id.PrivateKey(), remotePub)
	if err != nil {
		return nil, newError(CodePeerUnreachable, fmt.Sprintf("dial %s: %v", addr, err))
	}

	channelID := newChannelID()
	ch := &peerChannel{
		id:       channelID,
		conn:     conn,
		peerDID:  p.DID,
		openedAt: time.Now().UTC(),
	}

	s.peerMu.Lock()
	s.peerChannels[channelID] = ch
	s.peerMu.Unlock()

	// Auto-populate address book: record this peer as FirstSeen if not already
	// present, then Touch to update LastSeen and enforce TOFU on subsequent dials.
	// Best-effort: a nil or closed address book is silently skipped.
	if s.abook != nil {
		multibase := identity.MultibaseFragment(p.DID)
		// Add the peer as FirstSeen; ignore ErrEntryExists (already in book).
		_ = s.abook.Add(p.DID, "", multibase)
		// Touch records the pubkey and updates LastSeen. A TOFU violation (pubkey
		// changed since first sight) is surfaced as an audit warning but does NOT
		// abort the dial — the Noise handshake already authenticated the key; this
		// is a bookkeeping concern, not a security gate.
		if err := s.abook.Touch(p.DID, multibase); err != nil {
			s.auditPeer(ctx, activity.KindPeerChannelOpened, map[string]any{
				"channel_id": channelID,
				"peer_did":   p.DID,
				"endpoint":   p.Endpoint,
				"tofu_warn":  err.Error(),
			})
		} else {
			s.auditPeer(ctx, activity.KindPeerChannelOpened, map[string]any{
				"channel_id": channelID,
				"peer_did":   p.DID,
				"endpoint":   p.Endpoint,
			})
		}
		if err := s.abook.Save(); err != nil && s.logger != nil {
			s.logger.Printf("address book save after dial: %v", err)
		}
	} else {
		// Audit: peer.channel.opened (no address book)
		s.auditPeer(ctx, activity.KindPeerChannelOpened, map[string]any{
			"channel_id": channelID,
			"peer_did":   p.DID,
			"endpoint":   p.Endpoint,
		})
	}

	return peerDialResult{
		ChannelID: channelID,
		PeerDID:   p.DID,
		OpenedAt:  ch.openedAt.Format(time.RFC3339),
	}, nil
}

// --- daimon.peer.close -------------------------------------------------------

type peerCloseParams struct {
	ChannelID string `json:"channel_id"`
}

func (s *Server) handlePeerClose(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p peerCloseParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.ChannelID == "" {
		return nil, newError(CodeInvalidParams, "channel_id is required")
	}

	s.peerMu.Lock()
	ch, ok := s.peerChannels[p.ChannelID]
	if !ok {
		s.peerMu.Unlock()
		return nil, newError(CodeNotFound, fmt.Sprintf("channel not found: %s", p.ChannelID))
	}
	delete(s.peerChannels, p.ChannelID)
	s.peerMu.Unlock()

	_ = ch.conn.Close()

	s.auditPeer(ctx, activity.KindPeerChannelClosed, map[string]any{
		"channel_id": p.ChannelID,
		"peer_did":   ch.peerDID,
	})

	return struct{}{}, nil
}

// --- daimon.peer.list --------------------------------------------------------

// peerChannelWire is one row in the peer.list response.
//
// v0.4 dogfood-polish: added Direction + PeerX25519. Direction is
// "outbound" (we dialed) or "inbound" (we accepted). PeerX25519 is
// the transport public key as lowercase hex — always populated;
// useful as a stable identifier for first-contact peers whose DID
// hasn't been registered in the address book yet.
//
// peer_did is empty for inbound channels from unknown peers; the
// dialer can self-announce a DID later (e.g. via peer.dial-equivalent
// signalling), but in v0.3+ we just resolve via address book lookup.
type peerChannelWire struct {
	ChannelID  string `json:"channel_id"`
	PeerDID    string `json:"peer_did"`
	OpenedAt   string `json:"opened_at"`
	Direction  string `json:"direction"`
	PeerX25519 string `json:"peer_x25519"`
}

type peerListResult struct {
	Channels []peerChannelWire `json:"channels"`
}

// handlePeerList returns both outbound channels (this daimon dialed
// somewhere) and inbound channels (this daimon was dialed). v0.4
// dogfood-polish: prior to this, inbound channels were invisible — the
// listener could serve peer.echo + peer.ask + peer.pay.required just
// fine, but had no way to answer "who's currently connected to me?",
// which is the listener-side counterpart of the dialer's daimon peer
// list. Now both sides are observable.
func (s *Server) handlePeerList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	s.peerMu.Lock()
	out := make([]peerChannelWire, 0, len(s.peerChannels)+len(s.inboundChannels))
	for _, ch := range s.peerChannels {
		out = append(out, peerChannelWire{
			ChannelID:  ch.id,
			PeerDID:    ch.peerDID,
			OpenedAt:   ch.openedAt.Format(time.RFC3339),
			Direction:  "outbound",
			PeerX25519: fmt.Sprintf("%x", ch.conn.PeerX25519),
		})
	}
	// Inbound channels: peer_did resolved from the address book when
	// known (pinned/blocked/first_seen peers). Empty when the dialer
	// hasn't been registered locally — peer_x25519 still uniquely
	// identifies the transport endpoint either way.
	for _, ch := range s.inboundChannels {
		peerDID := ""
		if entry := s.lookupPeerByX25519(ch.peerX25519); entry != nil {
			peerDID = entry.DID
		}
		out = append(out, peerChannelWire{
			ChannelID:  ch.id,
			PeerDID:    peerDID,
			OpenedAt:   ch.openedAt.Format(time.RFC3339),
			Direction:  "inbound",
			PeerX25519: fmt.Sprintf("%x", ch.peerX25519),
		})
	}
	s.peerMu.Unlock()
	return peerListResult{Channels: out}, nil
}

// --- daimon.peer.invoke -------------------------------------------------------

type peerInvokeParams struct {
	ChannelID string `json:"channel_id"`
	Method    string `json:"method"`
	// Params is optional; nil sends no params to the peer.
	Params json.RawMessage `json:"params,omitempty"`
}

type peerInvokeResult struct {
	// Result is the raw JSON result from the remote peer.
	Result json.RawMessage `json:"result,omitempty"`
}

func (s *Server) handlePeerInvoke(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p peerInvokeParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.ChannelID == "" {
		return nil, newError(CodeInvalidParams, "channel_id is required")
	}
	if p.Method == "" {
		return nil, newError(CodeInvalidParams, "method is required")
	}

	s.peerMu.Lock()
	ch, ok := s.peerChannels[p.ChannelID]
	s.peerMu.Unlock()
	if !ok {
		return nil, newError(CodeNotFound, fmt.Sprintf("channel not found: %s", p.ChannelID))
	}

	// Build and send the peer JSON-RPC request.
	reqID := newChannelID() // unique request ID
	req := Request{
		JSONRPC: JSONRPCVersion,
		Method:  p.Method,
		Params:  p.Params,
		ID:      json.RawMessage(`"` + reqID + `"`),
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, newError(CodeInternalError, "marshal peer request", err.Error())
	}
	if err := ch.conn.SendFrame(reqBytes); err != nil {
		// Channel is broken — remove it from the map.
		s.peerMu.Lock()
		delete(s.peerChannels, p.ChannelID)
		s.peerMu.Unlock()
		return nil, newError(CodePeerUnreachable, fmt.Sprintf("send to peer: %v", err))
	}

	// Wait for the peer's response frame.
	respBytes, err := ch.conn.RecvFrame()
	if err != nil {
		s.peerMu.Lock()
		delete(s.peerChannels, p.ChannelID)
		s.peerMu.Unlock()
		return nil, newError(CodePeerUnreachable, fmt.Sprintf("recv from peer: %v", err))
	}
	// Use a dedicated struct with json.RawMessage so the peer's result is
	// preserved verbatim rather than round-tripped through interface{}.
	var peerResp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *RPCError       `json:"error,omitempty"`
		ID      json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(respBytes, &peerResp); err != nil {
		return nil, newError(CodeInternalError, "unmarshal peer response", err.Error())
	}
	if peerResp.Error != nil {
		// Propagate the peer's RPC error as our own internal error.
		return nil, newError(CodeInternalError,
			fmt.Sprintf("peer returned error: %s", peerResp.Error.Message))
	}

	// Audit: peer.invoke.sent
	s.auditPeer(ctx, activity.KindPeerInvokeSent, map[string]any{
		"channel_id": p.ChannelID,
		"peer_did":   ch.peerDID,
		"method":     p.Method,
	})

	return peerInvokeResult{Result: peerResp.Result}, nil
}

// --- peer.echo (inbound, served to remote peers) ----------------------------

// peerEchoParams is the request shape for the peer.echo verb.
// The remote peer sends {"message": "..."} and gets it back with "from_did".
type peerEchoParams struct {
	Message string `json:"message"`
}

// peerEchoResult is the response shape.
type peerEchoResult struct {
	Message string `json:"message"`
	FromDID string `json:"from_did"`
}

// handlePeerEcho serves the peer.echo verb from inbound peer connections.
// Called by dispatchPeer when a remote daimon invokes peer.echo on us.
func (s *Server) handlePeerEcho(conn *transport.Conn, params json.RawMessage) (any, *RPCError) {
	var p peerEchoParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}

	// Audit: peer.invoke.received (best-effort).
	// We use a background context because dispatchPeer doesn't carry one.
	//
	// Dogfood finding (2026-05-25): the listener daemon serves the request
	// silently — there's no terminal on this side, so the audit log IS the
	// inbox. We include the echo message so `daimon activity query --kind
	// peer.invoke.received --json` actually shows the caller what was said,
	// and resolve the caller's DID from the address book when known so the
	// payload is human-readable (peer_x25519 alone is just a hex blob).
	if s.alog != nil {
		callerDID := ""
		if entry := s.lookupPeerByX25519(conn.PeerX25519); entry != nil {
			callerDID = entry.DID
		}
		payload := map[string]any{
			"method":      "peer.echo",
			"peer_x25519": fmt.Sprintf("%x", conn.PeerX25519),
			"message":     p.Message,
		}
		if callerDID != "" {
			payload["caller_did"] = callerDID
		}
		go func() {
			_, _ = s.alog.Append(context.Background(), activity.KindPeerInvokeReceived, payload)
		}()
	}

	return peerEchoResult{
		Message: p.Message,
		FromDID: s.id.DID(),
	}, nil
}

// --- peer.ask (inbound, served to authorized remote peers) ------------------

// peerAskParams is the inbound wire shape for peer.ask.
// The remote peer identifies which of OUR providers to call and passes a
// normalized provider.Request. inject_context is deliberately NOT supported
// in peer.ask v0.3 — the peer should build its own retrieval context before
// asking us to invoke the LLM.
//
// v0.4 adds optional capability-token fields. When capability_token is
// present the serving daimon verifies it cryptographically (Biscuit v3)
// before consulting the address book. capability_token_id is used only
// for revocation checking and call-count tracking — it is not embedded
// in the Biscuit bytes themselves and may be omitted for attenuated tokens
// whose ID the caller doesn't know.
//
// request_receipt=true requests a signed reputation receipt attached to
// the response (phase 44).
type peerAskParams struct {
	Provider          string           `json:"provider"`
	Request           provider.Request `json:"request"`
	CapabilityToken   string           `json:"capability_token,omitempty"`
	CapabilityTokenID string           `json:"capability_token_id,omitempty"`
	RequestReceipt    bool             `json:"request_receipt,omitempty"`
}

// peerAskResult carries the provider response back to the calling peer.
// Receipt is non-nil when the caller set request_receipt=true and the
// serving daimon was able to sign a proof-of-service receipt.
type peerAskResult struct {
	Response *provider.Response  `json:"response"`
	Receipt  *reputation.Receipt `json:"receipt,omitempty"`
}

// handlePeerAsk serves the peer.ask verb from inbound authorized peer connections.
// Called by dispatchPeer AFTER it has confirmed authorization (address-book pin
// or a valid Biscuit capability token) — this handler does NOT re-check auth.
//
// entry is the address book entry for the calling peer (already looked up by
// the dispatcher); it may be nil when the caller authenticated via a capability
// token only.  It is used for the audit payload and receipt caller_did.
func (s *Server) handlePeerAsk(conn *transport.Conn, entry *addressbook.Entry, params json.RawMessage) (any, *RPCError) {
	var p peerAskParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Provider == "" {
		return nil, newError(CodeInvalidParams, "provider is required")
	}
	if len(p.Request.Messages) == 0 {
		return nil, newError(CodeInvalidParams, "request.messages is required")
	}
	if s.providers == nil {
		return nil, newError(CodeNotFound, "no provider registry on this daimon")
	}
	prov, err := s.providers.Get(p.Provider)
	if err != nil {
		return nil, newError(CodeNotFound, fmt.Sprintf("provider %q not found: %v", p.Provider, err))
	}

	start := time.Now()
	resp, err := prov.Invoke(context.Background(), p.Request)
	elapsed := time.Since(start)
	if err != nil {
		return nil, newError(CodeInternalError, fmt.Sprintf("peer.ask: provider %s: %v", p.Provider, err))
	}

	callerDID := ""
	if entry != nil {
		callerDID = entry.DID
	}

	// Audit: peer.invoke.served — logged asynchronously because dispatchPeer
	// doesn't carry a context.
	if s.alog != nil {
		go func() {
			_, _ = s.alog.Append(context.Background(), activity.KindPeerInvokeServed, map[string]any{
				"peer_did":      callerDID,
				"peer_x25519":   fmt.Sprintf("%x", conn.PeerX25519),
				"provider":      p.Provider,
				"model":         resp.Model,
				"input_tokens":  resp.Usage.InputTokens,
				"output_tokens": resp.Usage.OutputTokens,
				"stop_reason":   string(resp.StopReason),
				"duration_ms":   elapsed.Milliseconds(),
			})
		}()
	}

	result := peerAskResult{Response: resp}

	// Receipt: issue a signed proof-of-service when the caller requested one.
	// Requires a loaded identity for signing; silently skips on failure so the
	// provider response is never lost due to a receipt error.
	if p.RequestReceipt && s.id != nil {
		rc := &reputation.Receipt{
			ReceiptID:    ulid.Make().String(),
			ServedAt:     reputation.FormatTime(start.Add(elapsed)), // wall time at completion
			Verb:         "peer.ask",
			ServerDID:    s.id.DID(),
			CallerDID:    callerDID,
			Provider:     p.Provider,
			Model:        resp.Model,
			InputTokens:  int64(resp.Usage.InputTokens),
			OutputTokens: int64(resp.Usage.OutputTokens),
			DurationMS:   elapsed.Milliseconds(),
		}
		if signErr := reputation.Sign(s.id.PrivateKey(), rc); signErr == nil {
			result.Receipt = rc

			// Persist asynchronously so DB latency doesn't block the response.
			if s.capDB != nil {
				go func() {
					sigBytes, _ := base64.RawURLEncoding.DecodeString(rc.Signature)
					_ = s.capDB.RecordReceipt(context.Background(), capability.Receipt{
						ReceiptID:    rc.ReceiptID,
						Direction:    capability.ReceiptIssued,
						ServedAt:     start.Add(elapsed),
						ServedVerb:   rc.Verb,
						CallerDID:    rc.CallerDID,
						ServerDID:    rc.ServerDID,
						Provider:     rc.Provider,
						Model:        rc.Model,
						InputTokens:  rc.InputTokens,
						OutputTokens: rc.OutputTokens,
						DurationMS:   rc.DurationMS,
						Signature:    sigBytes,
					})
				}()
			}

			// Audit receipt issuance.
			s.auditCapability(context.Background(), activity.KindReputationReceiptIssued, map[string]any{
				"receipt_id": rc.ReceiptID,
				"caller_did": rc.CallerDID,
				"model":      rc.Model,
			})
		}
	}

	return result, nil
}


// --- peer.pay.required (inbound, universally available) ----------------------

// peerPayRequiredParams is the inbound wire shape for peer.pay.required.
// The calling peer names the service it wants to pay for; in v0.3 only
// "peer.ask" has payment requirements.
type peerPayRequiredParams struct {
	Service string `json:"service"` // e.g. "peer.ask"
}

// peerPayRequiredResult carries the payment requirements back to the calling peer.
// The requirements slice follows the x402 v2 PaymentRequirements shape so a
// future SDK can feed it directly into the x402 payment flow without adaptation.
type peerPayRequiredResult struct {
	Requirements []payment.PaymentRequirements `json:"requirements"`
}

// handlePeerPayRequired serves the peer.pay.required verb.
//
// Authorization: universally available — no address book gate. Price
// discovery must be accessible BEFORE a peer can set up payment, so
// requiring prior authorization would be circular. The handler is
// read-only from the serving daimon's perspective: it returns what the
// daimon's wallet address is and how much to pay, without moving any funds.
//
// Wallet dependency: the serving daimon must have at least one EVM wallet
// configured. If s.wstore is nil or contains no EVM wallet, returns
// CodePeerProtocolUnsupported so the caller knows to configure a wallet
// first (via daimon.wallet.create).
//
// v0.3 hardcodes: USDC on Base Sepolia, 1.00 USDC (1 000 000 units, 6
// decimals), 300-second payment window. Phase 40.4 will make network,
// asset, and amount configurable.
func (s *Server) handlePeerPayRequired(conn *transport.Conn, params json.RawMessage) (any, *RPCError) {
	var p peerPayRequiredParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Service == "" {
		return nil, newError(CodeInvalidParams, "service is required")
	}
	// Only peer.ask has payment requirements in v0.3. Other verbs (peer.echo,
	// peer.pay.required itself) are free; peer.ask is the only one that
	// consumes the serving daimon's provider API credits.
	if p.Service != "peer.ask" {
		return nil, newError(CodeInvalidParams,
			fmt.Sprintf("no payment requirements defined for service %q; only peer.ask is payable in v0.3", p.Service))
	}

	if s.wstore == nil {
		return nil, newError(CodePeerProtocolUnsupported,
			"peer.pay.required: no wallet configured on this daimon; "+
				"create one with daimon.wallet.create before accepting peer payments")
	}

	// Resolve the payment address: prefer Base Sepolia (testnet), fall back
	// to any EVM wallet in the store. Phase 40.4 will expose a multi-chain
	// requirements list; for v0.3 we advertise exactly one entry.
	const baseSepolia = "evm:base-sepolia"
	w, err := s.wstore.FindByChain(baseSepolia)
	if err != nil {
		// Fallback: scan for any EVM wallet.
		for _, candidate := range s.wstore.List() {
			if strings.HasPrefix(candidate.Chain, "evm:") {
				w = candidate
				break
			}
		}
	}
	if w == nil {
		return nil, newError(CodePeerProtocolUnsupported,
			"peer.pay.required: no EVM wallet found; "+
				"create one with daimon.wallet.create (chain: evm:base-sepolia)")
	}

	// v0.3 constants: USDC on Base Sepolia.
	const (
		// usdcBaseSepolia is the canonical USDC token contract on Base Sepolia
		// (https://docs.base.org/docs/tokens/usdc). Used as the Asset field.
		usdcBaseSepolia = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
		// defaultAmount is 1.00 USDC expressed in the 6-decimal smallest unit.
		defaultAmount = "1000000"
		// defaultTimeout is the maximum seconds the serving daimon will accept
		// a payment that was authorised before "now". Matches x402's recommended
		// 5-minute window for short-lived authorizations.
		defaultTimeout = 300
	)

	reqs := []payment.PaymentRequirements{
		{
			Scheme:            payment.SchemeExact,
			Network:           "base-sepolia",
			MaxAmountRequired: defaultAmount,
			Resource:          "peer.ask",
			Description:       "1.00 USDC payment required to invoke peer.ask on this daimon (Base Sepolia testnet)",
			PayTo:             w.Address,
			MaxTimeoutSeconds: defaultTimeout,
			Asset:             usdcBaseSepolia,
		},
	}

	// Audit: peer.payment.invoiced — best-effort, async (dispatchPeer
	// carries no context, and the audit must not block the response).
	// As with peer.echo, resolve the caller's DID from the address book
	// when known so the audit row is human-readable, not just a hex blob.
	if s.alog != nil {
		callerDID := ""
		if entry := s.lookupPeerByX25519(conn.PeerX25519); entry != nil {
			callerDID = entry.DID
		}
		payload := map[string]any{
			"service":     p.Service,
			"peer_x25519": fmt.Sprintf("%x", conn.PeerX25519),
			"pay_to":      w.Address,
			"amount":      defaultAmount,
			"network":     "base-sepolia",
			"asset":       usdcBaseSepolia,
		}
		if callerDID != "" {
			payload["caller_did"] = callerDID
		}
		go func() {
			_, _ = s.alog.Append(context.Background(), activity.KindPeerPaymentInvoiced, payload)
		}()
	}

	return peerPayRequiredResult{Requirements: reqs}, nil
}

// --- helpers -----------------------------------------------------------------

// auditPeer appends a peer-federation audit row. Best-effort: a failed
// append logs a warning but does not fail the RPC.
func (s *Server) auditPeer(ctx context.Context, kind activity.Kind, payload map[string]any) {
	if s.alog == nil {
		return
	}
	if _, err := s.alog.Append(ctx, kind, payload); err != nil && s.logger != nil {
		s.logger.Printf("audit %s failed: %v", kind, err)
	}
}

// mapPeerError translates peer connection errors to RPC error codes.
func mapPeerError(err error, op string) *RPCError {
	switch {
	case errors.Is(err, transport.ErrConnClosed):
		return newError(CodePeerUnreachable, fmt.Sprintf("peer.%s: connection closed", op))
	}
	return newError(CodePeerUnreachable, fmt.Sprintf("peer.%s: %v", op, err))
}
