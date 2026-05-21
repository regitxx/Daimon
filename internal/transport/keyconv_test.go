package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// --- Clamping correctness ---------------------------------------------------

func TestEd25519PrivateToX25519_Clamping(t *testing.T) {
	// Whatever the input, the output MUST satisfy the RFC 7748 clamping:
	// - byte 0:  bits 0, 1, 2 cleared
	// - byte 31: bit 7 cleared, bit 6 set
	//
	// This is the most important property — wrong clamping makes the
	// scalar produce points outside the prime-order subgroup, which
	// breaks Noise's security proof. Run a bunch of random keys to
	// catch any case where the implementation forgets one of the
	// mask operations.
	for i := 0; i < 100; i++ {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		xPriv, err := Ed25519PrivateToX25519(priv)
		if err != nil {
			t.Fatalf("Ed25519PrivateToX25519: %v", err)
		}
		// Byte 0: bottom 3 bits clear.
		if xPriv[0]&0x07 != 0 {
			t.Errorf("iter %d: byte 0 = 0x%02x, bottom 3 bits not cleared", i, xPriv[0])
		}
		// Byte 31: bit 7 clear.
		if xPriv[31]&0x80 != 0 {
			t.Errorf("iter %d: byte 31 = 0x%02x, top bit not cleared", i, xPriv[31])
		}
		// Byte 31: bit 6 set.
		if xPriv[31]&0x40 == 0 {
			t.Errorf("iter %d: byte 31 = 0x%02x, bit 6 not set", i, xPriv[31])
		}
	}
}

// --- Determinism ------------------------------------------------------------

func TestEd25519PrivateToX25519_Deterministic(t *testing.T) {
	// Same Ed25519 input → same X25519 output, every time. Anti-
	// regression for "the SHA-512 input was accidentally salted by
	// something non-deterministic" bugs.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a, err := Ed25519PrivateToX25519(priv)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	for i := 0; i < 10; i++ {
		b, err := Ed25519PrivateToX25519(priv)
		if err != nil {
			t.Fatalf("iter %d call: %v", i, err)
		}
		if a != b {
			t.Errorf("iter %d: non-deterministic output", i)
		}
	}
}

// --- Diffie-Hellman correctness --------------------------------------------

func TestX25519DH_AliceAndBobMatch(t *testing.T) {
	// The fundamental correctness check: if Alice and Bob each derive
	// an X25519 keypair from their respective Ed25519 keys and do the
	// DH, they MUST end up with the same shared secret. This is the
	// property Noise IK depends on at every es/ee/se/ss step.
	_, aliceEd, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("alice GenerateKey: %v", err)
	}
	_, bobEd, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("bob GenerateKey: %v", err)
	}

	alicePriv, alicePub, err := Ed25519ToX25519Keypair(aliceEd)
	if err != nil {
		t.Fatalf("alice convert: %v", err)
	}
	bobPriv, bobPub, err := Ed25519ToX25519Keypair(bobEd)
	if err != nil {
		t.Fatalf("bob convert: %v", err)
	}

	aliceShared, err := X25519SharedSecret(alicePriv, bobPub)
	if err != nil {
		t.Fatalf("alice DH: %v", err)
	}
	bobShared, err := X25519SharedSecret(bobPriv, alicePub)
	if err != nil {
		t.Fatalf("bob DH: %v", err)
	}

	if aliceShared != bobShared {
		t.Errorf("DH mismatch:\n  alice: %x\n  bob:   %x", aliceShared, bobShared)
	}
}

// --- Independence from Ed25519 secret ---------------------------------------

func TestEd25519PrivateToX25519_DistinctSeedsDistinctOutputs(t *testing.T) {
	// Two independently-generated Ed25519 keypairs MUST produce two
	// distinct X25519 private keys with overwhelming probability.
	// Anti-regression for a "we forgot the SHA-512, the output is
	// just the seed" or "the output is constant" bug.
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)

	xA, _ := Ed25519PrivateToX25519(privA)
	xB, _ := Ed25519PrivateToX25519(privB)
	if xA == xB {
		t.Error("distinct Ed25519 inputs produced identical X25519 outputs — implementation likely broken")
	}
}

// --- One-way property ------------------------------------------------------

func TestEd25519PrivateToX25519_NotIdentity(t *testing.T) {
	// The X25519 private key MUST NOT equal the Ed25519 secret seed.
	// Anti-regression for "we accidentally just copied the seed
	// without the SHA-512 step" bugs.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	xPriv, _ := Ed25519PrivateToX25519(priv)
	seed := priv.Seed()
	identical := true
	for i := 0; i < X25519KeySize; i++ {
		if xPriv[i] != seed[i] {
			identical = false
			break
		}
	}
	if identical {
		t.Error("X25519 private key equals Ed25519 seed — SHA-512 step was skipped")
	}
}

