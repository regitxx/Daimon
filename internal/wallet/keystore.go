package wallet

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/crypto/argon2"

	"github.com/regitxx/Daimon/internal/secretbox"
)

// Keystore-file envelope format mirrors internal/identity/keystore.go's
// shape exactly. Same Argon2id parameters, same AES-256-GCM cipher, same
// JSON-encoded outer file. Differences are confined to (a) plaintext shape
// (we store {mnemonic, wallets} not a raw Ed25519 key) and (b) the version
// integer's namespace (kept distinct so a future format change in one
// keystore can't be conflated with the other).
const (
	argon2MemoryKiB       = 64 * 1024
	argon2Iterations      = 3
	argon2Parallelism     = 4
	argon2KeyLen          = 32
	argon2SaltLen         = 16
	keystoreFormatVersion = 1
)

var (
	// ErrWrongPassword is returned by Open when AES-GCM authentication
	// fails — i.e. the password didn't derive the right KEK. Same
	// don't-leak-which-step-failed posture as internal/identity.
	ErrWrongPassword = errors.New("wallet: wrong password or corrupted keystore")
	// ErrUnsupportedFormat surfaces a keystore version we don't know how
	// to read.
	ErrUnsupportedFormat = errors.New("wallet: unsupported keystore format version")
	// ErrChainAlreadyExists is returned by CreateWallet when the requested
	// chain is already present in the keystore. v0.2 holds one wallet per
	// chain label; multi-wallet-per-chain can be added later if needed.
	ErrChainAlreadyExists = errors.New("wallet: chain already exists in keystore")
)

// keystoreFile is the on-disk envelope. Identical shape to identity's
// keystore so the two are interchangeable to anyone reading the file format.
type keystoreFile struct {
	Version    int       `json:"version"`
	KDF        string    `json:"kdf"`
	KDFParams  kdfParams `json:"kdf_params"`
	Cipher     string    `json:"cipher"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
}

type kdfParams struct {
	MemoryKiB   uint32 `json:"memory_kib"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	Salt        string `json:"salt"`
}

// plaintext is the decrypted body — JSON-encoded, then AES-GCM-sealed under
// the Argon2id-derived KEK.
type plaintext struct {
	Mnemonic string    `json:"mnemonic"`
	Wallets  []*Wallet `json:"wallets"`
}

// Store is the live, decrypted handle on a wallet keystore. While open it
// holds the mnemonic in memory so SignDigest can re-derive private keys
// without re-prompting for the password. Close() zeroes the mnemonic.
//
// All public methods are safe for concurrent use (mu serialises mutations).
type Store struct {
	path     string
	password []byte

	mu       sync.Mutex
	mnemonic *Mnemonic
	wallets  []*Wallet
}

// Open returns a Store backed by the file at path. If the file does not
// exist, a fresh BIP-39 24-word mnemonic is generated, encrypted under
// password, and written. The mnemonic is returned ONLY on creation; if the
// file already exists the returned *Mnemonic is nil and the caller must
// recover it from their backup.
//
// On the create path, the keystore is written before Open returns — so a
// failure to surface the mnemonic to the user leaves them with a keystore
// they cannot recover. Callers should treat the returned mnemonic as
// must-display and only acknowledge it AFTER the user confirms they have a
// durable copy.
func Open(path string, password []byte) (*Store, *Mnemonic, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return createKeystore(path, password)
	}
	s, err := loadKeystore(path, password)
	return s, nil, err
}

// createKeystore generates a fresh mnemonic, writes the encrypted keystore,
// and returns both the open Store and the new Mnemonic for surfacing.
func createKeystore(path string, password []byte) (*Store, *Mnemonic, error) {
	m, err := Generate()
	if err != nil {
		return nil, nil, err
	}
	pt := plaintext{Mnemonic: m.String(), Wallets: nil}
	if err := writeKeystore(path, password, pt); err != nil {
		return nil, nil, err
	}
	s := &Store{
		path:     path,
		password: append([]byte(nil), password...),
		mnemonic: m,
		wallets:  nil,
	}
	return s, m, nil
}

