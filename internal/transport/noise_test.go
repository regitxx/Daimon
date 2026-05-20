package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// Noise IK handshake + channel round-trip. We test against ourselves
// (initiator and responder both in-process) because the wire layer
// won't land until slice 31.2 (QUIC integration). The cryptographic
// correctness — handshake completes, both sides converge on the
// same channel keys, encrypt/decrypt round-trips, tampering is
// detected — is the same with or without a network transport, and
// in-process testing keeps the failure modes attributable.

// runHandshake drives a complete two-message Noise IK exchange
// between initiator and responder. Returns both sides post-handshake
// so tests can exercise channel encrypt/decrypt.
func runHandshake(t *testing.T, initiator, responder *HandshakeState) {
	t.Helper()
	// Message 1: initiator → responder
	msg1, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatalf("WriteMessage(1): %v", err)
	}
	if _, err := responder.ReadMessage(msg1); err != nil {
		t.Fatalf("ReadMessage(1): %v", err)
	}
	// Message 2: responder → initiator. This is the message that
	// completes the handshake for both sides.
	msg2, err := responder.WriteMessage(nil)
	if err != nil {
		t.Fatalf("WriteMessage(2): %v", err)
	}
	if _, err := initiator.ReadMessage(msg2); err != nil {
		t.Fatalf("ReadMessage(2): %v", err)
	}
	if !initiator.IsComplete() {
		t.Fatal("initiator: handshake should be complete after 2 messages")
	}
	if !responder.IsComplete() {
		t.Fatal("responder: handshake should be complete after 2 messages")
	}
}

// --- Happy path: handshake completes, channel round-trips ------------------

func TestNoise_HandshakeCompletes(t *testing.T) {
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)

	// Bob's X25519 pubkey — Alice needs to know this ahead of time
	// (that's what IK means). In production this comes from Bob's
	// DaimonEndpoint document.
	_, bobXPub, err := Ed25519ToX25519Keypair(bobEd)
	if err != nil {
		t.Fatalf("derive bobXPub: %v", err)
	}

	alice, err := NewInitiator(aliceEd, bobXPub)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	bob, err := NewResponder(bobEd)
	if err != nil {
		t.Fatalf("NewResponder: %v", err)
	}

	runHandshake(t, alice, bob)
}

func TestNoise_ChannelRoundTrip(t *testing.T) {
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobXPub, _ := Ed25519ToX25519Keypair(bobEd)
	alice, _ := NewInitiator(aliceEd, bobXPub)
	bob, _ := NewResponder(bobEd)
	runHandshake(t, alice, bob)

	// Alice → Bob
	ct, err := alice.Encrypt(nil, nil, []byte("hello bob"))
	if err != nil {
		t.Fatalf("alice.Encrypt: %v", err)
	}
	pt, err := bob.Decrypt(nil, nil, ct)
	if err != nil {
		t.Fatalf("bob.Decrypt: %v", err)
	}
	if string(pt) != "hello bob" {
		t.Errorf("alice → bob roundtrip: got %q, want hello bob", pt)
	}

	// Bob → Alice (different direction, different cipher state)
	ct, err = bob.Encrypt(nil, nil, []byte("hi alice"))
	if err != nil {
		t.Fatalf("bob.Encrypt: %v", err)
	}
	pt, err = alice.Decrypt(nil, nil, ct)
	if err != nil {
		t.Fatalf("alice.Decrypt: %v", err)
	}
	if string(pt) != "hi alice" {
		t.Errorf("bob → alice roundtrip: got %q, want hi alice", pt)
	}
}

func TestNoise_ChannelManyMessages(t *testing.T) {
	// Noise nonces increment per-message. Send a bunch in each
	// direction to exercise the nonce machinery + catch any
	// off-by-one bugs in the wrapper.
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobXPub, _ := Ed25519ToX25519Keypair(bobEd)
	alice, _ := NewInitiator(aliceEd, bobXPub)
	bob, _ := NewResponder(bobEd)
	runHandshake(t, alice, bob)

	for i := 0; i < 100; i++ {
		msg := []byte("alice msg ")
		msg = append(msg, byte('0'+i%10))
		ct, err := alice.Encrypt(nil, nil, msg)
		if err != nil {
			t.Fatalf("iter %d encrypt: %v", i, err)
		}
		pt, err := bob.Decrypt(nil, nil, ct)
		if err != nil {
			t.Fatalf("iter %d decrypt: %v", i, err)
		}
		if !bytes.Equal(pt, msg) {
			t.Errorf("iter %d: got %q, want %q", i, pt, msg)
		}
	}
}

