package payment

import (
	"strings"
	"testing"
)

// EIP3009Digest is the inner loop of every x402 payment:
// build domain separator + struct hash + EIP-712 prefix + keccak256
// — once per payment.pay. It's CPU-bound, fast (~microseconds on
// modern hardware), and the dominant non-IO cost of a payment.
// Worth benchmarking primarily as a regression guard: if a future
// refactor accidentally allocates a fresh hasher per call instead
// of reusing one, the throughput should drop visibly.

func BenchmarkEIP3009Digest(b *testing.B) {
	chain, err := LookupByDaimonChain("evm:base")
	if err != nil {
		b.Fatalf("LookupByDaimonChain: %v", err)
	}
	auth := EVMAuthorizationV2{
		From:        "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		To:          "0xfFFf0000000000000000000000000000000000ff",
		Value:       "100",
		ValidAfter:  "0",
		ValidBefore: "9999999999",
		Nonce:       "0x" + strings.Repeat("ab", 32),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := EIP3009Digest(chain, auth); err != nil {
			b.Fatalf("EIP3009Digest: %v", err)
		}
	}
}

// DomainSeparator alone — same chain info, same domain shape, every
// payment.pay computes it once. Smaller piece of the total digest
// cost. Benchmarked separately so a regression that hits the domain
// separator path specifically (e.g. a new EIP-712 v0.x field) is
// distinguishable from a regression in the struct-hash path.
func BenchmarkDomainSeparator(b *testing.B) {
	chain, err := LookupByDaimonChain("evm:base")
	if err != nil {
		b.Fatalf("LookupByDaimonChain: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DomainSeparator(chain.EIP712Name, chain.EIP712Version, chain.ChainID, chain.USDCAddress); err != nil {
			b.Fatalf("DomainSeparator: %v", err)
		}
	}
}
