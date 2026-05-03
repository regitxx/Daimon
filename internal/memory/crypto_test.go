package memory

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func freshKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, rowKeyLen)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := freshKey(t)
	pt := []byte("huckgod prefers Go over Rust for daimon-core")

	ct, err := encryptField(key, pt, "01HXYZ", "content")
	if err != nil {
		t.Fatalf("encryptField: %v", err)
	}
	if bytes.Equal(ct, pt) {
		t.Fatal("ciphertext must not equal plaintext")
	}
	if ct[0] != rowCryptoVersion {
		t.Errorf("version byte = 0x%02x, want 0x%02x", ct[0], rowCryptoVersion)
	}
	if len(ct) != 1+rowNonceLen+len(pt)+16 {
		t.Errorf("ciphertext length = %d, want %d", len(ct), 1+rowNonceLen+len(pt)+16)
	}

	got, err := decryptField(key, ct, "01HXYZ", "content")
	if err != nil {
		t.Fatalf("decryptField: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Errorf("plaintext mismatch:\n got:  %q\n want: %q", got, pt)
	}
}

func TestEncryptionRandomizesCiphertext(t *testing.T) {
	key := freshKey(t)
	pt := []byte("identical input")
	a, err := encryptField(key, pt, "id", "content")
	if err != nil {
		t.Fatalf("encrypt a: %v", err)
	}
	b, err := encryptField(key, pt, "id", "content")
	if err != nil {
		t.Fatalf("encrypt b: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertexts (nonce reuse?)")
	}
}

func TestDecryptRejectsCrossRowSwap(t *testing.T) {
	key := freshKey(t)
	pt := []byte("secret content")
	rowA, err := encryptField(key, pt, "row-a", "content")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Try to read row-a's ciphertext as if it belonged to row-b.
	_, err = decryptField(key, rowA, "row-b", "content")
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on cross-row swap, got %v", err)
	}
}

func TestDecryptRejectsCrossFieldSwap(t *testing.T) {
	key := freshKey(t)
	pt := []byte("data")
	asContent, err := encryptField(key, pt, "id", "content")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Try to read content ciphertext as if it were the metadata column.
	_, err = decryptField(key, asContent, "id", "metadata")
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on cross-field swap, got %v", err)
	}
}

func TestDecryptRejectsForeignKey(t *testing.T) {
	pt := []byte("secret")
	keyA := freshKey(t)
	keyB := freshKey(t)
	ct, err := encryptField(keyA, pt, "id", "content")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	_, err = decryptField(keyB, ct, "id", "content")
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on foreign-key decrypt, got %v", err)
	}
}

func TestDecryptRejectsBitFlip(t *testing.T) {
	key := freshKey(t)
	ct, err := encryptField(key, []byte("hello"), "id", "content")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Flip a bit somewhere in the ciphertext body (after version+nonce).
	tampered := append([]byte(nil), ct...)
	tampered[len(tampered)-1] ^= 0x01
	_, err = decryptField(key, tampered, "id", "content")
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on bit flip, got %v", err)
	}
}

func TestDecryptRejectsUnknownVersion(t *testing.T) {
	key := freshKey(t)
	ct, err := encryptField(key, []byte("hello"), "id", "content")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	tampered := append([]byte(nil), ct...)
	tampered[0] = 0xFF
	_, err = decryptField(key, tampered, "id", "content")
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on unknown version, got %v", err)
	}
}

func TestDecryptRejectsTruncatedBlob(t *testing.T) {
	key := freshKey(t)
	_, err := decryptField(key, []byte{rowCryptoVersion, 0x00, 0x01}, "id", "content")
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on truncated blob, got %v", err)
	}
}

func TestDecryptRejectsPlaintextBlobUnderEncryptedKey(t *testing.T) {
	// A blob that looks like plaintext (no version+nonce framing) must not
	// be silently returned when a non-nil key is supplied.
	key := freshKey(t)
	_, err := decryptField(key, []byte("just plain text, no AEAD framing here"), "id", "content")
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext for plaintext-shaped blob, got %v", err)
	}
}

func TestEncryptInvalidKeyLength(t *testing.T) {
	short := make([]byte, 16)
	if _, err := encryptField(short, []byte("x"), "id", "content"); !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("expected ErrInvalidKeyLength, got %v", err)
	}
	if _, err := decryptField(short, []byte("x"), "id", "content"); !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("expected ErrInvalidKeyLength on decrypt, got %v", err)
	}
}

func TestEncryptionDisabledWhenKeyNil(t *testing.T) {
	pt := []byte("plain")
	out, err := encryptField(nil, pt, "id", "content")
	if err != nil {
		t.Fatalf("encryptField(nil): %v", err)
	}
	if !bytes.Equal(out, pt) {
		t.Errorf("nil key should pass through, got %q", out)
	}
	got, err := decryptField(nil, pt, "id", "content")
	if err != nil {
		t.Fatalf("decryptField(nil): %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Errorf("nil key should pass through on decrypt, got %q", got)
	}
}

func TestEmptyPlaintextProducesNil(t *testing.T) {
	key := freshKey(t)
	out, err := encryptField(key, nil, "id", "metadata")
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}
	if out != nil {
		t.Errorf("empty plaintext should encrypt to nil, got %v", out)
	}
	got, err := decryptField(key, nil, "id", "metadata")
	if err != nil {
		t.Fatalf("decrypt empty: %v", err)
	}
	if got != nil {
		t.Errorf("empty blob should decrypt to nil, got %v", got)
	}
}
