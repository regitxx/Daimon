// Package wallet provides BIP-39/BIP-32 HD wallet management for Daimon.
//
// v0.2 scope:
//
//   - One mnemonic per principal (BIP-39, 24 words, English wordlist).
//   - N derived wallets per principal, each tagged with a chain label.
//   - First chain: EVM (Base / Base-Sepolia / Mainnet / Polygon / Arbitrum /
//     etc.) — all use the same secp256k1 + Keccak256 + EIP-55 address
//     derivation, so one HD-derived secp256k1 key works on any EVM chain.
//   - SLIP-10 / Ed25519-curve chains (Solana, Stellar) deferred to v0.2.x.
//
// Mirrors internal/memory's package shape: a Store opens an encrypted
// keystore file, mediates all on-disk crypto through internal/secretbox +
// AES-256-GCM under an Argon2id-derived KEK, and exposes typed verbs
// (CreateWallet / List / FindByChain / SignDigest). The mnemonic and all
// derived private keys live only in memory while the Store is open; Close
// zeroes them.
//
// Storage layout: one JSON file per principal, $DAIMON_HOME/wallet.keystore,
// mode 0600, with the same Argon2id + AES-256-GCM envelope used by
// internal/identity/keystore.go. The encrypted plaintext is a JSON object
// {mnemonic, wallets[]} where wallets carry the BIP-44 derivation path so
// each can be re-derived deterministically.
package wallet

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/sha3"
)

// Errors surfaced by the wallet package.
var (
	ErrUnsupportedChain = errors.New("wallet: unsupported chain")
	ErrNotFound         = errors.New("wallet: not found")
	ErrInvalidDigest    = errors.New("wallet: digest must be 32 bytes")
	ErrInvalidMnemonic  = errors.New("wallet: invalid mnemonic")
)

// MnemonicWordCount is the BIP-39 entropy strength Daimon defaults to.
// 24 words encodes 256 bits of entropy — the upper-bound of BIP-39, matches
// the entropy of an Ed25519 identity key, and is what every mainstream
// wallet (MetaMask, Phantom, Rabby) generates by default.
const MnemonicWordCount = 24

// mnemonicEntropyBits is the entropy bit-count that produces MnemonicWordCount
// words via BIP-39's encoding (32 bits per 3 words, so 24 words = 256 bits).
const mnemonicEntropyBits = 256

// EVM chain labels recognised by this package. Add as new EVM chains gain
// x402 facilitator coverage. The label is opaque to derivation — every EVM
// chain uses the same secp256k1 key + EIP-55 address, so the label just
// tags the wallet's intended primary chain.
//
// Non-EVM chain labels (svm:* for Solana, stellar:* for Stellar) are
// recognised at the label level for forward compatibility but not yet
// implemented in CreateWallet — see EVM/SLIP-10 caveat in the package doc.
var (
	evmChainPrefixes = map[string]struct{}{
		"evm:":      {},
		"ethereum:": {}, // alias for "evm:" — accepted, normalised to "evm:" form on store
	}
)

// chainKind classifies a chain label. Returned by parseChain.
type chainKind int

const (
	chainEVM chainKind = iota + 1
	chainUnsupported
)

func parseChain(chain string) chainKind {
	for prefix := range evmChainPrefixes {
		if strings.HasPrefix(chain, prefix) {
			return chainEVM
		}
	}
	return chainUnsupported
}

// Wallet is a single derived keypair as surfaced via RPC + persisted on disk.
// The private key is NOT a field here — it lives in the mnemonic + path and
// is re-derived per signing operation, then zeroed.
type Wallet struct {
	ID        string `json:"id"`         // ULID
	Chain     string `json:"chain"`      // e.g. "evm:base"
	Path      string `json:"path"`       // BIP-44 derivation, e.g. "m/44'/60'/0'/0/0"
	Address   string `json:"address"`    // EIP-55 checksummed for EVM
	PubKey    string `json:"pubkey"`     // hex-encoded 33-byte compressed secp256k1
	CreatedAt int64  `json:"created_at"` // unix ms
}

// Mnemonic wraps a BIP-39 word sequence with a typed presentation. The Words
// field is the canonical representation; use String() to render as a single
// space-separated line for display/storage.
type Mnemonic struct {
	Words []string
}

// String returns the mnemonic as a single space-separated string — the
// format BIP-39 verifiers expect.
func (m *Mnemonic) String() string {
	return strings.Join(m.Words, " ")
}

// Generate creates a fresh BIP-39 24-word mnemonic backed by 256 bits of
// system entropy. The mnemonic must be surfaced to the principal exactly
// once (at first wallet creation) so they can back it up — losing the
// mnemonic means losing the ability to recover any wallet derived from it.
func Generate() (*Mnemonic, error) {
	entropy, err := bip39.NewEntropy(mnemonicEntropyBits)
	if err != nil {
		return nil, fmt.Errorf("generate entropy: %w", err)
	}
	phrase, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, fmt.Errorf("encode mnemonic: %w", err)
	}
	return &Mnemonic{Words: strings.Fields(phrase)}, nil
}

