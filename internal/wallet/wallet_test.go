package wallet

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// Trezor's published BIP-39 test-vector mnemonic for 256-bit entropy
// "0000...00". Used here as a stable, deterministic input so the derivation
// outputs are reproducible across runs. NOT a real wallet — published in
// the BIP-39 spec test vectors.
const trezorAllZerosMnemonic = "abandon abandon abandon abandon abandon abandon " +
	"abandon abandon abandon abandon abandon abandon " +
	"abandon abandon abandon abandon abandon abandon " +
	"abandon abandon abandon abandon abandon art"

// --- Mnemonic surface --------------------------------------------------------

func TestGenerate_Produces24ValidWords(t *testing.T) {
	m, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(m.Words) != MnemonicWordCount {
		t.Fatalf("got %d words, want %d", len(m.Words), MnemonicWordCount)
	}
	// Round-trip through ParseMnemonic to verify BIP-39 checksum.
	if _, err := ParseMnemonic(m.String()); err != nil {
		t.Fatalf("re-parse of generated mnemonic failed: %v", err)
	}
}

func TestParseMnemonic_AcceptsKnownVector(t *testing.T) {
	m, err := ParseMnemonic(trezorAllZerosMnemonic)
	if err != nil {
		t.Fatalf("ParseMnemonic: %v", err)
	}
	if len(m.Words) != MnemonicWordCount {
		t.Fatalf("got %d words", len(m.Words))
	}
}

func TestParseMnemonic_RejectsBadChecksum(t *testing.T) {
	// Same words as the trezor vector but flip the last word — fails the
	// BIP-39 checksum because the last word encodes part of the checksum.
	bad := strings.Replace(trezorAllZerosMnemonic, "art", "able", 1)
	if _, err := ParseMnemonic(bad); !errors.Is(err, ErrInvalidMnemonic) {
		t.Fatalf("expected ErrInvalidMnemonic, got %v", err)
	}
}

func TestParseMnemonic_RejectsUnknownWord(t *testing.T) {
	bad := strings.Replace(trezorAllZerosMnemonic, "abandon", "xxxxxxxx", 1)
	if _, err := ParseMnemonic(bad); !errors.Is(err, ErrInvalidMnemonic) {
		t.Fatalf("expected ErrInvalidMnemonic, got %v", err)
	}
}

// --- EVM derivation ----------------------------------------------------------

// The canonical 12-word "abandon ... about" BIP-39 test vector at path
// m/44'/60'/0'/0/0 produces a well-known Ethereum address — the same one
// every BIP-39 derivation tool (iancoleman.io/bip39, MetaMask, etc.)
// returns. Anchoring against it cross-checks our derivation pipeline
// against an externally-verifiable value rather than just regression-testing
// against ourselves.
const (
	canonicalTwelveWordMnemonic = "abandon abandon abandon abandon abandon abandon " +
		"abandon abandon abandon abandon abandon about"
	canonicalTwelveWordEVMAddrIndex0 = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
)

func TestDerive_EVMAddressMatchesPublishedVector(t *testing.T) {
	m, err := ParseMnemonic(canonicalTwelveWordMnemonic)
	if err != nil {
		t.Fatalf("ParseMnemonic: %v", err)
	}
	priv, err := deriveEVMKey(m, "m/44'/60'/0'/0/0")
	if err != nil {
		t.Fatalf("deriveEVMKey: %v", err)
	}
	defer zero(priv)
	got, _, err := publicKeyAddress(priv)
	if err != nil {
		t.Fatalf("publicKeyAddress: %v", err)
	}
	if got != canonicalTwelveWordEVMAddrIndex0 {
		t.Fatalf("EVM address mismatch against published BIP-39 vector:\n  got  %s\n  want %s\n"+
			"This means the BIP-32 derivation pipeline drifted from the standard.",
			got, canonicalTwelveWordEVMAddrIndex0)
	}
}

func TestDerive_DistinctIndicesYieldDistinctAddresses(t *testing.T) {
	m, _ := ParseMnemonic(trezorAllZerosMnemonic)
	a, _ := deriveEVMKey(m, "m/44'/60'/0'/0/0")
	b, _ := deriveEVMKey(m, "m/44'/60'/0'/0/1")
	addrA, _, _ := publicKeyAddress(a)
	addrB, _, _ := publicKeyAddress(b)
	zero(a)
	zero(b)
	if addrA == addrB {
		t.Fatalf("same address for distinct paths: %s", addrA)
	}
}

// --- Store: keystore round-trip ---------------------------------------------

func TestStore_CreateAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	pw := []byte("opensesame")

	s, m, err := Open(path, pw)
	if err != nil {
		t.Fatalf("Open(create): %v", err)
	}
	if m == nil || len(m.Words) != MnemonicWordCount {
		t.Fatalf("expected fresh mnemonic on create, got %v", m)
	}
	w, err := s.CreateWallet("evm:base")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if !strings.HasPrefix(w.Address, "0x") || len(w.Address) != 42 {
		t.Fatalf("invalid EVM address: %s", w.Address)
	}
	expectedAddress := w.Address
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen with the same password — should see the wallet, no fresh
	// mnemonic returned (nil).
	s2, m2, err := Open(path, []byte("opensesame"))
	if err != nil {
		t.Fatalf("Open(reopen): %v", err)
	}
	if m2 != nil {
		t.Fatalf("expected nil mnemonic on reopen, got %v", m2)
	}
	got, err := s2.FindByChain("evm:base")
	if err != nil {
		t.Fatalf("FindByChain after reopen: %v", err)
	}
	if got.Address != expectedAddress {
		t.Fatalf("address drift across reopen:\n  got  %s\n  want %s", got.Address, expectedAddress)
	}
	s2.Close()
}

