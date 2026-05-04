package memory

import (
	"github.com/regitxx/Daimon/internal/secretbox"
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
// The AEAD primitive and the bytes-packed envelope live in
// internal/secretbox; this file contributes the AAD construction and the
// HKDF info label that make memory-row encryption a domain-separated
// instance of the shared envelope.
//
// Encryption is enabled iff Store.key is non-nil. A nil key falls through —
// the byte slice is stored as-is. v0.1 always derives a non-nil key from the
// bound identity; the nil branch exists for migration tooling that needs to
// inspect rows without unlock and is not exercised by the public API.

const (
	rowAADPrefix = "daimon-memory-row-v1"

	// MemoryEncryptionKeyLabel is the HKDF info string for deriving the
	// memory store's row-encryption key from the principal's Ed25519 seed.
	MemoryEncryptionKeyLabel = "daimon-memory-encryption-v1"

	// rowKeyLen, rowNonceLen, and rowCryptoVersion alias the secretbox
	// constants under their pre-refactor names so existing callers
	// (store.go's DeriveSubkey, the memory_test/crypto_test fixtures) can
	// reach the values without importing secretbox directly.
	rowKeyLen        = secretbox.KeyLen
	rowNonceLen      = secretbox.NonceLen
	rowCryptoVersion = secretbox.Version
)

// Errors surfaced by the row-encryption layer. Aliased to secretbox's
// sentinels so `errors.Is(err, memory.ErrInvalidCiphertext)` matches whether
// the error originates here, in the wrapper, or deeper in the AEAD primitive.
var (
	ErrInvalidCiphertext = secretbox.ErrInvalidCiphertext
	ErrInvalidKeyLength  = secretbox.ErrInvalidKeyLength
)

// encryptField returns the on-disk blob for plaintext under (memoryID, field).
// A nil key disables encryption — plaintext is returned as-is. An empty
// plaintext returns nil (the column is stored NULL).
func encryptField(key, plaintext []byte, memoryID, field string) ([]byte, error) {
	return secretbox.SealAAD(key, plaintext, buildRowAAD(memoryID, field))
}

// decryptField reverses encryptField. A nil key returns blob as-is. An empty
// blob returns nil. Any failure to authenticate the ciphertext under
// (memoryID, field) returns ErrInvalidCiphertext — including the case where
// the blob is plaintext from an unencrypted store being read by an encrypted
// one (no version byte / not a valid AEAD packet).
func decryptField(key, blob []byte, memoryID, field string) ([]byte, error) {
	return secretbox.OpenAAD(key, blob, buildRowAAD(memoryID, field))
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
