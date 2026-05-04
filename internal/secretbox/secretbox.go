// Package secretbox factors the AES-256-GCM AEAD primitive and the
// "version || nonce || ciphertext" envelope shape that Daimon's at-rest
// encryption layers share.
//
// Two layers are exposed:
//
//   - Lower layer: NewAEAD(key) returns a configured AES-256-GCM cipher.AEAD.
//     Used by the identity keystore and the provider credentials store, both
//     of which carry their AES-GCM ciphertext inside an Argon2id-parameterised
//     JSON envelope that this package does not model.
//
//   - Upper layer: SealAAD / OpenAAD operate on the bytes-packed envelope
//
//     version(1B) || nonce(12B) || AES-256-GCM(plaintext, AAD)
//
//     Used by the memory store (per-row, AAD bound to row id + field) and the
//     activity log (per-entry, AAD bound to entry id + "payload").
//
// AAD construction is the caller's responsibility — each call site binds its
// own domain-separated AAD that prevents cross-row / cross-entry / cross-field
// ciphertext movement.
//
// Encryption is enabled iff key is non-nil. A nil key falls through —
// plaintext and blob arguments are returned as-is. Empty plaintext returns
// nil so callers can rely on `omitempty` JSON encoding to drop the field.
//
// Version is a constant in this package; bumping it is a single edit.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Version is the on-disk envelope format version emitted by SealAAD. OpenAAD
// rejects any other value with ErrInvalidCiphertext.
const Version byte = 0x01

// KeyLen is the required AES-256 key length in bytes.
const KeyLen = 32

// NonceLen is the AES-GCM nonce length in bytes.
const NonceLen = 12

// gcmTagLen is the AES-GCM authentication tag length in bytes (used only for
// the truncated-blob length check in OpenAAD).
const gcmTagLen = 16

// Errors surfaced by the AEAD layer.
var (
	ErrInvalidCiphertext = errors.New("secretbox: invalid ciphertext")
	ErrInvalidKeyLength  = errors.New("secretbox: invalid key length")
)

// NewAEAD returns AES-256-GCM configured under key. The key must be exactly
// KeyLen bytes; any other length returns ErrInvalidKeyLength.
func NewAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeyLen {
		return nil, ErrInvalidKeyLength
	}
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

// SealAAD returns the on-disk envelope for plaintext, bound to aad:
//
//	version(1B) || nonce(12B) || AES-256-GCM(plaintext, aad)
//
// A nil key returns plaintext as-is (encryption disabled). An empty plaintext
// returns nil regardless of key.
func SealAAD(key, plaintext, aad []byte) ([]byte, error) {
	if key == nil {
		return plaintext, nil
	}
	if len(key) != KeyLen {
		return nil, ErrInvalidKeyLength
	}
	if len(plaintext) == 0 {
		return nil, nil
	}
	gcm, err := NewAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, aad)

	out := make([]byte, 1+NonceLen+len(ct))
	out[0] = Version
	copy(out[1:1+NonceLen], nonce)
	copy(out[1+NonceLen:], ct)
	return out, nil
}

// OpenAAD reverses SealAAD. A nil key returns blob as-is. An empty blob
// returns nil. Any failure to authenticate the ciphertext under aad returns
// ErrInvalidCiphertext — including the case where blob is plaintext from an
// unencrypted source being read by an encrypted one (no version byte / not a
// valid AEAD packet).
func OpenAAD(key, blob, aad []byte) ([]byte, error) {
	if key == nil {
		return blob, nil
	}
	if len(key) != KeyLen {
		return nil, ErrInvalidKeyLength
	}
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob) < 1+NonceLen+gcmTagLen {
		return nil, fmt.Errorf("%w: blob too short (%d bytes)", ErrInvalidCiphertext, len(blob))
	}
	if blob[0] != Version {
		return nil, fmt.Errorf("%w: unknown version 0x%02x", ErrInvalidCiphertext, blob[0])
	}
	gcm, err := NewAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := blob[1 : 1+NonceLen]
	ct := blob[1+NonceLen:]
	pt, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}
	return pt, nil
}
