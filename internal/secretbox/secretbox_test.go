package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"testing"
)

func freshKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeyLen)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestSealOpenRoundtrip(t *testing.T) {
	key := freshKey(t)
	pt := []byte("daimon at-rest envelope")
	aad := []byte("context-1")

	ct, err := SealAAD(key, pt, aad)
	if err != nil {
		t.Fatalf("SealAAD: %v", err)
	}
	if bytes.Equal(ct, pt) {
		t.Fatal("ciphertext must not equal plaintext")
	}
	if ct[0] != Version {
		t.Errorf("version byte = 0x%02x, want 0x%02x", ct[0], Version)
	}
	if len(ct) != 1+NonceLen+len(pt)+gcmTagLen {
		t.Errorf("envelope length = %d, want %d", len(ct), 1+NonceLen+len(pt)+gcmTagLen)
	}
	got, err := OpenAAD(key, ct, aad)
	if err != nil {
		t.Fatalf("OpenAAD: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Errorf("plaintext mismatch:\n got:  %q\n want: %q", got, pt)
	}
}

func TestSealRandomizesNonce(t *testing.T) {
	key := freshKey(t)
	pt := []byte("identical input")
	aad := []byte("aad")
	a, err := SealAAD(key, pt, aad)
	if err != nil {
		t.Fatalf("seal a: %v", err)
	}
	b, err := SealAAD(key, pt, aad)
	if err != nil {
		t.Fatalf("seal b: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext produced identical envelopes (nonce reuse?)")
	}
}

func TestOpenRejectsForeignAAD(t *testing.T) {
	key := freshKey(t)
	pt := []byte("payload")
	ct, err := SealAAD(key, pt, []byte("aad-a"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := OpenAAD(key, ct, []byte("aad-b")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on different AAD, got %v", err)
	}
}

func TestOpenRejectsForeignKey(t *testing.T) {
	keyA := freshKey(t)
	keyB := freshKey(t)
	ct, err := SealAAD(keyA, []byte("data"), []byte("aad"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := OpenAAD(keyB, ct, []byte("aad")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on foreign-key open, got %v", err)
	}
}

func TestOpenRejectsBitFlip(t *testing.T) {
	key := freshKey(t)
	ct, err := SealAAD(key, []byte("hello"), []byte("aad"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := OpenAAD(key, tampered, []byte("aad")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on bit flip, got %v", err)
	}
}

func TestOpenRejectsUnknownVersion(t *testing.T) {
	key := freshKey(t)
	ct, err := SealAAD(key, []byte("hello"), []byte("aad"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	tampered := append([]byte(nil), ct...)
	tampered[0] = 0xFF
	if _, err := OpenAAD(key, tampered, []byte("aad")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on unknown version, got %v", err)
	}
}

func TestOpenRejectsTruncatedBlob(t *testing.T) {
	key := freshKey(t)
	if _, err := OpenAAD(key, []byte{Version, 0x00, 0x01}, []byte("aad")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on truncated blob, got %v", err)
	}
}

func TestOpenRejectsPlaintextShapedBlob(t *testing.T) {
	key := freshKey(t)
	if _, err := OpenAAD(key, []byte("just plain text, no AEAD framing here"), []byte("aad")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on plaintext-shaped blob, got %v", err)
	}
}

func TestInvalidKeyLength(t *testing.T) {
	short := make([]byte, 16)
	if _, err := NewAEAD(short); !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("NewAEAD: expected ErrInvalidKeyLength, got %v", err)
	}
	if _, err := SealAAD(short, []byte("x"), nil); !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("SealAAD: expected ErrInvalidKeyLength, got %v", err)
	}
	if _, err := OpenAAD(short, make([]byte, 1+NonceLen+gcmTagLen), nil); !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("OpenAAD: expected ErrInvalidKeyLength, got %v", err)
	}
}

func TestNilKeyPassesThrough(t *testing.T) {
	pt := []byte("plain")
	out, err := SealAAD(nil, pt, []byte("aad"))
	if err != nil {
		t.Fatalf("SealAAD(nil): %v", err)
	}
	if !bytes.Equal(out, pt) {
		t.Errorf("nil key seal should pass through, got %q", out)
	}
	got, err := OpenAAD(nil, pt, []byte("aad"))
	if err != nil {
		t.Fatalf("OpenAAD(nil): %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Errorf("nil key open should pass through, got %q", got)
	}
}

func TestEmptyPlaintextProducesNil(t *testing.T) {
	key := freshKey(t)
	out, err := SealAAD(key, nil, []byte("aad"))
	if err != nil {
		t.Fatalf("SealAAD empty: %v", err)
	}
	if out != nil {
		t.Errorf("empty plaintext should seal to nil, got %v", out)
	}
	got, err := OpenAAD(key, nil, []byte("aad"))
	if err != nil {
		t.Fatalf("OpenAAD empty: %v", err)
	}
	if got != nil {
		t.Errorf("empty blob should open to nil, got %v", got)
	}
}

// TestEnvelopeByteStability locks down the on-disk envelope shape: a
// hand-rolled envelope (version || nonce || AES-256-GCM(plaintext, aad))
// computed via the standard library's primitives directly must round-trip
// through OpenAAD. If this fails, the bytes-packed format has drifted —
// session-22-written activity.log files and pre-refactor memory rows would
// no longer be readable.
func TestEnvelopeByteStability(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, KeyLen)
	nonce := bytes.Repeat([]byte{0x33}, NonceLen)
	aad := []byte("daimon-memory-row-v1\x00row-1\x00content")
	plaintext := []byte("hello daimon")

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, aad)

	envelope := make([]byte, 0, 1+NonceLen+len(ct))
	envelope = append(envelope, Version)
	envelope = append(envelope, nonce...)
	envelope = append(envelope, ct...)

	got, err := OpenAAD(key, envelope, aad)
	if err != nil {
		t.Fatalf("OpenAAD: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("byte-stability mismatch:\n got:  %q\n want: %q", got, plaintext)
	}
}
