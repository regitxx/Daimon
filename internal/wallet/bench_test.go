package wallet

import (
	"testing"
)

// DeriveAddress is BIP-32 derivation + secp256k1 pubkey + Keccak256
// hashing + EIP-55 checksum. Run on every `daimon.wallet.create`,
// `daimon.wallet.derive`, and on every payment.pay (one derivation
// per signing call to keep the private key in scope only during
// the sign). Optimisation target if v0.2.x adds high-volume signing.
//
// Cost dominated by BIP-32 path walk + secp256k1 scalar mult, both
// of which are fast on modern hardware (~tens of microseconds total
// on M-series Macs). The Keccak hash + EIP-55 checksumming are noise
// compared to the curve op.

func BenchmarkDeriveAddress_EVMIndex0(b *testing.B) {
	m, err := ParseMnemonic(canonicalTwelveWordMnemonic)
	if err != nil {
		b.Fatalf("ParseMnemonic: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DeriveAddress(m, "evm:base", 0); err != nil {
			b.Fatalf("DeriveAddress: %v", err)
		}
	}
}

// Different indices walk a different BIP-32 child but otherwise the
// same shape. The benchmark exists to catch a regression where
// derivation cost would somehow scale with index value (it
// shouldn't; BIP-32 is constant-time over path depth, not index).
func BenchmarkDeriveAddress_EVMIndex100(b *testing.B) {
	m, err := ParseMnemonic(canonicalTwelveWordMnemonic)
	if err != nil {
		b.Fatalf("ParseMnemonic: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DeriveAddress(m, "evm:base", 100); err != nil {
			b.Fatalf("DeriveAddress: %v", err)
		}
	}
}