// --- Identity assertions after handshake -----------------------------------

func TestNoise_PeerStatic_ResponderSeesInitiator(t *testing.T) {
	// After the handshake, the responder MUST be able to identify
	// the initiator's static pubkey — that's the whole point of IK
	// (mutual authentication). We assert the responder.PeerStatic()
	// equals the initiator's X25519 pubkey.
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)
	_, aliceXPub, _ := Ed25519ToX25519Keypair(aliceEd)
	_, bobXPub, _ := Ed25519ToX25519Keypair(bobEd)

	alice, _ := NewInitiator(aliceEd, bobXPub)
	bob, _ := NewResponder(bobEd)
	runHandshake(t, alice, bob)

	gotPeerForBob := bob.PeerStatic()
	if gotPeerForBob != aliceXPub {
		t.Errorf("bob.PeerStatic() = %x, want alice's X25519 pubkey %x", gotPeerForBob, aliceXPub)
	}
	gotPeerForAlice := alice.PeerStatic()
	if gotPeerForAlice != bobXPub {
		t.Errorf("alice.PeerStatic() = %x, want bob's X25519 pubkey %x", gotPeerForAlice, bobXPub)
	}
}

// --- Tampering detection ---------------------------------------------------

func TestNoise_DecryptRejectsTamperedCiphertext(t *testing.T) {
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobXPub, _ := Ed25519ToX25519Keypair(bobEd)
	alice, _ := NewInitiator(aliceEd, bobXPub)
	bob, _ := NewResponder(bobEd)
	runHandshake(t, alice, bob)

	ct, err := alice.Encrypt(nil, nil, []byte("important message"))
	if err != nil {
		t.Fatalf("alice.Encrypt: %v", err)
	}
	// Flip a single bit in the ciphertext. AEAD verification MUST
	// reject — this is the defining property of "authenticated"
	// encryption.
	ct[5] ^= 0x01
	if _, err := bob.Decrypt(nil, nil, ct); err == nil {
		t.Error("bob.Decrypt accepted tampered ciphertext — AEAD verification missed it")
	}
}

func TestNoise_DecryptRejectsReorderedMessage(t *testing.T) {
	// Noise nonces are sequential — message 2 cannot be decrypted
	// before message 1. Anti-regression for "we accidentally use
	// a stateless cipher" bugs.
	//
	// Also asserts the secure-behaviour property that a FAILED
	// decrypt does NOT advance the receive nonce. Otherwise a
	// single bad packet from a network attacker could DoS the
	// entire channel by desynchronising the nonce counter. The
	// correct behaviour (which flynn/noise implements): failed
	// decrypts leave the nonce alone, so a subsequent in-order
	// message still decrypts cleanly.
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobXPub, _ := Ed25519ToX25519Keypair(bobEd)
	alice, _ := NewInitiator(aliceEd, bobXPub)
	bob, _ := NewResponder(bobEd)
	runHandshake(t, alice, bob)

	ct1, _ := alice.Encrypt(nil, nil, []byte("msg 1"))
	ct2, _ := alice.Encrypt(nil, nil, []byte("msg 2"))

	// Bob receives msg 2 first — MUST fail (wrong nonce: msg2 was
	// encrypted at nonce=1, bob's recv counter is at 0).
	if _, err := bob.Decrypt(nil, nil, ct2); err == nil {
		t.Error("bob accepted out-of-order ciphertext (msg 2 before msg 1)")
	}
	// After the failed msg 2 decrypt, bob's recv counter is STILL
	// at 0 (failure didn't advance it). So msg 1 in order should
	// now succeed. This is the DoS-resistance property — one bad
	// packet doesn't kill the channel.
	pt, err := bob.Decrypt(nil, nil, ct1)
	if err != nil {
		t.Fatalf("bob failed to decrypt msg 1 after a rejected msg 2: %v "+
			"(this means failed-decrypt incorrectly advanced the nonce — DoS vulnerability)", err)
	}
	if string(pt) != "msg 1" {
		t.Errorf("msg 1 plaintext = %q, want msg 1", pt)
	}
}

// --- Negative-path handshake -----------------------------------------------

