package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/wallet"
)

// Wallet handlers — daimon.wallet.{list, create, address, sign}.
//
// SPEC §6.1.X (proposed, v0.2 draft — see design/v0.2-wallet.md). All wallet
// methods require the daemon to be unlocked AND the wallet keystore to be
// loaded. The keystore is auto-created by the unlock callback the first time
// `daimon unlock` runs, so under normal lifecycle wstore is always populated
// after the gate flips; the nil check exists to surface a clear error if a
// non-fatal wallet-keystore load failure ever leaves the daemon unlocked but
// wallet-less.

// walletNotReady is the standard error for handlers that need wstore but
// don't have it. CodeInvalidRequest is the right shape — the daemon IS
// unlocked, but the wallet subsystem isn't available, which from the
// caller's perspective is a precondition failure rather than a not-found.
func walletNotReady() *RPCError {
	return newError(CodeInvalidRequest, "wallet keystore not loaded; check daemon logs for keystore open failures")
}

// --- daimon.wallet.list ------------------------------------------------------

type walletEntry struct {
	ID        string `json:"id"`
	Chain     string `json:"chain"`
	Path      string `json:"path"`
	Address   string `json:"address"`
	PubKey    string `json:"pubkey"`
	CreatedAt int64  `json:"created_at"`
}

func toWalletEntry(w *wallet.Wallet) walletEntry {
	return walletEntry{
		ID:        w.ID,
		Chain:     w.Chain,
		Path:      w.Path,
		Address:   w.Address,
		PubKey:    w.PubKey,
		CreatedAt: w.CreatedAt,
	}
}

func (s *Server) handleWalletList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	if s.wstore == nil {
		return nil, walletNotReady()
	}
	ws := s.wstore.List()
	out := make([]walletEntry, 0, len(ws))
	for _, w := range ws {
		out = append(out, toWalletEntry(w))
	}
	return out, nil
}

// --- daimon.wallet.create ----------------------------------------------------

type walletCreateParams struct {
	Chain string `json:"chain"`
}

func (s *Server) handleWalletCreate(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.wstore == nil {
		return nil, walletNotReady()
	}
	var p walletCreateParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Chain == "" {
		return nil, newError(CodeInvalidParams, "chain is required")
	}
	w, err := s.wstore.CreateWallet(p.Chain)
	switch {
	case errors.Is(err, wallet.ErrUnsupportedChain):
		return nil, newError(CodeInvalidParams, "unsupported chain", p.Chain)
	case errors.Is(err, wallet.ErrChainAlreadyExists):
		return nil, newError(CodeInvalidParams, "wallet for this chain already exists", p.Chain)
	case err != nil:
		return nil, newError(CodeInternalError, "create wallet", err.Error())
	}

	// Audit-log the creation. Payload mirrors the on-disk Wallet shape minus
	// the path (which is implementation detail of HD derivation, not
	// user-facing data the audit trail needs to surface).
	if _, aerr := s.alog.Append(ctx, activity.KindWalletCreated, map[string]any{
		"id":      w.ID,
		"chain":   w.Chain,
		"address": w.Address,
		"pubkey":  w.PubKey,
	}); aerr != nil {
		s.logf("activity append (wallet.created id=%s): %v", w.ID, aerr)
	}

	return toWalletEntry(w), nil
}

// --- daimon.wallet.address ---------------------------------------------------

type walletAddressParams struct {
	Chain string `json:"chain"`
}

type walletAddressResult struct {
	Address string `json:"address"`
}

func (s *Server) handleWalletAddress(_ context.Context, params json.RawMessage) (any, *RPCError) {
	if s.wstore == nil {
		return nil, walletNotReady()
	}
	var p walletAddressParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Chain == "" {
		return nil, newError(CodeInvalidParams, "chain is required")
	}
	w, err := s.wstore.FindByChain(p.Chain)
	if errors.Is(err, wallet.ErrNotFound) {
		return nil, newError(CodeNotFound, "no wallet for chain", p.Chain)
	}
	if err != nil {
		return nil, newError(CodeInternalError, "find wallet", err.Error())
	}
	return walletAddressResult{Address: w.Address}, nil
}

// --- daimon.wallet.sign ------------------------------------------------------

// walletSignParams accepts a hex-encoded 32-byte digest. The handler returns
// a hex-encoded 65-byte EVM ECDSA signature [r || s || v] — the shape EIP-3009
// `transferWithAuthorization` verifiers expect.
//
// This is a low-level primitive surfaced for advanced/debug use. The
// canonical v0.2 client surface is `daimon.payment.pay` (phase 40.3), which
// internally builds the digest and calls into this signing layer without
// the caller having to know EIP-3009 framing.
type walletSignParams struct {
	Chain     string `json:"chain"`
	DigestHex string `json:"digest_hex"`
}

type walletSignResult struct {
	SignatureHex string `json:"signature_hex"`
}

func (s *Server) handleWalletSign(_ context.Context, params json.RawMessage) (any, *RPCError) {
	if s.wstore == nil {
		return nil, walletNotReady()
	}
	var p walletSignParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Chain == "" {
		return nil, newError(CodeInvalidParams, "chain is required")
	}
	if p.DigestHex == "" {
		return nil, newError(CodeInvalidParams, "digest_hex is required")
	}
	// 0x prefix is optional — strip it if present so callers can hand us the
	// EVM-conventional 0x-prefixed form without an explicit conversion step.
	hexStr := p.DigestHex
	if len(hexStr) >= 2 && hexStr[:2] == "0x" {
		hexStr = hexStr[2:]
	}
	digest, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, newError(CodeInvalidParams, "digest_hex is not valid hex", err.Error())
	}
	sig, err := s.wstore.SignDigest(p.Chain, digest)
	switch {
	case errors.Is(err, wallet.ErrInvalidDigest):
		return nil, newError(CodeInvalidParams, "digest must be 32 bytes")
	case errors.Is(err, wallet.ErrNotFound):
		return nil, newError(CodeNotFound, "no wallet for chain", p.Chain)
	case errors.Is(err, wallet.ErrUnsupportedChain):
		return nil, newError(CodeInvalidParams, "unsupported chain", p.Chain)
	case err != nil:
		return nil, newError(CodeInternalError, "sign digest", err.Error())
	}
	return walletSignResult{SignatureHex: "0x" + hex.EncodeToString(sig)}, nil
}
