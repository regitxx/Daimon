package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Credential errors.
var (
	ErrCredentialNotFound = errors.New("provider credentials: not found")
	ErrWrongPassword      = errors.New("provider credentials: wrong password or corrupted file")
	ErrUnsupportedFormat  = errors.New("provider credentials: unsupported format version")
)

// SPEC §10 path: $DAIMON_HOME/providers.json.encrypted. Tied to the same
// at-rest crypto family as the identity keystore: Argon2id (≥64 MiB / ≥3 iters
// / ≥4 parallel per SPEC §4.2) → AES-256-GCM. JSON envelope on disk for
// debuggability.
//
// TODO: factor a shared internal/secretbox so this and internal/identity
// both call into one crypto implementation. Deferred to the session that
// adds passkey/WebAuthn-PRF — that's where the abstraction earns its keep.
const (
	credentialFormatVersion = 1
	argon2MemoryKiB         = 64 * 1024
	argon2Iterations        = 3
	argon2Parallelism       = 4
	argon2KeyLen            = 32
	argon2SaltLen           = 16
	aesGCMNonceLen          = 12
)

type credentialFile struct {
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

// CredentialStore is an in-memory map of {provider name → secret}, persisted
// to disk encrypted-at-rest. Once unlocked, secrets live in plaintext for the
// daimon's lifetime — same trust boundary as the unlocked Ed25519 private key
// in internal/identity.
type CredentialStore struct {
	mu      sync.RWMutex
	secrets map[string]string
}

// NewCredentialStore returns an empty in-memory store. Use Load to hydrate
// from disk.
func NewCredentialStore() *CredentialStore {
	return &CredentialStore{secrets: make(map[string]string)}
}

// LoadCredentialStore reads, decrypts, and returns the store at path.
//
// First-run behaviour: a non-existent path returns an empty store with no
// error — daimons start without provider credentials configured.
func LoadCredentialStore(path string, password []byte) (*CredentialStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewCredentialStore(), nil
		}
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var f credentialFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if f.Version != credentialFormatVersion {
		return nil, ErrUnsupportedFormat
	}
	if f.KDF != "argon2id" || f.Cipher != "aes-256-gcm" {
		return nil, ErrUnsupportedFormat
	}
	salt, err := base64.StdEncoding.DecodeString(f.KDFParams.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(f.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	key := argon2.IDKey(
		password, salt,
		f.KDFParams.Iterations, f.KDFParams.MemoryKiB, f.KDFParams.Parallelism,
		argon2KeyLen,
	)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aes new gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	var secrets map[string]string
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return nil, fmt.Errorf("parse decrypted secrets: %w", err)
	}
	if secrets == nil {
		secrets = make(map[string]string)
	}
	return &CredentialStore{secrets: secrets}, nil
}

// Set stores a secret under name, replacing any existing value.
func (c *CredentialStore) Set(name, secret string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.secrets[name] = secret
}

// Get returns the secret for name, or ErrCredentialNotFound.
func (c *CredentialStore) Get(name string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.secrets[name]
	if !ok {
		return "", ErrCredentialNotFound
	}
	return s, nil
}

// Has reports whether name has a stored secret. Cheap; suitable for marking
// adapters configured/unconfigured in daimon.provider.list responses without
// exposing the secret itself.
func (c *CredentialStore) Has(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.secrets[name]
	return ok
}

// Names returns the registered names, sorted.
func (c *CredentialStore) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.secrets))
	for n := range c.secrets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Delete removes the secret for name. No error if absent.
func (c *CredentialStore) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.secrets, name)
}

// Save encrypts the current set with password and writes it to path with
// mode 0600.
func (c *CredentialStore) Save(path string, password []byte) error {
	c.mu.RLock()
	plaintext, err := json.Marshal(c.secrets)
	c.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey(password, salt, argon2Iterations, argon2MemoryKiB, argon2Parallelism, argon2KeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("aes new gcm: %w", err)
	}
	nonce := make([]byte, aesGCMNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	f := credentialFile{
		Version: credentialFormatVersion,
		KDF:     "argon2id",
		KDFParams: kdfParams{
			MemoryKiB:   argon2MemoryKiB,
			Iterations:  argon2Iterations,
			Parallelism: argon2Parallelism,
			Salt:        base64.StdEncoding.EncodeToString(salt),
		},
		Cipher:     "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}
