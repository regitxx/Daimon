package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/addressbook"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/provider"
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

type peerChannelWire struct {
	ChannelID string `json:"channel_id"`
	PeerDID   string `json:"peer_did"`
	OpenedAt  string `json:"opened_at"`
}

type peerListResult struct {
	Channels []peerChannelWire `json:"channels"`
}

func (s *Server) handlePeerList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	s.peerMu.Lock()
	out := make([]peerChannelWire, 0, len(s.peerChannels))
	for _, ch := range s.peerChannels {
		out = append(out, peerChannelWire{
			ChannelID: ch.id,
			PeerDID:   ch.peerDID,
			OpenedAt:  ch.openedAt.Format(time.RFC3339),
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
	if s.alog != nil {
		go func() {
			_, _ = s.alog.Append(context.Background(), activity.KindPeerInvokeReceived, map[string]any{
				"method":      "peer.echo",
				"peer_x25519": fmt.Sprintf("%x", conn.PeerX25519),
			})
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
type peerAskParams struct {
	Provider string           `json:"provider"`
	Request  provider.Request `json:"request"`
}

// peerAskResult carries the provider response back to the calling peer.
type peerAskResult struct {
	Response *provider.Response `json:"response"`
}

// handlePeerAsk serves the peer.ask verb from inbound authorized peer connections.
// Called by dispatchPeer AFTER it has confirmed the peer is in the address book
// with HasVerb("peer.ask") — this handler does NOT re-check authorization.
//
// entry is the address book entry for the calling peer (already looked up and
// verified by the dispatcher); it is used for the audit payload.
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

	// Audit: peer.invoke.served — the "I consumed my provider on behalf of a peer" row.
	// Logged asynchronously (same pattern as peer.invoke.received) because
	// dispatchPeer doesn't carry a context.
	if s.alog != nil {
		peerDID := ""
		if entry != nil {
			peerDID = entry.DID
		}
		go func() {
			_, _ = s.alog.Append(context.Background(), activity.KindPeerInvokeServed, map[string]any{
				"peer_did":      peerDID,
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

	return peerAskResult{Response: resp}, nil
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
