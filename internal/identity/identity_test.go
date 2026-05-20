package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := id.DID(); !strings.HasPrefix(got, "did:key:z") {
		t.Errorf("DID = %q, want prefix did:key:z", got)
	}
	if got, want := len(id.PublicKey()), ed25519.PublicKeySize; got != want {
		t.Errorf("public key size = %d, want %d", got, want)
	}
}

func TestSignVerify(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	msg := []byte("the quick brown daimon jumps over the lazy human")
	sig, err := id.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !id.Verify(msg, sig) {
		t.Error("identity did not verify its own signature")
	}
	if !ed25519.Verify(id.PublicKey(), msg, sig) {
		t.Error("ed25519.Verify rejected a valid signature")
	}
	tampered := append([]byte{}, msg...)
	tampered[0] ^= 0x01
	if id.Verify(tampered, sig) {
		t.Error("verification accepted a tampered message")
	}
}

func TestDIDKeyRoundTrip(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	did := id.DID()
	pub, err := DecodeDIDKey(did)
	if err != nil {
		t.Fatalf("DecodeDIDKey(%q): %v", did, err)
	}
	if !bytes.Equal(pub, id.PublicKey()) {
		t.Error("decoded public key does not match")
	}
}

func TestDIDKeyRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"not-a-did",
		"did:key:",
		"did:key:abc",
		"did:web:example.com",
	}
	for _, c := range cases {
		if _, err := DecodeDIDKey(c); err == nil {
			t.Errorf("DecodeDIDKey(%q) succeeded, want error", c)
		}
	}
}

func TestKeystoreRoundTrip(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	password := []byte("correct horse battery staple")

	if err := id.SaveToKeystore(path, password); err != nil {
		t.Fatalf("SaveToKeystore: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("keystore permissions = %o, want 0600", perm)
	}

	loaded, err := LoadFromKeystore(path, password)
	if err != nil {
		t.Fatalf("LoadFromKeystore: %v", err)
	}
	if loaded.DID() != id.DID() {
		t.Errorf("loaded DID = %q, want %q", loaded.DID(), id.DID())
	}

	msg := []byte("test")
	sigOriginal, _ := id.Sign(msg)
	sigLoaded, _ := loaded.Sign(msg)
	if !bytes.Equal(sigOriginal, sigLoaded) {
		t.Error("signatures differ between original and loaded identity")
	}
}

func TestKeystoreWrongPassword(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	if err := id.SaveToKeystore(path, []byte("right")); err != nil {
		t.Fatalf("SaveToKeystore: %v", err)
	}
	if _, err := LoadFromKeystore(path, []byte("wrong")); err != ErrWrongPassword {
		t.Errorf("LoadFromKeystore with wrong password: got %v, want ErrWrongPassword", err)
	}
}

func TestRotatePassword_RoundTrip(t *testing.T) {
	// Rotate the password, then assert new password decrypts to the
	// same private key the original was holding, AND the old password
	// no longer decrypts.
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	if err := id.SaveToKeystore(path, []byte("old")); err != nil {
		t.Fatalf("SaveToKeystore: %v", err)
	}
	originalDID := id.DID()

	if err := RotatePassword(path, []byte("old"), []byte("new")); err != nil {
		t.Fatalf("RotatePassword: %v", err)
	}

	// New password works + yields the SAME private key (same DID).
	loaded, err := LoadFromKeystore(path, []byte("new"))
	if err != nil {
		t.Fatalf("LoadFromKeystore(new): %v", err)
	}
	if loaded.DID() != originalDID {
		t.Fatalf("rotate changed DID: %s → %s", originalDID, loaded.DID())
	}

	// Old password no longer works.
	if _, err := LoadFromKeystore(path, []byte("old")); err != ErrWrongPassword {
		t.Errorf("post-rotate load with old password: got %v, want ErrWrongPassword", err)
	}
}

func TestRotatePassword_RejectsWrongOldPassword(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	if err := id.SaveToKeystore(path, []byte("correct")); err != nil {
		t.Fatalf("SaveToKeystore: %v", err)
	}

	if err := RotatePassword(path, []byte("WRONG"), []byte("new")); err != ErrWrongPassword {
		t.Errorf("RotatePassword with wrong old password: got %v, want ErrWrongPassword", err)
	}

	// Original keystore must still decrypt under the correct password —
	// a failed rotate is non-destructive.
	if _, err := LoadFromKeystore(path, []byte("correct")); err != nil {
		t.Errorf("post-failed-rotate load with correct password: got %v, want success", err)
	}
}

func TestRotatePassword_RejectsEmptyNew(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	if err := id.SaveToKeystore(path, []byte("pw")); err != nil {
		t.Fatalf("SaveToKeystore: %v", err)
	}
	if err := RotatePassword(path, []byte("pw"), []byte("")); err == nil {
		t.Error("RotatePassword with empty new password: expected error, got nil")
	}
	// Original still works.
	if _, err := LoadFromKeystore(path, []byte("pw")); err != nil {
		t.Errorf("post-empty-new-rotate load: got %v, want success", err)
	}
}

func TestRotatePassword_TempFileCleanedUpOnFailure(t *testing.T) {
	// On a successful rotate, the .rotate-tmp sibling must NOT exist
	// after the rename. Captures the "we didn't leak a temp file in
	// the happy path" property.
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	if err := id.SaveToKeystore(path, []byte("a")); err != nil {
		t.Fatalf("SaveToKeystore: %v", err)
	}
	if err := RotatePassword(path, []byte("a"), []byte("b")); err != nil {
		t.Fatalf("RotatePassword: %v", err)
	}
	tmp := path + ".rotate-tmp"
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rotate-tmp file leaked on success: stat err = %v", err)
	}
}

