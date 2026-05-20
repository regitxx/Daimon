package transport

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/flynn/noise"
)

// Noise IK handshake wrapper. The flynn/noise library implements the
// Noise Protocol Framework's full pattern catalogue; this file is a
// thin Daimon-flavored API on top of it that:
//
//   - Takes Ed25519 identity keys (what the daimon already has) and
//     internally derives the X25519 keys Noise IK needs (via the
//     keyconv.go primitives).
//   - Picks the canonical Noise_IK suite (X25519 + ChaCha20-Poly1305
//     + SHA-256) so callers never have to think about cipher choice.
//     This matches design Decision 1's framing — one pattern, one
//     suite, no negotiation surface.
//   - Surfaces the two-message handshake as Initiate/Respond pairs
//     plus the resulting CipherState pair for post-handshake
//     channel encrypt/decrypt.
//
// Noise IK in 30 seconds: both sides have static keys. The initiator
// ALREADY KNOWS the responder's static public key (e.g. from the
// daimon's DID document's TransportPubKey field). The handshake is
// two messages:
//
//   1. Initiator → Responder: e, es, s, ss   (encrypted)
//   2. Responder → Initiator: e, ee, se      (encrypted)
//
// After 2 messages both sides have a shared encrypted channel and
// have mutually authenticated each other's static keys. No PKI,
// no certs, no CAs — the static keys themselves are the identity.
//
// The handshake messages here are byte slices that the caller is
// responsible for transmitting. Slice 31.2 (QUIC transport) will
// thread these through a QUIC stream.

// NoiseSuite is the canonical Noise IK cipher suite this transport
// uses. Pinned so different daimons can't accidentally negotiate
// different suites — there's only one.
var NoiseSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// HandshakePattern is Noise IK — initiator knows responder's static
// pubkey ahead of time, both sides have static keypairs, two-message
// handshake. Per design Decision 1.
var HandshakePattern = noise.HandshakeIK

// HandshakePrologue is a Daimon-specific opaque blob mixed into the
// handshake's symmetric state. Both sides MUST agree on it for the
// handshake to succeed; pinning it to a version string lets us
// reject handshakes from clients running an incompatible version of
// the transport protocol with a clean "handshake failed" rather
// than a mysterious cipher-decrypt failure mid-channel.
var HandshakePrologue = []byte("daimon-noise-ik-v0.3-draft")

// HandshakeState is the live handshake — an initiator or responder
// running through the two-message exchange. Wraps flynn/noise's
// HandshakeState with daimon-specific construction logic.
type HandshakeState struct {
	hs       *noise.HandshakeState
	isInit   bool
	complete bool

	// Once the handshake completes, the cipher states are extracted
	// from hs and stored here so a caller can encrypt + decrypt
	// post-handshake data on the channel.
	sendCS *noise.CipherState
	recvCS *noise.CipherState
}

// NewInitiator constructs a HandshakeState in initiator role. The
// caller supplies their own Ed25519 private key (the static key,
// converted internally to X25519) AND the responder's known
// X25519 public key (typically pulled from the responder's
// DaimonEndpoint TransportPubKey).
//
// Returns ErrInvalidKey if either key is the wrong size; returns
// other errors only if the underlying flynn/noise rejects the
// configuration (which shouldn't happen for valid inputs).
func NewInitiator(edPriv ed25519.PrivateKey, responderX25519Pub [X25519KeySize]byte) (*HandshakeState, error) {
	xPriv, xPub, err := Ed25519ToX25519Keypair(edPriv)
	if err != nil {
		return nil, fmt.Errorf("noise initiator: key conversion: %w", err)
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: NoiseSuite,
		Pattern:     HandshakePattern,
		Initiator:   true,
		Prologue:    HandshakePrologue,
		StaticKeypair: noise.DHKey{
			Private: xPriv[:],
			Public:  xPub[:],
		},
		PeerStatic: responderX25519Pub[:],
	})
	if err != nil {
		return nil, fmt.Errorf("noise initiator: %w", err)
	}
	return &HandshakeState{hs: hs, isInit: true}, nil
}

// NewResponder constructs a HandshakeState in responder role. The
// caller supplies their own Ed25519 private key (converted to
// X25519 internally). The initiator's identity comes IN with the
// first handshake message, so we don't need it ahead of time.
func NewResponder(edPriv ed25519.PrivateKey) (*HandshakeState, error) {
	xPriv, xPub, err := Ed25519ToX25519Keypair(edPriv)
	if err != nil {
		return nil, fmt.Errorf("noise responder: key conversion: %w", err)
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: NoiseSuite,
		Pattern:     HandshakePattern,
		Initiator:   false,
		Prologue:    HandshakePrologue,
		StaticKeypair: noise.DHKey{
			Private: xPriv[:],
			Public:  xPub[:],
		},
	})
	if err != nil {
		return nil, fmt.Errorf("noise responder: %w", err)
	}
	return &HandshakeState{hs: hs, isInit: false}, nil
}