func TestStore_WrongPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")

	s, _, err := Open(path, []byte("correct-horse"))
	if err != nil {
		t.Fatalf("Open(create): %v", err)
	}
	if _, err := s.CreateWallet("evm:base"); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	s.Close()

	if _, _, err := Open(path, []byte("wrong-horse")); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

// --- Store: CreateWallet semantics ------------------------------------------

func TestStore_CreateWallet_DuplicateChainRejected(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()

	if _, err := s.CreateWallet("evm:base"); err != nil {
		t.Fatalf("first CreateWallet: %v", err)
	}
	if _, err := s.CreateWallet("evm:base"); !errors.Is(err, ErrChainAlreadyExists) {
		t.Fatalf("expected ErrChainAlreadyExists, got %v", err)
	}
}

func TestStore_CreateWallet_MultipleChains(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()

	a, err := s.CreateWallet("evm:base")
	if err != nil {
		t.Fatalf("evm:base: %v", err)
	}
	b, err := s.CreateWallet("evm:base-sepolia")
	if err != nil {
		t.Fatalf("evm:base-sepolia: %v", err)
	}
	if a.Address == b.Address {
		t.Fatalf("two chains derived to same address %s — should be distinct paths", a.Address)
	}
	if a.Path == b.Path {
		t.Fatalf("two chains share derivation path %s — should be unique", a.Path)
	}
	if got := len(s.List()); got != 2 {
		t.Fatalf("expected 2 wallets after creates, got %d", got)
	}
}

func TestStore_CreateWallet_UnsupportedChain(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()

	for _, chain := range []string{"svm:solana", "stellar", "bitcoin", ""} {
		if _, err := s.CreateWallet(chain); !errors.Is(err, ErrUnsupportedChain) {
			t.Fatalf("chain %q: expected ErrUnsupportedChain, got %v", chain, err)
		}
	}
}

// --- Store: signing ----------------------------------------------------------

func TestStore_SignDigest_RecoversToWalletPublicKey(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()

	w, err := s.CreateWallet("evm:base")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	msg := []byte("daimon test message — to be hashed and signed")
	digest := sha256.Sum256(msg)
	sig, err := s.SignDigest("evm:base", digest[:])
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte EVM signature, got %d bytes", len(sig))
	}

	// Reconstitute the compact-form signature ecdsa.RecoverCompact wants:
	// [recoveryByte || r || s]. We emitted [r || s || v]; rebuild.
	compact := make([]byte, 65)
	compact[0] = sig[64] // v (27 or 28)
	copy(compact[1:33], sig[0:32])
	copy(compact[33:65], sig[32:64])

	recoveredPub, _, err := ecdsa.RecoverCompact(compact, digest[:])
	if err != nil {
		t.Fatalf("RecoverCompact: %v", err)
	}
	if got, want := bytesToHex(recoveredPub.SerializeCompressed()), w.PubKey; got != want {
		t.Fatalf("recovered pubkey != wallet pubkey:\n  got  %s\n  want %s", got, want)
	}
}

func TestStore_SignDigest_RejectsBadDigestLength(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()
	if _, err := s.CreateWallet("evm:base"); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}

	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := s.SignDigest("evm:base", make([]byte, n)); !errors.Is(err, ErrInvalidDigest) {
			t.Fatalf("digest length %d: expected ErrInvalidDigest, got %v", n, err)
		}
	}
}

func TestStore_SignDigest_NotFound(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()

	digest := sha256.Sum256([]byte("anything"))
	if _, err := s.SignDigest("evm:base", digest[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- Store: lookup -----------------------------------------------------------

func TestStore_FindByChain_NotFound(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()
	if _, err := s.FindByChain("evm:mainnet"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_List_ReturnsCopy(t *testing.T) {
	s := openFreshStore(t)
	defer s.Close()
	if _, err := s.CreateWallet("evm:base"); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	out := s.List()
	if len(out) != 1 {
		t.Fatalf("expected 1 wallet, got %d", len(out))
	}
	// Mutating the returned slice must not affect the store's internal
	// state.
	out[0] = nil
	out = nil
	if got := s.List(); len(got) != 1 || got[0] == nil {
		t.Fatalf("List() returned slice that aliased internal state")
	}
}

// --- Helpers -----------------------------------------------------------------

func openFreshStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	s, _, err := Open(path, []byte("test-password"))
	if err != nil {
		t.Fatalf("openFreshStore: %v", err)
	}
	return s
}

func bytesToHex(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hex[c>>4]
		out[i*2+1] = hex[c&0xF]
	}
	return string(out)
}
