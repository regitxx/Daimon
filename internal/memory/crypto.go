package memory

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Application-level row encryption per SPEC §5.1.
//
// content, metadata, and source columns are stored as
//
//	version(1B) || nonce(12B) || AES-256-GCM(plaintext, AAD)
//
// where AAD = "daimon-memory-row-v1" || 0x00 || memoryID || 0x00 || fieldName.
// The AAD binds each ciphertext to its row ID and column so a stolen ciphertext
// cannot be silently moved onto another row or into another field.
//
// The encryption key is the 32-byte HKDF-SHA256 derivation of the principal's
// Ed25519 seed under the label "daimon-memory-encryption-v1" (see
// identity.DeriveSubkey). The key never touches disk: it is rederived in
// memory at unlock time, lives only as long as the unlocked daimond process,
// and is bound to the same trust scope (SPEC §9.2) as the unlocked private key.
//
// Encryption is enabled iff Store.key is non-nil. A nil key falls through —
// the byte slice is stored as-is. v0.1 always derives a non-nil key from the
// bound identity; the nil branch exists for migration tooling that needs to
// inspect rows without unlock and is not exercised by the public API.

const (
	rowCryptoVersion = 0x01
	rowNonceLen      = 12
	rowKeyLen        = 32
	rowAADPrefix     = "daimon-memory-row-v1"

	// MemoryEncryptionKeyLabel is the HKDF info string for deriving the
	// memory store's row-encryption key from the principal's Ed25519 seed.
	MemoryEncryptionKeyLabel = "daimon-memory-encryption-v1"
)

// Errors surfaced by the row-encryption layer.
var (
	ErrInvalidCiphertext = errors.New("memory: invalid ciphertext")
	ErrInvalidKeyLength  = errors.New("memory: invalid encryption key length")
)

// encryptField returns the on-disk blob for plaintext under (memoryID, field).
// A nil key disables encryption — plaintext is returned as-is. An empty
// plaintext returns nil (the column is stored NULL).
func encryptField(key, plaintext []byte, memoryID, field string) ([]byte, error) {
	if key == nil {
		return plaintext, nil
	}
	if len(key) != rowKeyLen {
		return nil, ErrInvalidKeyLength
	}
	if len(plaintext) == 0 {
		return nil, nil
	}

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, rowNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	aad := buildRowAAD(memoryID, field)
	ct := gcm.Seal(nil, nonce, plaintext, aad)

	out := make([]byte, 1+rowNonceLen+len(ct))
	out[0] = rowCryptoVersion
	copy(out[1:1+rowNonceLen], nonce)
	copy(out[1+rowNonceLen:], ct)
	return out, nil
}

// decryptField reverses encryptField. A nil key returns blob as-is. An empty
// blob returns nil. Any failure to authenticate the ciphertext under
// (memoryID, field) returns ErrInvalidCiphertext — including the case where
// the blob is plaintext from an unencrypted store being read by an encrypted
// one (no version byte / not a valid AEAD packet).
func decryptField(key, blob []byte, memoryID, field string) ([]byte, error) {
	if key == nil {
		return blob, nil
	}
	if len(key) != rowKeyLen {
		return nil, ErrInvalidKeyLength
	}
	if len(blob) == 0 {
		return nil, nil
	}
	// minimum: version(1) + nonce(12) + GCM tag(16)
	if len(blob) < 1+rowNonceLen+16 {
		return nil, fmt.Errorf("%w: blob too short (%d bytes)", ErrInvalidCiphertext, len(blob))
	}
	if blob[0] != rowCryptoVersion {
		return nil, fmt.Errorf("%w: unknown version 0x%02x", ErrInvalidCiphertext, blob[0])
	}

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := blob[1 : 1+rowNonceLen]
	ct := blob[1+rowNonceLen:]
	aad := buildRowAAD(memoryID, field)
	pt, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aes new gcm: %w", err)
	}
	return gcm, nil
}

func buildRowAAD(memoryID, field string) []byte {
	aad := make([]byte, 0, len(rowAADPrefix)+1+len(memoryID)+1+len(field))
	aad = append(aad, rowAADPrefix...)
	aad = append(aad, 0x00)
	aad = append(aad, memoryID...)
	aad = append(aad, 0x00)
	aad = append(aad, field...)
	return aad
}
