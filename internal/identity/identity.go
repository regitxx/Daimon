package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

var ErrIdentityLocked = errors.New("identity is locked")

// Identity represents a principal's cryptographic identity.
// In v0.1 it holds an Ed25519 keypair and is addressed by did:key.
type Identity struct {
	priv ed25519.PrivateKey
}

// Generate creates a new Identity with a freshly generated Ed25519 keypair.
func Generate() (*Identity, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}
	return &Identity{priv: priv}, nil
}

// LoadFromKeystore reads and decrypts an identity from disk.
func LoadFromKeystore(path string, password []byte) (*Identity, error) {
	priv, err := loadKeystore(path, password)
	if err != nil {
		return nil, err
	}
	return &Identity{priv: priv}, nil
}

// SaveToKeystore encrypts the identity with a passkey/password-derived key
// and writes it to path with mode 0600.
func (i *Identity) SaveToKeystore(path string, password []byte) error {
	if i.priv == nil {
		return ErrIdentityLocked
	}
	return saveKeystore(path, i.priv, password)
}

// DID returns the did:key DID for this identity.
func (i *Identity) DID() string {
	return EncodeDIDKey(i.PublicKey())
}

// PublicKey returns the Ed25519 public key.
func (i *Identity) PublicKey() ed25519.PublicKey {
	return i.priv.Public().(ed25519.PublicKey)
}

// Sign produces an Ed25519 signature over msg.
func (i *Identity) Sign(msg []byte) ([]byte, error) {
	if i.priv == nil {
		return nil, ErrIdentityLocked
	}
	return ed25519.Sign(i.priv, msg), nil
}

// Verify reports whether sig is a valid signature over msg by this identity.
func (i *Identity) Verify(msg, sig []byte) bool {
	return ed25519.Verify(i.PublicKey(), msg, sig)
}

// DeriveSubkey derives a domain-separated symmetric key from the principal's
// Ed25519 seed via HKDF-SHA256.
//
// Use a distinct, stable info label per consumer (e.g. the memory store's
// row-encryption key uses "daimon-memory-encryption-v1"). The derivation is
// deterministic — the same identity always produces the same subkey for the
// same label — so an unlocked daimon can re-open data it wrote previously
// without storing the subkey to disk.
//
// The seed never leaves this function; only the derived bytes are returned.
// HKDF-SHA256 over a 32-byte uniformly-random Ed25519 seed produces output
// statistically independent of the signing operations performed with the
// underlying key.
func (i *Identity) DeriveSubkey(label string, length int) ([]byte, error) {
	if i.priv == nil {
		return nil, ErrIdentityLocked
	}
	if label == "" {
		return nil, errors.New("identity: empty subkey label")
	}
	if length <= 0 {
		return nil, errors.New("identity: subkey length must be positive")
	}
	seed := i.priv.Seed()
	r := hkdf.New(sha256.New, seed, nil, []byte(label))
	out := make([]byte, length)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("derive subkey: %w", err)
	}
	return out, nil
}

// DIDDocument returns a minimal W3C DID document for this identity using
// the Ed25519VerificationKey2020 suite.
func (i *Identity) DIDDocument() ([]byte, error) {
	did := i.DID()
	frag := MultibaseFragment(did)
	keyID := did + "#" + frag

	doc := map[string]any{
		"@context": []string{
			"https://www.w3.org/ns/did/v1",
			"https://w3id.org/security/suites/ed25519-2020/v1",
		},
		"id": did,
		"verificationMethod": []map[string]any{
			{
				"id":                 keyID,
				"type":               "Ed25519VerificationKey2020",
				"controller":         did,
				"publicKeyMultibase": frag,
			},
		},
		"authentication":  []string{keyID},
		"assertionMethod": []string{keyID},
	}
	return json.MarshalIndent(doc, "", "  ")
}
