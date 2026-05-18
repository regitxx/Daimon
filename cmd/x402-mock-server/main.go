// x402-mock-server is a tiny HTTP server that pretends to be an
// x402-protected resource. It serves the role a real x402 server would
// play in a live test: emit 402 with valid PAYMENT-REQUIRED headers,
// accept retries that carry PAYMENT-SIGNATURE, and return 200 with
// PAYMENT-RESPONSE.
//
// Cryptographic verification is real — the server recovers the public
// key from the signature and asserts it matches authorization.from,
// the same property a real x402 facilitator (Coinbase CDP or self-
// hosted) checks before settling on-chain. What this server does NOT
// do: actually settle anything on a blockchain. The PAYMENT-RESPONSE
// it emits names a synthetic transaction hash so daimon's audit
// log gets a payment.settled row, but there's no real movement of
// funds.
//
// Intended use: cross-language live smoke for the daimon's v0.2 wallet
// + x402 surface, parallel in shape to examples/streaming/ for v0.1.
//
// Usage:
//
//	x402-mock-server [-addr 127.0.0.1:8402] [-pay-to 0x...] [-amount 100]
//
// Flags default to a localhost-only listener, a fixed pay-to address,
// and 100 USDC smallest-units ($0.0001) per request — well under the
// daimon's default $0.10 ceiling.
package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"

	"github.com/regitxx/Daimon/internal/payment"
)

var (
	addr   = flag.String("addr", "127.0.0.1:8402", "listen address")
	payTo  = flag.String("pay-to", "0xfFFf0000000000000000000000000000000000ff", "payTo address surfaced in 402 responses")
	amount = flag.String("amount", "100", "MaxAmountRequired in USDC smallest units (decimal string)")
	network = flag.String("network", "base", "x402 network identifier (must be in internal/payment's chain registry)")
)

func main() {
	flag.Parse()

	chain, err := payment.LookupByX402Network(*network)
	if err != nil {
		log.Fatalf("unknown network %q: %v", *network, err)
	}
	srv := &server{
		chain:  chain,
		payTo:  *payTo,
		amount: *amount,
	}
	http.HandleFunc("/", srv.handle)

	fmt.Fprintf(os.Stderr, "x402-mock-server listening on http://%s/\n", *addr)
	fmt.Fprintf(os.Stderr, "  network=%s chain_id=%d usdc=%s\n", chain.X402Network, chain.ChainID, chain.USDCAddress)
	fmt.Fprintf(os.Stderr, "  pay-to=%s amount=%s smallest-units\n", *payTo, *amount)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatal(err)
	}
}

type server struct {
	chain  *payment.ChainInfo
	payTo  string
	amount string
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	sig := r.Header.Get(payment.HeaderPaymentSignature)
	if sig == "" {
		// First contact — emit the 402 with our requirements.
		env := payment.PaymentRequiredEnvelope{
			X402Version: 1,
			Accepts: []payment.PaymentRequirements{{
				Scheme:            payment.SchemeExact,
				Network:           s.chain.X402Network,
				MaxAmountRequired: s.amount,
				Resource:          "http://" + r.Host + r.URL.Path,
				Description:       "mock x402 resource for daimon smoke testing",
				PayTo:             s.payTo,
				MaxTimeoutSeconds: 60,
				Asset:             s.chain.USDCAddress,
			}},
		}
		raw, _ := json.Marshal(env)
		w.Header().Set(payment.HeaderPaymentRequired, base64.StdEncoding.EncodeToString(raw))
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte("payment required\n"))
		return
	}

	// Retry — validate the PAYMENT-SIGNATURE.
	addr, err := s.verifySignature(sig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "PAYMENT-SIGNATURE verification failed: %v\n", err)
		http.Error(w, "invalid PAYMENT-SIGNATURE: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := payment.PaymentResponse{
		Success:     true,
		Transaction: "0x" + strings.Repeat("ab", 32),
		Network:     s.chain.X402Network,
		Payer:       addr,
	}
	raw, _ := json.Marshal(resp)
	w.Header().Set(payment.HeaderPaymentResponse, base64.StdEncoding.EncodeToString(raw))
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "paid resource served to %s (network=%s, amount=%s)\n", addr, s.chain.X402Network, s.amount)
	fmt.Fprintf(os.Stderr, "served paid resource to %s\n", addr)
}