// WriteMessage produces the next handshake message to send to the
// peer. The optional payload is encrypted as part of the message —
// useful for "0-RTT data" piggybacking (initiator's first message
// can include an early ping); v0.3 baseline passes nil here and
// reserves payload-in-handshake for later optimisation.
//
// After the second message of the handshake, this method ALSO
// extracts the cipher states for post-handshake transport. Check
// IsComplete() after writing.
func (h *HandshakeState) WriteMessage(payload []byte) ([]byte, error) {
	if h.complete {
		return nil, ErrHandshakeComplete
	}
	out := make([]byte, 0, len(payload)+128)
	out, cs1, cs2, err := h.hs.WriteMessage(out, payload)
	if err != nil {
		return nil, fmt.Errorf("noise write: %w", err)
	}
	if cs1 != nil && cs2 != nil {
		h.complete = true
		// Initiator's send cipher is cs1, recv is cs2.
		// Responder's send cipher is cs2, recv is cs1.
		// (per Noise spec: WriteMessage returns (initiator_send,
		// responder_send) on the side that completes the handshake.)
		if h.isInit {
			h.sendCS = cs1
			h.recvCS = cs2
		} else {
			h.sendCS = cs2
			h.recvCS = cs1
		}
	}
	return out, nil
}

// ReadMessage consumes a handshake message from the peer + returns
// the decrypted payload (which is empty if the peer didn't include
// any 0-RTT data). Like WriteMessage, this also extracts cipher
// states when it completes the handshake.
func (h *HandshakeState) ReadMessage(in []byte) ([]byte, error) {
	if h.complete {
		return nil, ErrHandshakeComplete
	}
	out := make([]byte, 0, len(in))
	out, cs1, cs2, err := h.hs.ReadMessage(out, in)
	if err != nil {
		return nil, fmt.Errorf("noise read: %w", err)
	}
	if cs1 != nil && cs2 != nil {
		h.complete = true
		if h.isInit {
			h.sendCS = cs1
			h.recvCS = cs2
		} else {
			h.sendCS = cs2
			h.recvCS = cs1
		}
	}
	return out, nil
}

// IsComplete reports whether the handshake has finished. A caller
// running through the two-message exchange should check this after
// each WriteMessage / ReadMessage call; once true, switch to using
// Encrypt / Decrypt for channel data.
func (h *HandshakeState) IsComplete() bool {
	return h.complete
}

// PeerStatic returns the peer's static public key. For an initiator
// this is the same key passed to NewInitiator (round-tripped for
// API completeness). For a responder, this is the initiator's
// static key as revealed by the first handshake message — available
// only after ReadMessage on the first inbound message.
//
// Returns the zero array if the peer's static key isn't known yet
// (responder before the first inbound message).
func (h *HandshakeState) PeerStatic() [X25519KeySize]byte {
	var out [X25519KeySize]byte
	peer := h.hs.PeerStatic()
	if len(peer) == X25519KeySize {
		copy(out[:], peer)
	}
	return out
}

// Encrypt produces ciphertext for application data over the
// established channel. MUST only be called after IsComplete()
// returns true. Each successive call increments the internal
// nonce — Noise spec mandates strict ordering, callers MUST NOT
// reorder Encrypt outputs on the wire or the peer's Decrypt will
// reject.
//
// associatedData is mixed into the AEAD computation (AAD). Empty
// is fine; if the wire format includes a frame header, that
// header is a natural AAD.
func (h *HandshakeState) Encrypt(out, associatedData, plaintext []byte) ([]byte, error) {
	if !h.complete {
		return nil, ErrHandshakeNotComplete
	}
	if h.sendCS == nil {
		return nil, errors.New("noise: send cipher state is nil")
	}
	result, err := h.sendCS.Encrypt(out, associatedData, plaintext)
	if err != nil {
		return nil, fmt.Errorf("noise encrypt: %w", err)
	}
	return result, nil
}

// Decrypt consumes ciphertext from the peer + returns the
// plaintext. Symmetric to Encrypt. Returns an error if the
// ciphertext doesn't verify under the current nonce — typically
// indicates the wire was tampered with, the nonce was reordered,
// or the channel diverged.
func (h *HandshakeState) Decrypt(out, associatedData, ciphertext []byte) ([]byte, error) {
	if !h.complete {
		return nil, ErrHandshakeNotComplete
	}
	if h.recvCS == nil {
		return nil, errors.New("noise: recv cipher state is nil")
	}
	return h.recvCS.Decrypt(out, associatedData, ciphertext)
}

// Errors surfaced by the Noise wrapper.
var (
	// ErrHandshakeComplete is returned when WriteMessage or
	// ReadMessage is called after the handshake has finished.
	// Use Encrypt/Decrypt for post-handshake data.
	ErrHandshakeComplete = errors.New("noise: handshake already complete")

	// ErrHandshakeNotComplete is returned when Encrypt or Decrypt
	// is called before the handshake has finished.
	ErrHandshakeNotComplete = errors.New("noise: handshake not yet complete")
)
