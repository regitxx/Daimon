package wallet

import (
	"crypto/sha256"
	"errors"
	"os"
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

// --- Store: ShowMnemonic ----------------------------------------------------

func TestStore_ShowMnemonic_ReturnsStoredSeed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	pw := []byte("re-confirm-me")

	s, fresh, err := Open(path, pw)
	if err != nil {
		t.Fatalf("Open(create): %v", err)
	}
	if fresh == nil {
		t.Fatal("expected a fresh mnemonic on first create")
	}
	// Capture the mnemonic the daemon generated; we'll assert
	// ShowMnemonic returns the same words.
	want := fresh.String()

	// ShowMnemonic must work even while the Store is open + holding the
	// in-memory mnemonic — it should re-derive via the on-disk file, not
	// just hand back s.mnemonic.
	got, err := s.ShowMnemonic(pw)
	if err != nil {
		t.Fatalf("ShowMnemonic: %v", err)
	}
	if got.String() != want {
		t.Fatalf("ShowMnemonic returned wrong mnemonic:\n  got  %s\n  want %s", got.String(), want)
	}
	s.Close()
}

func TestStore_ShowMnemonic_RejectsWrongPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")

	s, _, err := Open(path, []byte("correct"))
	if err != nil {
		t.Fatalf("Open(create): %v", err)
	}
	defer s.Close()

	if _, err := s.ShowMnemonic([]byte("WRONG")); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

func TestStore_ShowMnemonic_ReVerifiesAgainstDiskNotMemory(t *testing.T) {
	// The structural property the function promises: it does NOT just
	// hand back s.mnemonic to anyone with socket access. It re-runs the
	// full KDF + AEAD-decrypt against the on-disk keystore. The
	// wrong-password test above exercises that — if ShowMnemonic
	// short-circuited on the in-memory mnemonic, a wrong password
	// would still return the seed.
	//
	// This test asserts the same property from the other angle: it
	// works correctly even after the Store has been used (wallet
	// creation in flight), so the verification path doesn't depend
	// on the keystore being "fresh".
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	pw := []byte("post-use")
	s, fresh, err := Open(path, pw)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	want := fresh.String()

	if _, err := s.CreateWallet("evm:base"); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if _, err := s.CreateWallet("evm:base-sepolia"); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}

	got, err := s.ShowMnemonic(pw)
	if err != nil {
		t.Fatalf("ShowMnemonic after creates: %v", err)
	}
	if got.String() != want {
		t.Fatalf("mnemonic drift after wallet creates")
	}
}

// --- DeriveAddress: read-only derivation ------------------------------------

func TestDeriveAddress_MatchesCanonicalVector(t *testing.T) {
	// The same canonical 12-word vector → 0x9858EfFD...EcaEda94 at index 0
	// fixture used by TestDerive_EVMAddressMatchesPublishedVector, but
	// through the public DeriveAddress entry point that wallet.recover
	// + the new daimon.wallet.derive RPC verb both call into. Drift in
	// DeriveAddress would trip this test even if the lower-level
	// deriveEVMKey/publicKeyAddress functions stay correct.
	m, err := ParseMnemonic(canonicalTwelveWordMnemonic)
	if err != nil {
		t.Fatalf("ParseMnemonic: %v", err)
	}
	got, err := DeriveAddress(m, "evm:base", 0)
	if err != nil {
		t.Fatalf("DeriveAddress: %v", err)
	}
	if got.Address != canonicalTwelveWordEVMAddrIndex0 {
		t.Fatalf("address mismatch:\n  got  %s\n  want %s", got.Address, canonicalTwelveWordEVMAddrIndex0)
	}
	if got.Path != "m/44'/60'/0'/0/0" {
		t.Fatalf("unexpected path: %s", got.Path)
	}
	if got.Chain != "evm:base" {
		t.Fatalf("chain not threaded through: %s", got.Chain)
	}
	if len(got.PubKey) != 66 { // 33 bytes compressed, hex = 66 chars
		t.Fatalf("pubkey length: got %d, want 66 (33 bytes compressed)", len(got.PubKey))
	}
}