// verifySignature recovers the public key from the PAYMENT-SIGNATURE
// header, recomputes the EIP-3009 digest the client should have signed,
// and asserts the recovered address matches authorization.from. Returns
// the recovered address on success.
//
// This is the property a real x402 facilitator's /verify endpoint
// checks before /settle submits the transaction on-chain.
func (s *server) verifySignature(sigB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	var outer payment.PaymentPayload
	if err := json.Unmarshal(raw, &outer); err != nil {
		return "", fmt.Errorf("unmarshal outer: %w", err)
	}
	if outer.Scheme != payment.SchemeExact {
		return "", fmt.Errorf("unsupported scheme %q", outer.Scheme)
	}
	if outer.Network != s.chain.X402Network {
		return "", fmt.Errorf("network mismatch: got %q, expected %q", outer.Network, s.chain.X402Network)
	}

	var inner payment.EVMExactPayload
	if err := json.Unmarshal(outer.Payload, &inner); err != nil {
		return "", fmt.Errorf("unmarshal inner: %w", err)
	}
	if !strings.EqualFold(inner.Authorization.To, s.payTo) {
		return "", fmt.Errorf("authorization.to = %s, expected %s", inner.Authorization.To, s.payTo)
	}
	if inner.Authorization.Value != s.amount {
		return "", fmt.Errorf("authorization.value = %s, expected %s", inner.Authorization.Value, s.amount)
	}

	digest, err := payment.EIP3009Digest(s.chain, inner.Authorization)
	if err != nil {
		return "", fmt.Errorf("recompute digest: %w", err)
	}

	sigBytes, err := hex.DecodeString(strings.TrimPrefix(inner.Signature, "0x"))
	if err != nil {
		return "", fmt.Errorf("signature hex decode: %w", err)
	}
	if len(sigBytes) != 65 {
		return "", fmt.Errorf("signature length = %d, expected 65", len(sigBytes))
	}
	// Daimon emits [r || s || v]; ecdsa.RecoverCompact wants [v || r || s].
	compact := make([]byte, 65)
	compact[0] = sigBytes[64]
	copy(compact[1:33], sigBytes[0:32])
	copy(compact[33:65], sigBytes[32:64])
	pubKey, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return "", fmt.Errorf("recover compact: %w", err)
	}

	recovered, err := publicKeyEVMAddress(pubKey.SerializeUncompressed())
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(recovered, inner.Authorization.From) {
		return "", fmt.Errorf("signature recovered to %s, but authorization.from = %s", recovered, inner.Authorization.From)
	}
	return recovered, nil
}

// publicKeyEVMAddress derives the EIP-55 Ethereum address from an
// uncompressed secp256k1 public key. Duplicated from internal/wallet's
// private helper because internal/wallet doesn't expose this primitive
// publicly. Kept short and easy to audit alongside the rest of the
// mock server.
func publicKeyEVMAddress(uncompressed []byte) (string, error) {
	if len(uncompressed) != 65 || uncompressed[0] != 0x04 {
		return "", errors.New("uncompressed pubkey must be 65 bytes starting with 0x04")
	}
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	hashed := h.Sum(nil)
	raw := hashed[12:]
	lower := hex.EncodeToString(raw)
	c := sha3.NewLegacyKeccak256()
	c.Write([]byte(lower))
	checksumHash := c.Sum(nil)
	var sb strings.Builder
	sb.WriteString("0x")
	for i, r := range lower {
		if r >= 'a' && r <= 'f' && (checksumHash[i/2]>>uint(4*(1-i%2)))&0xF >= 8 {
			sb.WriteRune(r - 32)
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String(), nil
}
