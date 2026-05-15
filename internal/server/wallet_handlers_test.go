package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/wallet"
)

// walletFixture wraps a base demo-mode fixture and adds a wallet keystore
// loaded into the server. Returns (fixture, wstore) so the test can inspect
// the wallet store directly if needed.
//
// The keystore lives in t.TempDir() — separate from the identity/memory
// fixture's tempdir is fine because nothing cross-references them.
func newWalletFixture(t *testing.T) (*fixture, *wallet.Store) {
	t.Helper()
	f := newFixture(t)
	wpath := filepath.Join(t.TempDir(), "wallet.keystore")
	ws, _, err := wallet.Open(wpath, []byte("test-password"))
	if err != nil {
		t.Fatalf("wallet.Open: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	// The base fixture built a Server without a wallet — inject one here.
	// The server's wstore is unexported but we're in the same package.
	f.srv.wstore = ws
	return f, ws
}

// --- handler-level dispatch tests -------------------------------------------

func TestHandleWalletList_EmptyAtCreation(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.wallet.list", nil)
	var out []walletEntry
	resultAs(t, resp, &out)
	if len(out) != 0 {
		t.Fatalf("expected 0 wallets in fresh keystore, got %d", len(out))
	}
}

func TestHandleWalletCreate_ReturnsEVMAddress(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	var w walletEntry
	resultAs(t, resp, &w)
	if w.Chain != "evm:base" {
		t.Fatalf("chain = %q, want evm:base", w.Chain)
	}
	if len(w.Address) != 42 || w.Address[:2] != "0x" {
		t.Fatalf("address %q is not a valid EVM address", w.Address)
	}
	if w.Path == "" || w.PubKey == "" || w.ID == "" {
		t.Fatalf("required fields missing in result: %+v", w)
	}
}

func TestHandleWalletCreate_AuditLogsWalletCreated(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	mustNoError(t, resp)

	// Walk the activity log forward and assert a wallet.created entry is
	// present with the right payload fields. Genesis row is first; then
	// our wallet.created.
	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{})
	if err != nil {
		t.Fatalf("activity.Query: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Kind != activity.KindWalletCreated {
			continue
		}
		found = true
		var payload map[string]any
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if got, _ := payload["chain"].(string); got != "evm:base" {
			t.Fatalf("wallet.created payload.chain = %q, want evm:base", got)
		}
		if got, _ := payload["address"].(string); got == "" {
			t.Fatalf("wallet.created payload.address is empty")
		}
		break
	}
	if !found {
		t.Fatalf("no wallet.created entry in activity log; got %d entries", len(entries))
	}
}

func TestHandleWalletCreate_RejectsUnsupportedChain(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "stellar"})
	if resp.Error == nil {
		t.Fatal("expected error for unsupported chain")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error code = %d, want CodeInvalidParams (%d)", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandleWalletCreate_RejectsDuplicateChain(t *testing.T) {
	f, _ := newWalletFixture(t)
	// First create succeeds.
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	mustNoError(t, resp)
	// Second for the same chain fails.
	resp = f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	if resp.Error == nil {
		t.Fatal("expected error for duplicate chain")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error code = %d, want CodeInvalidParams", resp.Error.Code)
	}
}

func TestHandleWalletAddress_ReturnsAddressForExistingChain(t *testing.T) {
	f, _ := newWalletFixture(t)
	// Create a wallet first.
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	var created walletEntry
	resultAs(t, resp, &created)

	// Now look it up by chain.
	resp = f.call(t, "daimon.wallet.address", map[string]any{"chain": "evm:base"})
	var out walletAddressResult
	resultAs(t, resp, &out)
	if out.Address != created.Address {
		t.Fatalf("address mismatch: address handler returned %s, create returned %s", out.Address, created.Address)
	}
}

func TestHandleWalletAddress_NotFound(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.wallet.address", map[string]any{"chain": "evm:mainnet"})
	if resp.Error == nil {
		t.Fatal("expected not-found error")
	}
	if resp.Error.Code != CodeNotFound {
		t.Fatalf("error code = %d, want CodeNotFound (%d)", resp.Error.Code, CodeNotFound)
	}
}

func TestHandleWalletSign_ProducesValidSignature(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	mustNoError(t, resp)

	// Sign a 32-byte digest (a SHA-256 hash for simplicity — the wallet
	// just treats it as opaque 32 bytes).
	digest := make([]byte, 32)
	for i := range digest {
		digest[i] = byte(i * 7) // arbitrary fixed bytes
	}
	resp = f.call(t, "daimon.wallet.sign", map[string]any{
		"chain":      "evm:base",
		"digest_hex": "0x" + hex.EncodeToString(digest),
	})
	var out walletSignResult
	resultAs(t, resp, &out)
	// Strip 0x and check length: 65 bytes = 130 hex chars.
	hexBody := out.SignatureHex[2:]
	if len(hexBody) != 130 {
		t.Fatalf("signature length = %d hex chars, want 130 (65 bytes)", len(hexBody))
	}
}

func TestHandleWalletSign_RejectsBadDigest(t *testing.T) {
	f, _ := newWalletFixture(t)
	_ = f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})

	for _, c := range []struct {
		name string
		hex  string
	}{
		{"empty", ""},
		{"not_hex", "ZZZ"},
		{"wrong_length", "ab"},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := f.call(t, "daimon.wallet.sign", map[string]any{
				"chain":      "evm:base",
				"digest_hex": c.hex,
			})
			if resp.Error == nil {
				t.Fatalf("expected error for digest=%q", c.hex)
			}
			if resp.Error.Code != CodeInvalidParams {
				t.Fatalf("error code = %d, want CodeInvalidParams", resp.Error.Code)
			}
		})
	}
}

// --- nil-wstore behaviour ---------------------------------------------------

// When the daemon is unlocked but the wallet keystore failed to load, all
// wallet RPCs must surface a clear "wallet not loaded" error rather than
// nil-panicking or returning empty data. The base newFixture builds a
// server with wstore=nil, so it's the right vehicle here.
func TestWalletRPCs_RejectWhenWalletNotLoaded(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		method string
		params any
	}{
		{"daimon.wallet.list", nil},
		{"daimon.wallet.create", map[string]any{"chain": "evm:base"}},
		{"daimon.wallet.address", map[string]any{"chain": "evm:base"}},
		{"daimon.wallet.sign", map[string]any{"chain": "evm:base", "digest_hex": "00"}},
	} {
		t.Run(c.method, func(t *testing.T) {
			resp := f.call(t, c.method, c.params)
			if resp.Error == nil {
				t.Fatalf("%s: expected error when wstore is nil", c.method)
			}
			if resp.Error.Code != CodeInvalidRequest {
				t.Fatalf("%s: error code = %d, want CodeInvalidRequest (%d)", c.method, resp.Error.Code, CodeInvalidRequest)
			}
		})
	}
}

// --- helpers -----------------------------------------------------------------

func mustNoError(t *testing.T, r *Response) {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("RPC error: code=%d msg=%q data=%v", r.Error.Code, r.Error.Message, r.Error.Data)
	}
}