// --- Input validation -------------------------------------------------------

func TestEd25519PrivateToX25519_RejectsWrongSize(t *testing.T) {
	short := make([]byte, 10)
	if _, err := Ed25519PrivateToX25519(short); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("short key: got %v, want ErrInvalidKey", err)
	}
	long := make([]byte, 200)
	if _, err := Ed25519PrivateToX25519(long); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("long key: got %v, want ErrInvalidKey", err)
	}
}

// --- X25519SharedSecret error path -----------------------------------------

func TestX25519SharedSecret_RejectsLowOrderPeerPubkey(t *testing.T) {
	// The all-zero u-coordinate is a known low-order point on
	// Curve25519. curve25519.X25519 returns an error for this case
	// (the "low-order point" defense). Our wrapper MUST surface
	// that error, not silently return a zero shared secret.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	xPriv, _ := Ed25519PrivateToX25519(priv)
	var zeroPub [X25519KeySize]byte // all zeros — low-order point
	if _, err := X25519SharedSecret(xPriv, zeroPub); err == nil {
		t.Error("expected error for all-zero peer pubkey (low-order point), got nil")
	}
}

// --- Ed25519PublicToX25519 ---------------------------------------------------

// TestEd25519PublicToX25519_RoundTrip is the primary correctness check: for
// any Ed25519 key pair, deriving the X25519 pubkey from the private key (via
// SHA-512 + scalar-mult-basepoint) must equal deriving it from the public key
// (via the Edwards→Montgomery birational map). Both are representations of the
// same group element; the algebraic identity guarantees they coincide.
func TestEd25519PublicToX25519_RoundTrip(t *testing.T) {
	for i := 0; i < 50; i++ {
		_, edPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("iter %d: GenerateKey: %v", i, err)
		}
		edPub := edPriv.Public().(ed25519.PublicKey)

		// Derive from private key (via seed → SHA-512 → X25519 private → X25519 public)
		_, xPubFromPriv, err := Ed25519ToX25519Keypair(edPriv)
		if err != nil {
			t.Fatalf("iter %d: Ed25519ToX25519Keypair: %v", i, err)
		}
		// Derive from public key (birational map)
		xPubFromPub, err := Ed25519PublicToX25519(edPub)
		if err != nil {
			t.Fatalf("iter %d: Ed25519PublicToX25519: %v", i, err)
		}
		if xPubFromPriv != xPubFromPub {
			t.Errorf("iter %d: round-trip mismatch:\n  from priv: %x\n  from pub:  %x", i, xPubFromPriv, xPubFromPub)
		}
	}
}

func TestEd25519PublicToX25519_Deterministic(t *testing.T) {
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	edPub := edPriv.Public().(ed25519.PublicKey)
	a, err := Ed25519PublicToX25519(edPub)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	for i := 0; i < 10; i++ {
		b, err := Ed25519PublicToX25519(edPub)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if a != b {
			t.Errorf("iter %d: non-deterministic output", i)
		}
	}
}

func TestEd25519PublicToX25519_RejectsWrongSize(t *testing.T) {
	if _, err := Ed25519PublicToX25519(make([]byte, 10)); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("short key: got %v, want ErrInvalidKey", err)
	}
	if _, err := Ed25519PublicToX25519(make([]byte, 64)); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("long key: got %v, want ErrInvalidKey", err)
	}
}

// --- X25519PublicFromPrivate matches Ed25519ToX25519Keypair ---------------

func TestX25519PublicFromPrivate_MatchesKeypairHelper(t *testing.T) {
	// The convenience helper and the two-step path MUST produce the
	// same X25519 pubkey for the same Ed25519 input.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	xPrivA, err := Ed25519PrivateToX25519(priv)
	if err != nil {
		t.Fatalf("Ed25519PrivateToX25519: %v", err)
	}
	xPubA, err := X25519PublicFromPrivate(xPrivA)
	if err != nil {
		t.Fatalf("X25519PublicFromPrivate: %v", err)
	}

	xPrivB, xPubB, err := Ed25519ToX25519Keypair(priv)
	if err != nil {
		t.Fatalf("Ed25519ToX25519Keypair: %v", err)
	}

	if xPrivA != xPrivB || xPubA != xPubB {
		t.Errorf("two-step vs helper diverge:\n  priv: %x vs %x\n  pub:  %x vs %x",
			xPrivA, xPrivB, xPubA, xPubB)
	}
}
