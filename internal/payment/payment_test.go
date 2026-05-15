package payment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/wallet"
)

// --- EIP-712 / EIP-3009 hashing ---------------------------------------------

// Anchor the EIP-712 type-string hashes against precomputed values so a
// refactor of the typehash strings can't silently change the digest.
// These come from keccak256("EIP712Domain(...)") / keccak256("Transfer...")
// — recomputed any time you tweak the type strings.
func TestEIP712Domain_TypeHashStable(t *testing.T) {
	want := "8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f"
	got := hex.EncodeToString(eip712DomainTypeHash)
	if got != want {
		t.Fatalf("eip712DomainTypeHash drifted:\n  got  %s\n  want %s", got, want)
	}
}

func TestEIP3009_TypeHashStable(t *testing.T) {
	want := "7c7c6cdb67a18743f49ec6fa9b35f50d52ed05cbed4cc592e13b44501c1a2267"
	got := hex.EncodeToString(transferWithAuthorizationTypeHash)
	if got != want {
		t.Fatalf("transferWithAuthorizationTypeHash drifted:\n  got  %s\n  want %s", got, want)
	}
}

// padLeft32 + uint256 ABI encoding edge cases.
func TestUint256Bytes(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string // hex of 32 bytes
		wantErr bool
	}{
		{"zero", "0", strings.Repeat("00", 32), false},
		{"one", "1", strings.Repeat("00", 31) + "01", false},
		{"max_uint64", "18446744073709551615", strings.Repeat("00", 24) + "ffffffffffffffff", false},
		{"negative_rejected", "-1", "", true},
		{"non_decimal_rejected", "1x", "", true},
		// 2^256 -- one too large, should overflow.
		{
			"overflow_rejected",
			"115792089237316195423570985008687907853269984665640564039457584007913129639936",
			"", true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := uint256Bytes(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %x", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hex.EncodeToString(got) != c.want {
				t.Fatalf("encoding mismatch for %q:\n  got  %x\n  want %s", c.in, got, c.want)
			}
		})
	}
}

// Round-trip: digest is deterministic under the same inputs and changes
// when any field changes.
func TestEIP3009Digest_Determinism(t *testing.T) {
	chain, err := LookupByX402Network("base")
	if err != nil {
		t.Fatalf("LookupByX402Network: %v", err)
	}
	auth := EVMAuthorizationV2{
		From:        "0x0000000000000000000000000000000000000001",
		To:          "0x0000000000000000000000000000000000000002",
		Value:       "100000", // $0.10 USDC
		ValidAfter:  "0",
		ValidBefore: "1800000000",
		Nonce:       "0x" + strings.Repeat("ab", 32),
	}
	d1, err := EIP3009Digest(chain, auth)
	if err != nil {
		t.Fatalf("EIP3009Digest #1: %v", err)
	}
	d2, err := EIP3009Digest(chain, auth)
	if err != nil {
		t.Fatalf("EIP3009Digest #2: %v", err)
	}
	if !bytes.Equal(d1, d2) {
		t.Fatalf("digest not deterministic:\n  d1 %x\n  d2 %x", d1, d2)
	}
	if len(d1) != 32 {
		t.Fatalf("digest length = %d, want 32", len(d1))
	}

	// Flip a single byte in the nonce; digest must change.
	auth2 := auth
	auth2.Nonce = "0x" + strings.Repeat("ac", 32)
	d3, err := EIP3009Digest(chain, auth2)
	if err != nil {
		t.Fatalf("EIP3009Digest #3: %v", err)
	}
	if bytes.Equal(d1, d3) {
		t.Fatalf("digest unchanged after nonce flip — domain separator missing field?")
	}
}

// --- token registry ---------------------------------------------------------

func TestTokenRegistry_LookupRoundtrip(t *testing.T) {
	for _, c := range []struct{ x402, daimon string }{
		{"base", "evm:base"},
		{"base-sepolia", "evm:base-sepolia"},
		{"BASE", "evm:base"}, // case-insensitive on the x402 name
	} {
		t.Run(c.x402, func(t *testing.T) {
			info, err := LookupByX402Network(c.x402)
			if err != nil {
				t.Fatalf("LookupByX402Network(%q): %v", c.x402, err)
			}
			if info.DaimonChain != c.daimon {
				t.Fatalf("DaimonChain = %q, want %q", info.DaimonChain, c.daimon)
			}
			info2, err := LookupByDaimonChain(c.daimon)
			if err != nil {
				t.Fatalf("LookupByDaimonChain(%q): %v", c.daimon, err)
			}
			if info2.X402Network != info.X402Network {
				t.Fatalf("X402Network round-trip mismatch: %q != %q", info2.X402Network, info.X402Network)
			}
		})
	}
}