// ParseMnemonic accepts a space-separated mnemonic string (used to restore
// from a backup). Returns ErrInvalidMnemonic if the words are not a valid
// BIP-39 sequence (wrong count, unknown word, or bad checksum).
func ParseMnemonic(s string) (*Mnemonic, error) {
	s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
	if !bip39.IsMnemonicValid(s) {
		return nil, ErrInvalidMnemonic
	}
	return &Mnemonic{Words: strings.Fields(s)}, nil
}

// deriveEVMKey walks the BIP-32 derivation path on the seed derived from
// mnemonic + empty passphrase. Returns the secp256k1 private key bytes.
// Caller MUST zero the returned slice when done.
func deriveEVMKey(mnemonic *Mnemonic, path string) ([]byte, error) {
	seed := bip39.NewSeed(mnemonic.String(), "")
	master, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("master key: %w", err)
	}
	child := master
	parts := strings.Split(path, "/")
	if len(parts) < 1 || parts[0] != "m" {
		return nil, fmt.Errorf("wallet: derivation path must start with m/")
	}
	for _, segment := range parts[1:] {
		hardened := strings.HasSuffix(segment, "'")
		idxStr := strings.TrimSuffix(segment, "'")
		var idx uint32
		if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil {
			return nil, fmt.Errorf("wallet: invalid path segment %q: %w", segment, err)
		}
		if hardened {
			idx |= 0x80000000
		}
		child, err = child.NewChildKey(idx)
		if err != nil {
			return nil, fmt.Errorf("wallet: derive %s: %w", segment, err)
		}
	}
	return child.Key, nil
}

// publicKeyAddress returns the EIP-55-checksummed Ethereum-style address for
// a secp256k1 private key. Same derivation works for any EVM chain — Base,
// Polygon, Arbitrum, etc. all use Ethereum's address scheme.
//
// Reference: https://eips.ethereum.org/EIPS/eip-55
func publicKeyAddress(privKey []byte) (address string, compressedPubKey []byte, err error) {
	priv := secp256k1.PrivKeyFromBytes(privKey)
	pub := priv.PubKey()

	// EVM address = Keccak256(uncompressed_pubkey[1:65])[12:32]. The leading
	// byte of the uncompressed form is the 0x04 prefix that secp256k1
	// libraries add; address derivation needs the 64-byte X||Y body.
	uncompressed := pub.SerializeUncompressed() // 65 bytes: 0x04 || X(32) || Y(32)
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write(uncompressed[1:])
	hash := hasher.Sum(nil)
	rawAddr := hash[12:]

	// EIP-55 checksumming: hex-encode the address, then uppercase each char
	// at position i if the i-th nibble of Keccak256(addr_lower_hex) is >= 8.
	lowerHex := hex.EncodeToString(rawAddr)
	checksumHasher := sha3.NewLegacyKeccak256()
	checksumHasher.Write([]byte(lowerHex))
	checksumHash := checksumHasher.Sum(nil)

	out := strings.Builder{}
	out.WriteString("0x")
	for i, c := range lowerHex {
		if c >= 'a' && c <= 'f' && (checksumHash[i/2]>>uint(4*(1-i%2)))&0xF >= 8 {
			out.WriteRune(c - 32) // to uppercase
			continue
		}
		out.WriteRune(c)
	}
	return out.String(), pub.SerializeCompressed(), nil
}

// signEVMDigest produces a 65-byte EVM-style ECDSA signature [r || s || v]
// over a 32-byte digest. v is the recovery id (27 + recid) per Ethereum's
// signature encoding convention.
//
// EIP-3009 transferWithAuthorization signatures take this exact shape, so
// this function is the primitive that x402's `exact` scheme on EVM will
// call into in phase 40.3.
func signEVMDigest(privKey []byte, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, ErrInvalidDigest
	}
	priv := secp256k1.PrivKeyFromBytes(privKey)
	// ecdsa.SignCompact returns 65 bytes: [recoveryByte || r || s] with the
	// recovery byte prefixed. Ethereum convention reverses that to [r || s
	// || v] and uses 27/28 for v.
	compact := ecdsa.SignCompact(priv, digest, false /* uncompressed */)
	if len(compact) != 65 {
		return nil, fmt.Errorf("wallet: unexpected compact sig length %d", len(compact))
	}
	// dcrec emits [recid+27 || r || s] (recid in {27,28} for uncompressed).
	// Rearrange to [r || s || v] where v = recid (0 or 1) for EVM use; the
	// canonical Ethereum encoding uses v in {27, 28}, which we adopt here
	// so EIP-3009 verifiers accept it unchanged.
	sig := make([]byte, 65)
	copy(sig[0:32], compact[1:33])
	copy(sig[32:64], compact[33:65])
	sig[64] = compact[0] // already 27 or 28
	return sig, nil
}

// zero overwrites a byte slice with zeros. Used to clear private-key buffers
// after signing; not a security guarantee against process-memory inspection
// but a hygiene practice matching internal/identity's posture.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