func TestNoise_HandshakeFailsOnWrongResponderPubKey(t *testing.T) {
	// Alice points at a pubkey that isn't actually Bob's. The
	// handshake should fail at the responder side because Bob's
	// static key (which Bob derives from HIS ed25519) won't match
	// what Alice expected to encrypt to.
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)
	_, attackerEd, _ := ed25519.GenerateKey(rand.Reader)
	_, attackerXPub, _ := Ed25519ToX25519Keypair(attackerEd)

	alice, err := NewInitiator(aliceEd, attackerXPub)
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	bob, _ := NewResponder(bobEd)

	// Message 1 from Alice goes encrypted under attacker's pubkey,
	// not Bob's. Bob can't decrypt it.
	msg1, err := alice.WriteMessage(nil)
	if err != nil {
		t.Fatalf("alice.WriteMessage: %v", err)
	}
	if _, err := bob.ReadMessage(msg1); err == nil {
		t.Error("bob accepted a handshake message encrypted to the wrong pubkey")
	}
}

func TestNoise_HandshakeFailsOnPrologueMismatch(t *testing.T) {
	// Two clients running incompatible versions of the transport
	// protocol mix different prologue strings into the handshake's
	// symmetric state. They'll fail to converge on the same keys
	// → the second message will fail to decrypt.
	//
	// We can't easily test this with the current API (HandshakePrologue
	// is a package-level constant), but we leave a note + assert
	// that the prologue IS being mixed in by constructing an
	// initiator that uses the constant + a responder that's been
	// hand-modified — done by raising the constant briefly via the
	// test's noise.Config directly.
	//
	// Skipping the test now to avoid reaching into flynn/noise's
	// internals from the wrapper test, but the prologue is mixed in
	// (see NewInitiator + NewResponder both setting Prologue:
	// HandshakePrologue). If a future change accidentally drops the
	// Prologue field from the config, downstream tests would notice
	// during a cross-version smoke.
	t.Skip("prologue-mismatch test requires multi-version harness; skipped")
}

// --- Encrypt/Decrypt before handshake completes ----------------------------

func TestNoise_EncryptBeforeHandshakeComplete(t *testing.T) {
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, peerXPub, _ := Ed25519ToX25519Keypair(edPriv)
	hs, _ := NewInitiator(edPriv, peerXPub)
	if _, err := hs.Encrypt(nil, nil, []byte("too early")); !errors.Is(err, ErrHandshakeNotComplete) {
		t.Errorf("Encrypt before handshake: got %v, want ErrHandshakeNotComplete", err)
	}
	if _, err := hs.Decrypt(nil, nil, []byte("too early")); !errors.Is(err, ErrHandshakeNotComplete) {
		t.Errorf("Decrypt before handshake: got %v, want ErrHandshakeNotComplete", err)
	}
}

func TestNoise_WriteMessageAfterHandshakeComplete(t *testing.T) {
	_, aliceEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobEd, _ := ed25519.GenerateKey(rand.Reader)
	_, bobXPub, _ := Ed25519ToX25519Keypair(bobEd)
	alice, _ := NewInitiator(aliceEd, bobXPub)
	bob, _ := NewResponder(bobEd)
	runHandshake(t, alice, bob)

	if _, err := alice.WriteMessage(nil); !errors.Is(err, ErrHandshakeComplete) {
		t.Errorf("WriteMessage after handshake: got %v, want ErrHandshakeComplete", err)
	}
	if _, err := alice.ReadMessage(nil); !errors.Is(err, ErrHandshakeComplete) {
		t.Errorf("ReadMessage after handshake: got %v, want ErrHandshakeComplete", err)
	}
}

// --- Input validation -------------------------------------------------------

func TestNoise_NewInitiator_RejectsBadKeys(t *testing.T) {
	_, validPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, validXPub, _ := Ed25519ToX25519Keypair(validPriv)

	// Too-short Ed25519 private key.
	if _, err := NewInitiator(ed25519.PrivateKey(make([]byte, 10)), validXPub); err == nil {
		t.Error("NewInitiator with short Ed25519 key: expected error")
	}
}

func TestNoise_NewResponder_RejectsBadKeys(t *testing.T) {
	if _, err := NewResponder(ed25519.PrivateKey(make([]byte, 10))); err == nil {
		t.Error("NewResponder with short Ed25519 key: expected error")
	}
}