func TestDeriveAddress_DistinctIndicesYieldDistinctAddresses(t *testing.T) {
	m, _ := ParseMnemonic(trezorAllZerosMnemonic)
	a, err := DeriveAddress(m, "evm:base", 0)
	if err != nil {
		t.Fatalf("index 0: %v", err)
	}
	b, err := DeriveAddress(m, "evm:base", 1)
	if err != nil {
		t.Fatalf("index 1: %v", err)
	}
	if a.Address == b.Address {
		t.Fatalf("indices 0 and 1 collapsed to the same address: %s", a.Address)
	}
}

func TestDeriveAddress_DoesNotPersist(t *testing.T) {
	// Open a fresh keystore, derive, then assert the keystore's
	// in-memory wallet list is still empty. Captures the "derive is
	// read-only" contract.
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	s, _, err := Open(path, []byte("pw"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.Derive("evm:base", 0); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if got := len(s.List()); got != 0 {
		t.Fatalf("Derive persisted a wallet: List() returned %d, want 0", got)
	}
}

func TestDeriveAddress_RejectsUnsupportedChain(t *testing.T) {
	m, _ := ParseMnemonic(trezorAllZerosMnemonic)
	if _, err := DeriveAddress(m, "svm:solana", 0); !errors.Is(err, ErrUnsupportedChain) {
		t.Fatalf("expected ErrUnsupportedChain, got %v", err)
	}
}

func TestDeriveAddress_RejectsEmptyMnemonic(t *testing.T) {
	for _, m := range []*Mnemonic{nil, {Words: nil}, {Words: []string{}}} {
		if _, err := DeriveAddress(m, "evm:base", 0); !errors.Is(err, ErrInvalidMnemonic) {
			t.Fatalf("expected ErrInvalidMnemonic for %+v, got %v", m, err)
		}
	}
}

// --- RotatePassword: re-encrypt keystore under a new password ---------------

func TestRotatePassword_RoundTrip(t *testing.T) {
	// Rotate the password and confirm:
	// - new password decrypts to the same mnemonic + wallets
	// - old password no longer decrypts
	// - the same address gets derived at index 0 (mnemonic preserved)
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")

	s, fresh, err := Open(path, []byte("old"))
	if err != nil {
		t.Fatalf("Open(create): %v", err)
	}
	if fresh == nil {
		t.Fatal("expected fresh mnemonic on first create")
	}
	preMnemonic := fresh.String()
	w, err := s.CreateWallet("evm:base")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	preAddress := w.Address
	s.Close()

	if err := RotatePassword(path, []byte("old"), []byte("new")); err != nil {
		t.Fatalf("RotatePassword: %v", err)
	}

	// New password decrypts + preserves mnemonic + wallets.
	s2, _, err := Open(path, []byte("new"))
	if err != nil {
		t.Fatalf("Open(post-rotate, new pw): %v", err)
	}
	defer s2.Close()
	postMnemonic := s2.mnemonic.String()
	if postMnemonic != preMnemonic {
		t.Fatalf("rotate changed mnemonic")
	}
	got, err := s2.FindByChain("evm:base")
	if err != nil {
		t.Fatalf("FindByChain after rotate: %v", err)
	}
	if got.Address != preAddress {
		t.Fatalf("rotate changed derived address: %s → %s", preAddress, got.Address)
	}

	// Old password no longer decrypts.
	if _, _, err := Open(path, []byte("old")); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("post-rotate load with old password: got %v, want ErrWrongPassword", err)
	}
}

func TestRotatePassword_RejectsWrongOldPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	s, _, err := Open(path, []byte("correct"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()

	if err := RotatePassword(path, []byte("WRONG"), []byte("new")); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("RotatePassword with wrong old password: got %v, want ErrWrongPassword", err)
	}

	// Failed rotate must leave the original intact under the original password.
	if _, _, err := Open(path, []byte("correct")); err != nil {
		t.Errorf("post-failed-rotate load with correct password: got %v, want success", err)
	}
}

func TestRotatePassword_RejectsEmptyNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	s, _, err := Open(path, []byte("pw"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if err := RotatePassword(path, []byte("pw"), []byte("")); err == nil {
		t.Error("RotatePassword with empty new password: expected error, got nil")
	}
}

func TestRotatePassword_TempFileCleanedUpOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	s, _, err := Open(path, []byte("a"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if err := RotatePassword(path, []byte("a"), []byte("b")); err != nil {
		t.Fatalf("RotatePassword: %v", err)
	}
	tmp := path + ".rotate-tmp"
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rotate-tmp file leaked on success: stat err = %v", err)
	}
}

// --- RecoverInto: offline seed import ---------------------------------------

func TestRecoverInto_WritesKeystoreFromSuppliedSeed(t *testing.T) {
	// Drives the round-trip the CLI flow promises: hand RecoverInto a
	// known mnemonic, then prove that Open against the resulting file
	// (with the same password) yields a Store that derives addresses
	// from THAT mnemonic, not from a freshly generated one.
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	m, err := ParseMnemonic(canonicalTwelveWordMnemonic)
	if err != nil {
		t.Fatalf("ParseMnemonic: %v", err)
	}

	if err := RecoverInto(path, m, []byte("recovery-password")); err != nil {
		t.Fatalf("RecoverInto: %v", err)
	}

	// Reopen — should see this is a load, NOT a create (m2 == nil).
	s, m2, err := Open(path, []byte("recovery-password"))
	if err != nil {
		t.Fatalf("Open(post-recover): %v", err)
	}
	defer s.Close()
	if m2 != nil {
		t.Fatalf("expected Open to take the load branch (nil mnemonic), got %v", m2)
	}

	// Derive index-0 EVM wallet — must match the canonical 12-word vector
	// address. Proves the keystore really holds the supplied seed.
	w, err := s.CreateWallet("evm:base")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if w.Address != canonicalTwelveWordEVMAddrIndex0 {
		t.Fatalf("recovered seed produced wrong address:\n  got  %s\n  want %s",
			w.Address, canonicalTwelveWordEVMAddrIndex0)
	}
}

func TestRecoverInto_RefusesIfKeystoreAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")

	// Set up an existing keystore via the normal create path.
	s, _, err := Open(path, []byte("existing-pw"))
	if err != nil {
		t.Fatalf("Open(create): %v", err)
	}
	s.Close()

	// Attempt to recover into the same path — must refuse.
	m, _ := ParseMnemonic(canonicalTwelveWordMnemonic)
	err = RecoverInto(path, m, []byte("any-password"))
	if !errors.Is(err, ErrKeystoreExists) {
		t.Fatalf("expected ErrKeystoreExists, got %v", err)
	}
}

func TestRecoverInto_RejectsInvalidMnemonic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")

	// Construct a Mnemonic struct directly (bypassing ParseMnemonic) with
	// a bad-checksum word list. RecoverInto must catch this — otherwise
	// a typo-during-recovery could land an un-openable keystore on disk
	// that no Open call could ever decrypt (loadKeystore would fail at
	// ParseMnemonic, leaving the user with a "wrong password" error for
	// the right password).
	bad := &Mnemonic{Words: strings.Fields(
		strings.Replace(trezorAllZerosMnemonic, "art", "able", 1),
	)}
	if err := RecoverInto(path, bad, []byte("pw")); !errors.Is(err, ErrInvalidMnemonic) {
		t.Fatalf("expected ErrInvalidMnemonic, got %v", err)
	}
	// And the file must not have been written.
	if _, err := os.Stat(path); err == nil {
		t.Fatal("RecoverInto left a partial keystore on disk after rejecting a bad mnemonic")
	}
}

func TestRecoverInto_RejectsEmptyMnemonic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wallet.keystore")
	for _, m := range []*Mnemonic{nil, {Words: nil}, {Words: []string{}}} {
		if err := RecoverInto(path, m, []byte("pw")); !errors.Is(err, ErrInvalidMnemonic) {
			t.Fatalf("expected ErrInvalidMnemonic for %+v, got %v", m, err)
		}
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
