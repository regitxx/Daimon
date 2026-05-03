package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
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