func TestTokenRegistry_UnknownNetwork(t *testing.T) {
	if _, err := LookupByX402Network("ethereum-classic"); !errors.Is(err, ErrUnknownNetwork) {
		t.Fatalf("expected ErrUnknownNetwork, got %v", err)
	}
}

// --- end-to-end against a mock HTTP server ----------------------------------

// mockServer accepts payment, validates the EIP-3009 signature recovers
// to the expected wallet address, and serves the resource on the retry.
type mockServer struct {
	t            *testing.T
	expectPayTo  string
	expectAsset  string
	expectAmount string
	chain        *ChainInfo
}

func (m *mockServer) handle402(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(HeaderPaymentSignature) == "" {
		// First request — emit a 402 with the requirements.
		env := PaymentRequiredEnvelope{
			X402Version: 1,
			Accepts: []PaymentRequirements{
				{
					Scheme:            SchemeExact,
					Network:           m.chain.X402Network,
					MaxAmountRequired: m.expectAmount,
					Resource:          "http://" + r.Host + r.URL.Path,
					Description:       "test paid endpoint",
					PayTo:             m.expectPayTo,
					MaxTimeoutSeconds: 60,
					Asset:             m.expectAsset,
				},
			},
		}
		raw, _ := json.Marshal(env)
		w.Header().Set(HeaderPaymentRequired, base64.StdEncoding.EncodeToString(raw))
		w.WriteHeader(http.StatusPaymentRequired)
		return
	}

	// Retry — verify the PAYMENT-SIGNATURE header.
	sigB64 := r.Header.Get(HeaderPaymentSignature)
	outerJSON, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		m.t.Fatalf("server: base64-decode PAYMENT-SIGNATURE: %v", err)
	}
	var outer PaymentPayload
	if err := json.Unmarshal(outerJSON, &outer); err != nil {
		m.t.Fatalf("server: unmarshal outer payload: %v", err)
	}
	if outer.Scheme != SchemeExact || outer.Network != m.chain.X402Network {
		m.t.Fatalf("server: outer scheme/network mismatch: %s/%s", outer.Scheme, outer.Network)
	}
	var inner EVMExactPayload
	if err := json.Unmarshal(outer.Payload, &inner); err != nil {
		m.t.Fatalf("server: unmarshal inner payload: %v", err)
	}

	// Sanity: structural fields.
	if !strings.EqualFold(inner.Authorization.To, m.expectPayTo) {
		m.t.Fatalf("server: authorization.to = %s, want %s", inner.Authorization.To, m.expectPayTo)
	}
	if inner.Authorization.Value != m.expectAmount {
		m.t.Fatalf("server: authorization.value = %s, want %s", inner.Authorization.Value, m.expectAmount)
	}

	// Verify the signature recovers to the from address. This is the
	// cryptographic property a real facilitator would check before
	// settling on-chain.
	digest, err := EIP3009Digest(m.chain, inner.Authorization)
	if err != nil {
		m.t.Fatalf("server: EIP3009Digest: %v", err)
	}
	sigBytes, err := hexToBytes(inner.Signature)
	if err != nil {
		m.t.Fatalf("server: signature hex decode: %v", err)
	}
	if len(sigBytes) != 65 {
		m.t.Fatalf("server: signature length = %d, want 65", len(sigBytes))
	}
	// Daimon's wallet emits [r || s || v] with v in {27, 28}; ecdsa.RecoverCompact
	// wants [v || r || s] in the same convention.
	compact := make([]byte, 65)
	compact[0] = sigBytes[64]
	copy(compact[1:33], sigBytes[0:32])
	copy(compact[33:65], sigBytes[32:64])
	pubKey, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		m.t.Fatalf("server: RecoverCompact: %v", err)
	}
	recoveredAddr, _, err := publicKeyEVMAddress(pubKey.SerializeUncompressed())
	if err != nil {
		m.t.Fatalf("server: pubkey to address: %v", err)
	}
	if !strings.EqualFold(recoveredAddr, inner.Authorization.From) {
		m.t.Fatalf("server: signature recovered to %s, but authorization.from = %s",
			recoveredAddr, inner.Authorization.From)
	}

	// All checks pass — emit a settled response.
	resp := PaymentResponse{
		Success:     true,
		Transaction: "0x" + strings.Repeat("aa", 32),
		Network:     m.chain.X402Network,
		Payer:       inner.Authorization.From,
	}
	raw, _ := json.Marshal(resp)
	w.Header().Set(HeaderPaymentResponse, base64.StdEncoding.EncodeToString(raw))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("paid resource body"))
}