func TestKeystoreCorrupted(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	if err := id.SaveToKeystore(path, []byte("password")); err != nil {
		t.Fatalf("SaveToKeystore: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if i := bytes.Index(data, []byte(`"ciphertext": "`)); i >= 0 {
		data[i+len(`"ciphertext": "`)] ^= 0x01
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadFromKeystore(path, []byte("password")); err == nil {
		t.Error("LoadFromKeystore on corrupted keystore: got nil, want error")
	}
}

func TestDIDDocument(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	doc, err := id.DIDDocument()
	if err != nil {
		t.Fatalf("DIDDocument: %v", err)
	}
	if !bytes.Contains(doc, []byte(id.DID())) {
		t.Error("DID document does not contain DID")
	}

	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("invalid JSON in DID document: %v", err)
	}
	if got := parsed["id"]; got != id.DID() {
		t.Errorf("doc.id = %v, want %s", got, id.DID())
	}
	vm, ok := parsed["verificationMethod"].([]any)
	if !ok || len(vm) != 1 {
		t.Fatalf("verificationMethod has unexpected shape: %v", parsed["verificationMethod"])
	}
	method := vm[0].(map[string]any)
	if method["type"] != "Ed25519VerificationKey2020" {
		t.Errorf("verificationMethod.type = %v, want Ed25519VerificationKey2020", method["type"])
	}
	if method["controller"] != id.DID() {
		t.Errorf("verificationMethod.controller = %v, want %s", method["controller"], id.DID())
	}
}

func TestDeriveSubkey(t *testing.T) {
	id1, err := Generate()
	if err != nil {
		t.Fatalf("Generate id1: %v", err)
	}
	id2, err := Generate()
	if err != nil {
		t.Fatalf("Generate id2: %v", err)
	}

	// Determinism: same identity + same label + same length → same key.
	a1, err := id1.DeriveSubkey("daimon-memory-encryption-v1", 32)
	if err != nil {
		t.Fatalf("DeriveSubkey a1: %v", err)
	}
	a2, err := id1.DeriveSubkey("daimon-memory-encryption-v1", 32)
	if err != nil {
		t.Fatalf("DeriveSubkey a2: %v", err)
	}
	if !bytes.Equal(a1, a2) {
		t.Error("DeriveSubkey is not deterministic for the same identity")
	}
	if len(a1) != 32 {
		t.Errorf("DeriveSubkey length = %d, want 32", len(a1))
	}

	// Different identity → different key.
	b, err := id2.DeriveSubkey("daimon-memory-encryption-v1", 32)
	if err != nil {
		t.Fatalf("DeriveSubkey b: %v", err)
	}
	if bytes.Equal(a1, b) {
		t.Error("DeriveSubkey produced identical keys for distinct identities")
	}

	// Different label → different key (domain separation).
	c, err := id1.DeriveSubkey("daimon-something-else-v1", 32)
	if err != nil {
		t.Fatalf("DeriveSubkey c: %v", err)
	}
	if bytes.Equal(a1, c) {
		t.Error("DeriveSubkey returned identical keys for distinct labels")
	}

	// Different length → different bytes (HKDF stream is the same prefix).
	short, err := id1.DeriveSubkey("daimon-memory-encryption-v1", 16)
	if err != nil {
		t.Fatalf("DeriveSubkey short: %v", err)
	}
	if !bytes.Equal(short, a1[:16]) {
		t.Error("HKDF stream prefix mismatch — derivation is not stable across lengths")
	}

	// Argument validation.
	if _, err := id1.DeriveSubkey("", 32); err == nil {
		t.Error("expected error for empty label")
	}
	if _, err := id1.DeriveSubkey("ok", 0); err == nil {
		t.Error("expected error for zero length")
	}
	if _, err := id1.DeriveSubkey("ok", -1); err == nil {
		t.Error("expected error for negative length")
	}
}
