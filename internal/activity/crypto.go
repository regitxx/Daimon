package activity

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/regitxx/Daimon/internal/secretbox"
)

// Application-level activity-log payload encryption per SPEC §8.1.
//
// The `payload` field of each entry is stored on disk as
//
//	version(1B) || nonce(12B) || AES-256-GCM(plaintext_payload, AAD)
//
// base64-encoded into a JSON string so the JSONL file remains a sequence of
// well-formed JSON objects with the same key set as the unencrypted form (only
// the type of the `payload` value changes — JSON object → JSON string).
//
// AAD = "daimon-activity-payload-v1" || 0x00 || entry_id || 0x00 || "payload".
// The AAD binds each ciphertext to its entry id so a stolen ciphertext cannot
// be silently moved onto another entry.
//
// `id`, `ts`, `kind`, `prev_hash`, `hash`, and `signature` remain in clear so
// Query filtering (kind, time range), `LastHash` recovery on Open, and chain
// continuity all work without unlocking the key. The hash chain and signature
// are computed over the canonical *plaintext* entry — Verify recomputes the
// hash from the decrypted payload so chain integrity holds across the
// encryption boundary. An attacker who tampers with the ciphertext fails AEAD
// authentication before the chain check ever runs; an attacker who tampers
// with `prev_hash` or `hash` themselves still triggers ErrChainBroken /
// ErrHashMismatch.
//
// The encryption key is the 32-byte HKDF-SHA256 derivation of the principal's
// Ed25519 seed under the label ActivityEncryptionKeyLabel (see
// identity.DeriveSubkey). It never touches disk: rederived in memory at
// unlock, lives only as long as the unlocked daimond process, bound to the
// same trust scope (SPEC §9.2) as the unlocked private key.
//
// The AEAD primitive and the bytes-packed envelope live in
// internal/secretbox; this file contributes the AAD construction, the HKDF
// info label, and the JSONL-specific base64-into-JSON-string framing that
// make activity-payload encryption a domain-separated instance of the shared
// envelope.
//
// Encryption is enabled iff Log.key is non-nil. v0.1 always derives a non-nil
// key from the bound identity at Open; the nil branch exists only for
// migration tooling and is not exercised by the public Open path.

const (
	payloadAADPrefix = "daimon-activity-payload-v1"
	payloadAADField  = "payload"

	// ActivityEncryptionKeyLabel is the HKDF info string for deriving the
	// activity log's payload-encryption key from the principal's Ed25519
	// seed.
	ActivityEncryptionKeyLabel = "daimon-activity-encryption-v1"
)

// Errors surfaced by the payload-encryption layer. Aliased to secretbox's
// sentinels so `errors.Is(err, activity.ErrInvalidCiphertext)` matches whether
// the error originates here, in the wrapper, or deeper in the AEAD primitive.
var (
	ErrInvalidCiphertext = secretbox.ErrInvalidCiphertext
	ErrInvalidKeyLength  = secretbox.ErrInvalidKeyLength
)

// encodePayloadForDisk takes plaintext payload bytes and returns the wire-form
// JSON value to store under the `payload` field. With encryption enabled
// (non-nil key, non-empty plaintext), the result is the JSON-encoded base64
// string of the AEAD envelope. With a nil key, plaintext is returned as-is so
// the Entry round-trips through json.RawMessage unchanged. Empty plaintext
// returns nil so the field is omitted via `omitempty`.
func encodePayloadForDisk(key, plaintext []byte, entryID string) (json.RawMessage, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	if key == nil {
		return plaintext, nil
	}
	ct, err := secretbox.SealAAD(key, plaintext, buildPayloadAAD(entryID))
	if err != nil {
		return nil, err
	}
	enc, err := json.Marshal(base64.StdEncoding.EncodeToString(ct))
	if err != nil {
		return nil, fmt.Errorf("marshal ciphertext: %w", err)
	}
	return enc, nil
}

// decodePayloadFromDisk reverses encodePayloadForDisk: given the on-disk
// `payload` field as a json.RawMessage, returns the plaintext payload bytes
// suitable for assignment back into Entry.Payload. With encryption enabled,
// the wire form must be a JSON string of base64-encoded ciphertext; anything
// else (a JSON object, a non-base64 string, a too-short blob) surfaces as
// ErrInvalidCiphertext rather than silent corruption.
func decodePayloadFromDisk(key []byte, payload json.RawMessage, entryID string) (json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if key == nil {
		return payload, nil
	}
	var s string
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, fmt.Errorf("%w: payload not a JSON string: %v", ErrInvalidCiphertext, err)
	}
	ct, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: base64 decode: %v", ErrInvalidCiphertext, err)
	}
	pt, err := secretbox.OpenAAD(key, ct, buildPayloadAAD(entryID))
	if err != nil {
		return nil, err
	}
	return pt, nil
}

func buildPayloadAAD(entryID string) []byte {
	aad := make([]byte, 0, len(payloadAADPrefix)+1+len(entryID)+1+len(payloadAADField))
	aad = append(aad, payloadAADPrefix...)
	aad = append(aad, 0x00)
	aad = append(aad, entryID...)
	aad = append(aad, 0x00)
	aad = append(aad, payloadAADField...)
	return aad
}