// publicKeyEVMAddress is a local copy of the EIP-55-checksum derivation
// the wallet package uses internally. Duplicated here rather than
// exposed publicly because the mock-server side of the test is the only
// out-of-package caller in v0.2.
func publicKeyEVMAddress(uncompressed []byte) (string, []byte, error) {
	if len(uncompressed) != 65 || uncompressed[0] != 0x04 {
		return "", nil, errors.New("uncompressed pubkey must be 65 bytes starting with 0x04")
	}
	hashed := keccak256(uncompressed[1:])
	raw := hashed[12:]
	lowerHex := hex.EncodeToString(raw)
	checksumHash := keccak256([]byte(lowerHex))
	var sb strings.Builder
	sb.WriteString("0x")
	for i, c := range lowerHex {
		if c >= 'a' && c <= 'f' && (checksumHash[i/2]>>uint(4*(1-i%2)))&0xF >= 8 {
			sb.WriteRune(c - 32)
			continue
		}
		sb.WriteRune(c)
	}
	return sb.String(), nil, nil
}

// fixture wires a wallet store + activity log into the payment client.
type clientFixture struct {
	t        *testing.T
	wallet   *wallet.Store
	walletW  *wallet.Wallet
	alog     *activity.Log
	client   *Client
	mockSrv  *httptest.Server
	mock     *mockServer
}

