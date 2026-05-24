package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/regitxx/Daimon/internal/capability"
	"github.com/regitxx/Daimon/internal/reputation"
)

// daimon.reputation.* RPC handlers (v0.4 phase 44).
//
// These handlers expose the signed proof-of-service receipts that the serving
// daimon issues alongside peer.ask responses when request_receipt=true.
//
// Two directions are tracked in capability.db:
//   - "issued"   — receipts THIS daimon signed and returned to calling peers
//   - "received" — receipts returned to US by remote peers we called
//     (storing the received side is a future phase; the DB table is ready)

func capabilityDBNotReadyForReputation() *RPCError {
	return newError(CodeInvalidRequest, "capability store not loaded; reputation receipts unavailable")
}

// ---------------------------------------------------------------------------
// daimon.reputation.receipts
// ---------------------------------------------------------------------------

type reputationReceiptsParams struct {
	// Direction filters which receipts to return.
	// "issued"   — only receipts this daimon signed for inbound peer.ask calls
	// "received" — only receipts returned to this daimon by remote peers
	// ""         — both (default)
	Direction string `json:"direction,omitempty"`
}

type reputationReceiptWire struct {
	ReceiptID    string `json:"receipt_id"`
	Direction    string `json:"direction"`
	ServedAt     string `json:"served_at"`
	Verb         string `json:"verb"`
	ServerDID    string `json:"server_did"`
	CallerDID    string `json:"caller_did,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	DurationMS   int64  `json:"duration_ms"`
	Signature    string `json:"signature,omitempty"` // base64url; empty = not yet verified
}

type reputationReceiptsResult struct {
	Receipts []reputationReceiptWire `json:"receipts"`
}

func receiptToWire(r capability.Receipt) reputationReceiptWire {
	sig := ""
	if len(r.Signature) > 0 {
		sig = base64.RawURLEncoding.EncodeToString(r.Signature)
	}
	servedAt := ""
	if !r.ServedAt.IsZero() {
		servedAt = reputation.FormatTime(r.ServedAt)
	}
	return reputationReceiptWire{
		ReceiptID:    r.ReceiptID,
		Direction:    string(r.Direction),
		ServedAt:     servedAt,
		Verb:         r.ServedVerb,
		ServerDID:    r.ServerDID,
		CallerDID:    r.CallerDID,
		Provider:     r.Provider,
		Model:        r.Model,
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		DurationMS:   r.DurationMS,
		Signature:    sig,
	}
}

func (s *Server) handleReputationReceipts(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.capDB == nil {
		return nil, capabilityDBNotReadyForReputation()
	}

	var p reputationReceiptsParams
	if len(params) > 0 && string(params) != "null" {
		if rpcErr := decodeParams(params, &p); rpcErr != nil {
			return nil, rpcErr
		}
	}

	if p.Direction != "" &&
		p.Direction != string(capability.ReceiptIssued) &&
		p.Direction != string(capability.ReceiptReceived) {
		return nil, newError(CodeInvalidParams,
			fmt.Sprintf("direction must be %q, %q, or omitted", capability.ReceiptIssued, capability.ReceiptReceived))
	}

	rows, err := s.capDB.ListReceipts(ctx, capability.ReceiptDirection(p.Direction))
	if err != nil {
		return nil, newError(CodeInternalError, fmt.Sprintf("reputation.receipts: %v", err))
	}

	result := reputationReceiptsResult{Receipts: []reputationReceiptWire{}}
	for _, r := range rows {
		result.Receipts = append(result.Receipts, receiptToWire(r))
	}
	return result, nil
}