// loadKeystore decrypts an existing keystore file. Returns ErrWrongPassword
// if AES-GCM auth fails (which folds in both "wrong password" and "file
// corrupted" by design — never tell an attacker which is which).
func loadKeystore(path string, password []byte) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keystore: %w", err)
	}
	var ks keystoreFile
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, fmt.Errorf("parse keystore: %w", err)
	}
	if ks.Version != keystoreFormatVersion {
		return nil, ErrUnsupportedFormat
	}
	if ks.KDF != "argon2id" || ks.Cipher != "aes-256-gcm" {
		return nil, ErrUnsupportedFormat
	}
	salt, err := base64.StdEncoding.DecodeString(ks.KDFParams.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(ks.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ks.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	kek := argon2.IDKey(
		password,
		salt,
		ks.KDFParams.Iterations,
		ks.KDFParams.MemoryKiB,
		ks.KDFParams.Parallelism,
		argon2KeyLen,
	)
	gcm, err := secretbox.NewAEAD(kek)
	if err != nil {
		return nil, fmt.Errorf("aead: %w", err)
	}
	body, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	var pt plaintext
	if err := json.Unmarshal(body, &pt); err != nil {
		return nil, fmt.Errorf("parse plaintext: %w", err)
	}
	m, err := ParseMnemonic(pt.Mnemonic)
	if err != nil {
		return nil, fmt.Errorf("decoded mnemonic invalid: %w", err)
	}
	return &Store{
		path:     path,
		password: append([]byte(nil), password...),
		mnemonic: m,
		wallets:  pt.Wallets,
	}, nil
}

// writeKeystore seals the plaintext under a fresh Argon2id KEK + AES-GCM
// nonce and writes the file at mode 0600.
func writeKeystore(path string, password []byte, pt plaintext) error {
	body, err := json.Marshal(pt)
	if err != nil {
		return fmt.Errorf("marshal plaintext: %w", err)
	}
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("salt: %w", err)
	}
	kek := argon2.IDKey(password, salt, argon2Iterations, argon2MemoryKiB, argon2Parallelism, argon2KeyLen)
	gcm, err := secretbox.NewAEAD(kek)
	if err != nil {
		return fmt.Errorf("aead: %w", err)
	}
	nonce := make([]byte, secretbox.NonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, body, nil)
	ks := keystoreFile{
		Version: keystoreFormatVersion,
		KDF:     "argon2id",
		KDFParams: kdfParams{
			MemoryKiB:   argon2MemoryKiB,
			Iterations:  argon2Iterations,
			Parallelism: argon2Parallelism,
			Salt:        base64.StdEncoding.EncodeToString(salt),
		},
		Cipher:     "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}
	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keystore: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// CreateWallet derives a fresh wallet for the given chain and persists it.
// Returns ErrChainAlreadyExists if the chain is already present.
func (s *Store) CreateWallet(chain string) (*Wallet, error) {
	if parseChain(chain) != chainEVM {
		return nil, ErrUnsupportedChain
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, w := range s.wallets {
		if w.Chain == chain {
			return nil, ErrChainAlreadyExists
		}
	}

	// BIP-44 path m/44'/60'/0'/0/N where N is the index of this wallet
	// across all EVM wallets in the keystore. Coin type 60 = Ethereum
	// (SLIP-44), used by every EVM chain.
	idx := s.nextEVMIndex()
	path := fmt.Sprintf("m/44'/60'/0'/0/%d", idx)
	priv, err := deriveEVMKey(s.mnemonic, path)
	if err != nil {
		return nil, err
	}
	defer zero(priv)

	address, pubKey, err := publicKeyAddress(priv)
	if err != nil {
		return nil, err
	}
	w := &Wallet{
		ID:        ulid.MustNew(uint64(time.Now().UnixMilli()), rand.Reader).String(),
		Chain:     chain,
		Path:      path,
		Address:   address,
		PubKey:    hex.EncodeToString(pubKey),
		CreatedAt: time.Now().UnixMilli(),
	}
	s.wallets = append(s.wallets, w)
	if err := s.save(); err != nil {
		s.wallets = s.wallets[:len(s.wallets)-1]
		return nil, err
	}
	return w, nil
}

// nextEVMIndex returns the next unused BIP-44 address-index for the EVM
// account. Walks the current wallet list and returns max(existing index)+1,
// or 0 if no EVM wallets exist yet.
//
// Must be called with s.mu held.
func (s *Store) nextEVMIndex() uint32 {
	max := -1
	for _, w := range s.wallets {
		if parseChain(w.Chain) != chainEVM {
			continue
		}
		// Path format: "m/44'/60'/0'/0/<idx>" — pull the trailing integer.
		// Fragile if the path scheme ever changes; revisit when that
		// happens.
		var idx int
		if _, err := fmt.Sscanf(w.Path, "m/44'/60'/0'/0/%d", &idx); err == nil {
			if idx > max {
				max = idx
			}
		}
	}
	return uint32(max + 1)
}

// save writes the current in-memory state back to the encrypted keystore.
// Caller must hold s.mu.
func (s *Store) save() error {
	pt := plaintext{
		Mnemonic: s.mnemonic.String(),
		Wallets:  s.wallets,
	}
	return writeKeystore(s.path, s.password, pt)
}

// List returns all wallets in the keystore. Returned slice is safe to mutate
// — it's a fresh copy of the internal state.
func (s *Store) List() []*Wallet {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Wallet, len(s.wallets))
	copy(out, s.wallets)
	return out
}

// FindByChain returns the wallet bound to the given chain label, or
// ErrNotFound.
func (s *Store) FindByChain(chain string) (*Wallet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.wallets {
		if w.Chain == chain {
			return w, nil
		}
	}
	return nil, ErrNotFound
}

// SignDigest signs a 32-byte digest with the wallet bound to chain. The
// private key is derived fresh from the mnemonic + path, used once, and
// zeroed. Returns a 65-byte [r || s || v] signature for EVM chains.
func (s *Store) SignDigest(chain string, digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, ErrInvalidDigest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.wallets {
		if w.Chain != chain {
			continue
		}
		if parseChain(chain) != chainEVM {
			return nil, ErrUnsupportedChain
		}
		priv, err := deriveEVMKey(s.mnemonic, w.Path)
		if err != nil {
			return nil, err
		}
		defer zero(priv)
		return signEVMDigest(priv, digest)
	}
	return nil, ErrNotFound
}

// ShowMnemonic re-verifies the caller knows the keystore password and
// returns the stored mnemonic. The supplied password is run through the
// full Argon2id + AES-GCM-decrypt pipeline against the on-disk keystore
// — NOT compared against the in-memory `s.password` — so the operation
// is a genuine "prove you know the password right now" attestation
// rather than a "the daemon is unlocked, ergo any caller can read the
// seed" leak.
//
// Returns ErrWrongPassword if the supplied password doesn't decrypt the
// on-disk keystore. The Store's in-memory state is not consulted —
// rotating the password on disk independently of an unlocked Store is
// out of scope for v0.2.
//
// Performance: Argon2id KDF costs ~100ms by design. Callers should not
// invoke this in a tight loop.
func (s *Store) ShowMnemonic(password []byte) (*Mnemonic, error) {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()

	// Reuse loadKeystore's decrypt path — same Argon2id parameters, same
	// AES-256-GCM authentication. If the password is wrong, the GCM
	// authentication fails and we surface ErrWrongPassword exactly as
	// the initial Open would.
	verified, err := loadKeystore(path, password)
	if err != nil {
		return nil, err
	}
	// We only needed the mnemonic; close the verifier store so its
	// password/mnemonic copy is zeroed promptly.
	out := &Mnemonic{Words: append([]string(nil), verified.mnemonic.Words...)}
	_ = verified.Close()
	return out, nil
}

// Close zeroes the cached password and mnemonic. After Close, the Store is
// not usable; create a new one via Open to resume operations.
//
// Idempotent — calling Close twice is safe.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.password != nil {
		zero(s.password)
		s.password = nil
	}
	if s.mnemonic != nil {
		for i := range s.mnemonic.Words {
			s.mnemonic.Words[i] = ""
		}
		s.mnemonic = nil
	}
	return nil
}
