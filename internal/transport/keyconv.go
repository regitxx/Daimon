// Package transport implements the v0.3 federation transport layer:
// Ed25519 → X25519 key conversion (this file), Noise IK handshake
// over QUIC (subsequent slices), connection multiplexing + lifecycle.
//
// The transport layer's job is "given a peer's DID, establish an
// authenticated, encrypted, multiplexed channel to that peer." Per
// design/v0.3-federation.md Decision 1, the channel is Noise IK over
// QUIC with the daimon's Ed25519 identity key as the cryptographic
// anchor.
//
// Noise IK uses X25519 (Curve25519) for the Diffie-Hellman component,
// not Ed25519. The conversion in this file is the standard one-way
// derivation: take the Ed25519 secret seed, hash + clamp it per
// RFC 7748, and you get a valid X25519 secret. The X25519 public key
// is then a deterministic scalar mult of that secret against the
// Curve25519 basepoint.
package transport

import (
	"crypto/ed25519"
	"crypto/sha512"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// X25519KeySize is 32 bytes for both private and public X25519 keys.
const X25519KeySize = 32

// Ed25519PrivateToX25519 derives the X25519 private key that
// corresponds to an Ed25519 private key. The conversion is the
// standard one used by libsodium's crypto_sign_ed25519_sk_to_curve25519,
// Signal Protocol, age, and every other Ed25519+X25519 hybrid:
//
//  1. Take the 32-byte Ed25519 secret seed (first 32 bytes of the
//     64-byte private key; the second 32 bytes are the derived
//     public key, which we don't need here).
//  2. SHA-512 the seed → 64 bytes.
//  3. The first 32 bytes of that hash become the X25519 private
//     key, after applying the standard Curve25519 clamping per
//     RFC 7748 §5: clear bits 0/1/2 of byte 0, clear bit 7 of byte 31,
//     set bit 6 of byte 31.
//
// The conversion is one-way: given the X25519 private key, you
// cannot recover the Ed25519 private key (the SHA-512 step is the
// hash-trapdoor). This is intentional — it means leaking the
// transport key does not leak the identity key.
//
// Returns ErrInvalidKey if priv is not exactly ed25519.PrivateKeySize.
func Ed25519PrivateToX25519(priv ed25519.PrivateKey) ([X25519KeySize]byte, error) {
	var out [X25519KeySize]byte
	if len(priv) != ed25519.PrivateKeySize {
		return out, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidKey, len(priv), ed25519.PrivateKeySize)
	}
	// The Ed25519 "private key" in Go's std lib is 64 bytes:
	// seed(32) || pubkey(32). The X25519 derivation only uses the
	// seed half.
	seed := priv.Seed()
	h := sha512.Sum512(seed)
	copy(out[:], h[:X25519KeySize])

	// Curve25519 clamping per RFC 7748 §5. This ensures the scalar
	// is in the right form for the X25519 scalar mult: it makes
	// the bottom three bits zero (so multiplication by 8 stays in
	// the prime-order subgroup) and pins the top bit pattern (so
	// the scalar is always near 2^254, avoiding small-subgroup
	// attacks). The exact bit operations come from
	// https://datatracker.ietf.org/doc/html/rfc7748#section-5.
	out[0] &= 0xF8  // clear bits 0, 1, 2
	out[31] &= 0x7F // clear bit 7
	out[31] |= 0x40 // set bit 6

	return out, nil
}

// X25519PublicFromPrivate computes the X25519 public key (a Montgomery
// u-coordinate, 32 bytes) corresponding to a clamped X25519 private
// key. It's a scalar mult of the private key against the Curve25519
// basepoint — the standard "give me the pubkey for this privkey"
// primitive.
//
// Use this when you have an X25519 private key (typically from
// Ed25519PrivateToX25519) and want the matching public key to
// publish or hand to a peer.
//
// Note: x/crypto/curve25519.X25519 returns an error only for
// "low-order points" — which we never hit here because we're
// multiplying by the canonical basepoint. The error path is
// preserved for API completeness.
func X25519PublicFromPrivate(priv [X25519KeySize]byte) ([X25519KeySize]byte, error) {
	var out [X25519KeySize]byte
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return out, fmt.Errorf("X25519 scalar-mult-basepoint: %w", err)
	}
	copy(out[:], pub)
	return out, nil
}

// X25519SharedSecret computes the X25519 Diffie-Hellman shared secret
// between our private key and a peer's public key. This is the
// fundamental DH primitive that Noise IK builds its `es`/`ee`/`se`/
// `ss` mixing steps on.
//
// Returns an error if the peer's public key is on a small-order
// subgroup (the curve25519 package's defense against degenerate
// shared secrets). Callers MUST treat that error as "peer is
// malicious or broken" and abort the handshake.
func X25519SharedSecret(priv [X25519KeySize]byte, peerPub [X25519KeySize]byte) ([X25519KeySize]byte, error) {
	var out [X25519KeySize]byte
	shared, err := curve25519.X25519(priv[:], peerPub[:])
	if err != nil {
		return out, fmt.Errorf("X25519 DH: %w", err)
	}
	copy(out[:], shared)
	return out, nil
}

// Ed25519ToX25519Keypair is the convenience entry point: take an
// Ed25519 private key, derive both X25519 halves. Equivalent to
// calling Ed25519PrivateToX25519 then X25519PublicFromPrivate.
// Returned in (priv, pub) order.
func Ed25519ToX25519Keypair(edPriv ed25519.PrivateKey) (xPriv, xPub [X25519KeySize]byte, err error) {
	xPriv, err = Ed25519PrivateToX25519(edPriv)
	if err != nil {
		return xPriv, xPub, err
	}
	xPub, err = X25519PublicFromPrivate(xPriv)
	return xPriv, xPub, err
}

// ErrInvalidKey is returned by the key-conversion functions when the
// input is the wrong size. Resolution / handshake errors live in
// other files of this package.
var ErrInvalidKey = errors.New("transport: invalid key size")
