package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/argon2"

	"github.com/regitxx/Daimon/internal/secretbox"
)

const (
	argon2MemoryKiB       = 64 * 1024
	argon2Iterations      = 3
	argon2Parallelism     = 4
	argon2KeyLen          = 32
	argon2SaltLen         = 16
	keystoreFormatVersion = 1
)

var (
	ErrWrongPassword     = errors.New("wrong password or corrupted keystore")
	ErrUnknownKDF        = errors.New("unknown key derivation function")
	ErrUnknownCipher     = errors.New("unknown cipher")
	ErrUnsupportedFormat = errors.New("unsupported keystore format version")
)

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

func saveKeystore(path string, priv ed25519.PrivateKey, password []byte) error {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey(password, salt, argon2Iterations, argon2MemoryKiB, argon2Parallelism, argon2KeyLen)

	gcm, err := secretbox.NewAEAD(key)
	if err != nil {
		return fmt.Errorf("aead: %w", err)
	}

	nonce := make([]byte, secretbox.NonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, priv, nil)

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
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// RotatePassword re-encrypts the identity keystore at path under
// newPassword, after verifying oldPassword decrypts the existing file.
// The Ed25519 private key is unchanged — only the at-rest KEK is rotated,
// so the daimon's DID + audit chain continuity are preserved across the
// rotate.
//
// Atomic-ish: writes the new keystore to a sibling `.rotate-tmp` file,
// verifies it decrypts under newPassword, and only then atomically
// renames over the original. A failed rotate leaves the original
// keystore unchanged on disk; the `.rotate-tmp` file is removed.
//
// Offline-only by design — same posture as wallet.RotatePassword. The
// CLI surface (`daimon rotate-password`) refuses to rotate while the
// daemon is running, because a live rotate would desynchronise the
// in-memory identity from the on-disk keystore.
//
// Returns ErrWrongPassword if oldPassword doesn't decrypt the keystore.
func RotatePassword(path string, oldPassword, newPassword []byte) error {
	if len(newPassword) == 0 {
		return errors.New("identity: new password must not be empty")
	}
	// Step 1: verify old password.
	priv, err := loadKeystore(path, oldPassword)
	if err != nil {
		return err
	}
	defer func() {
		// Zero the private-key copy after we're done re-encrypting it.
		for i := range priv {
			priv[i] = 0
		}
	}()

	// Step 2: write under new password to a sibling temp file. Failure
	// leaves the original untouched.
	tmp := path + ".rotate-tmp"
	if err := saveKeystore(tmp, priv, newPassword); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write rotated keystore: %w", err)
	}

	// Step 3: paranoia re-decrypt the temp file with newPassword before
	// committing. Catches the rare "writeKeystore claimed success but
	// the bytes on disk aren't readable" case (disk error not surfaced
	// as an os.WriteFile error, bug in saveKeystore, …) before we
	// atomically clobber the working keystore.
	verify, err := loadKeystore(tmp, newPassword)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("verify rotated keystore: %w", err)
	}
	// Zero the verify-load copy too.
	for i := range verify {
		verify[i] = 0
	}

	// Step 4: atomic rename. POSIX rename(2) is atomic on the same
	// filesystem.
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit rotated keystore: %w", err)
	}
	return nil
}

func loadKeystore(path string, password []byte) (ed25519.PrivateKey, error) {
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
	if ks.KDF != "argon2id" {
		return nil, ErrUnknownKDF
	}
	if ks.Cipher != "aes-256-gcm" {
		return nil, ErrUnknownCipher
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

	key := argon2.IDKey(
		password,
		salt,
		ks.KDFParams.Iterations,
		ks.KDFParams.MemoryKiB,
		ks.KDFParams.Parallelism,
		argon2KeyLen,
	)

	gcm, err := secretbox.NewAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("aead: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	if len(plaintext) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key length: got %d, want %d", len(plaintext), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(plaintext), nil
}
