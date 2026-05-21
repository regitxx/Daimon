// Package identity implements the identity primitive for Daimon: Ed25519
// keypair generation, did:key encoding, and encrypted at-rest keystore.
package identity

import (
	"crypto/ed25519"
	"errors"
	"strings"

	"github.com/mr-tron/base58"
)

const (
	multicodecEd25519PubByte0 = 0xed
	multicodecEd25519PubByte1 = 0x01
	multibaseBase58btc        = 'z'
	didKeyPrefix              = "did:key:"
)

var ErrInvalidDIDKey = errors.New("invalid did:key")

// EncodeDIDKey encodes an Ed25519 public key as a did:key DID per the
// W3C did:key Method specification.
func EncodeDIDKey(pub ed25519.PublicKey) string {
	if len(pub) != ed25519.PublicKeySize {
		panic("ed25519 public key has wrong size")
	}
	prefixed := make([]byte, 2+len(pub))
	prefixed[0] = multicodecEd25519PubByte0
	prefixed[1] = multicodecEd25519PubByte1
	copy(prefixed[2:], pub)
	return didKeyPrefix + string(multibaseBase58btc) + base58.Encode(prefixed)
}

// DecodeDIDKey extracts the Ed25519 public key from a did:key DID.
func DecodeDIDKey(did string) (ed25519.PublicKey, error) {
	if !strings.HasPrefix(did, didKeyPrefix) {
		return nil, ErrInvalidDIDKey
	}
	body := strings.TrimPrefix(did, didKeyPrefix)
	if len(body) < 2 || body[0] != multibaseBase58btc {
		return nil, ErrInvalidDIDKey
	}
	decoded, err := base58.Decode(body[1:])
	if err != nil {
		return nil, ErrInvalidDIDKey
	}
	if len(decoded) != 2+ed25519.PublicKeySize {
		return nil, ErrInvalidDIDKey
	}
	if decoded[0] != multicodecEd25519PubByte0 || decoded[1] != multicodecEd25519PubByte1 {
		return nil, ErrInvalidDIDKey
	}
	return ed25519.PublicKey(decoded[2:]), nil
}

// MultibaseFragment returns the base58btc multibase-encoded form of the
// public key, suitable for use as the verification method fragment in a
// DID document.
func MultibaseFragment(did string) string {
	return strings.TrimPrefix(did, didKeyPrefix)
}

// PublicKeyFromDID extracts the Ed25519 public key from a did:key DID.
// It is a convenience alias for DecodeDIDKey.
// Returns ErrInvalidDIDKey if the DID is not a valid did:key.
func PublicKeyFromDID(did string) (ed25519.PublicKey, error) {
	return DecodeDIDKey(did)
}