func newClientFixture(t *testing.T) *clientFixture {
	t.Helper()
	dir := t.TempDir()
	walletPath := filepath.Join(dir, "wallet.keystore")
	ws, _, err := wallet.Open(walletPath, []byte("test-pw"))
	if err != nil {
		t.Fatalf("wallet.Open: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	w, err := ws.CreateWallet("evm:base")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}

	// Real activity log — encrypts payloads under an identity key, so we
	// need an identity to back it.
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	alog, err := activity.Open(filepath.Join(dir, "activity.log"), id)
	if err != nil {
		t.Fatalf("activity.Open: %v", err)
	}
	t.Cleanup(func() { _ = alog.Close() })

	chain, err := LookupByDaimonChain("evm:base")
	if err != nil {
		t.Fatalf("LookupByDaimonChain: %v", err)
	}

	mock := &mockServer{
		t:            t,
		expectPayTo:  "0xfFFf0000000000000000000000000000000000ff",
		expectAsset:  chain.USDCAddress,
		expectAmount: "100", // 100 smallest units; below the $0.10 ceiling
		chain:        chain,
	}
	srv := httptest.NewServer(http.HandlerFunc(mock.handle402))
	t.Cleanup(srv.Close)

	cl, err := NewClient(ws, alog, Config{
		HTTPClient:          srv.Client(),
		CeilingSmallestUnit: big.NewInt(100000), // $0.10 USDC
		Now:                 func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	return &clientFixture{
		t: t, wallet: ws, walletW: w, alog: alog, client: cl,
		mockSrv: srv, mock: mock,
	}
}

func TestClient_Do_HappyPath_402ToSettled(t *testing.T) {
	f := newClientFixture(t)
	req, err := http.NewRequestWithContext(context.Background(), "GET", f.mockSrv.URL+"/resource", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := f.client.Do(req.Context(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "paid resource body" {
		t.Fatalf("body = %q, want %q", string(body), "paid resource body")
	}

	// Audit log should now have: genesis + payment.signed + payment.settled.
	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{})
	if err != nil {
		t.Fatalf("activity.Query: %v", err)
	}
	gotKinds := map[activity.Kind]int{}
	for _, e := range entries {
		gotKinds[e.Kind]++
	}
	if gotKinds[activity.KindPaymentSigned] != 1 {
		t.Fatalf("expected exactly 1 payment.signed row, got %d", gotKinds[activity.KindPaymentSigned])
	}
	if gotKinds[activity.KindPaymentSettled] != 1 {
		t.Fatalf("expected exactly 1 payment.settled row, got %d", gotKinds[activity.KindPaymentSettled])
	}
}

func TestClient_Do_NonPaymentResponsePassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("free resource"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ws, _, _ := wallet.Open(filepath.Join(dir, "w"), []byte("pw"))
	id, _ := identity.Generate()
	alog, _ := activity.Open(filepath.Join(dir, "a"), id)
	defer ws.Close()
	defer alog.Close()
	c, _ := NewClient(ws, alog, Config{HTTPClient: srv.Client()})

	req, _ := http.NewRequest("GET", srv.URL+"/free", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "free resource" {
		t.Fatalf("body = %q, want %q", string(body), "free resource")
	}
}

func TestClient_Do_CeilingRejectsBeforeSigning(t *testing.T) {
	f := newClientFixture(t)
	// Crank the mock to demand more than the ceiling.
	f.mock.expectAmount = "999999999" // way over the $0.10 ceiling

	req, _ := http.NewRequest("GET", f.mockSrv.URL+"/resource", nil)
	_, err := f.client.Do(context.Background(), req)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("expected ErrCeilingExceeded, got %v", err)
	}

	// Audit log should have a payment.failed row with the reason — and
	// NO payment.signed row (the ceiling check fires before signing).
	entries, _ := f.alog.Query(context.Background(), activity.QueryOptions{})
	var failedCount, signedCount int
	for _, e := range entries {
		switch e.Kind {
		case activity.KindPaymentFailed:
			failedCount++
		case activity.KindPaymentSigned:
			signedCount++
		}
	}
	if signedCount != 0 {
		t.Fatalf("expected 0 payment.signed rows (signing rejected before signing), got %d", signedCount)
	}
	if failedCount != 1 {
		t.Fatalf("expected 1 payment.failed row, got %d", failedCount)
	}
}

func TestClient_Do_NoCompatibleRequirement(t *testing.T) {
	// Server demands payment on a chain the wallet store doesn't carry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := PaymentRequiredEnvelope{
			X402Version: 1,
			Accepts: []PaymentRequirements{
				{
					Scheme:            SchemeExact,
					Network:           "polygon", // not in v0.2 registry
					MaxAmountRequired: "100",
					PayTo:             "0xff00000000000000000000000000000000000000",
					Asset:             "0x0000000000000000000000000000000000000000",
				},
			},
		}
		raw, _ := json.Marshal(env)
		w.Header().Set(HeaderPaymentRequired, base64.StdEncoding.EncodeToString(raw))
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	dir := t.TempDir()
	ws, _, _ := wallet.Open(filepath.Join(dir, "w"), []byte("pw"))
	id, _ := identity.Generate()
	alog, _ := activity.Open(filepath.Join(dir, "a"), id)
	defer ws.Close()
	defer alog.Close()
	_, _ = ws.CreateWallet("evm:base") // wrong chain
	c, _ := NewClient(ws, alog, Config{HTTPClient: srv.Client()})

	req, _ := http.NewRequest("GET", srv.URL+"/r", nil)
	_, err := c.Do(context.Background(), req)
	if !errors.Is(err, ErrNoCompatibleRequirement) {
		t.Fatalf("expected ErrNoCompatibleRequirement, got %v", err)
	}
}

func TestClient_Do_InvalidPaymentRequiredHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderPaymentRequired, "not-valid-base64!!!")
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	dir := t.TempDir()
	ws, _, _ := wallet.Open(filepath.Join(dir, "w"), []byte("pw"))
	id, _ := identity.Generate()
	alog, _ := activity.Open(filepath.Join(dir, "a"), id)
	defer ws.Close()
	defer alog.Close()
	c, _ := NewClient(ws, alog, Config{HTTPClient: srv.Client()})

	req, _ := http.NewRequest("GET", srv.URL+"/r", nil)
	_, err := c.Do(context.Background(), req)
	if !errors.Is(err, ErrInvalidRequiredHeader) {
		t.Fatalf("expected ErrInvalidRequiredHeader, got %v", err)
	}
}

func TestParsePaymentResponseHeader_AbsentReturnsNil(t *testing.T) {
	if pr := parsePaymentResponseHeader(""); pr != nil {
		t.Fatalf("expected nil, got %+v", pr)
	}
}

func TestParsePaymentResponseHeader_RoundTrip(t *testing.T) {
	src := PaymentResponse{
		Success:     true,
		Transaction: "0xabc",
		Network:     "base",
		Payer:       "0xdef",
	}
	raw, _ := json.Marshal(src)
	hdr := base64.StdEncoding.EncodeToString(raw)
	got := parsePaymentResponseHeader(hdr)
	if got == nil {
		t.Fatal("got nil")
	}
	if got.Success != true || got.Transaction != "0xabc" || got.Network != "base" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
