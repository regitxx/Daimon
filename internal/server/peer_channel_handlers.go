package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
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

	// Audit: peer.channel.opened
	s.auditPeer(ctx, activity.KindPeerChannelOpened, map[string]any{
		"channel_id": channelID,
		"peer_did":   p.DID,
		"endpoint":   p.Endpoint,
	})

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
