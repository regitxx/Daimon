package web

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// --- signaturePayload determinism ------------------------------------------

func TestSignaturePayload_Deterministic(t *testing.T) {
	// Same endpoint → same hash, always. The protocols-sort
	// behaviour makes this even more important: a caller that
	// passes protocols in a different order MUST get the same
	// signature payload.
	e1 := DaimonEndpoint{
		URL:             "quic://daimon.example.com:4242",
		TransportPubKey: "z6MkABC",
		Protocols:       []string{"peer.echo", "peer.ask"},
		PaymentAddress:  "evm:base:0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
	}
	e2 := DaimonEndpoint{
		URL:             "quic://daimon.example.com:4242",
		TransportPubKey: "z6MkABC",
		Protocols:       []string{"peer.ask", "peer.echo"}, // reversed
		PaymentAddress:  "evm:base:0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
	}
	h1 := signaturePayload(e1)
	h2 := signaturePayload(e2)
	if string(h1) != string(h2) {
		t.Errorf("signaturePayload not order-invariant under protocols")
	}
}

func TestSignaturePayload_DistinguishesFields(t *testing.T) {
	// Changing any individual field changes the payload. Anti-
	// regression for a "we accidentally dropped a field from the
	// signed bytes" bug.
	base := DaimonEndpoint{
		URL:             "quic://a:1",
		TransportPubKey: "z6MkA",
		Protocols:       []string{"peer.echo"},
		PaymentAddress:  "evm:base:0x01",
	}
	mutations := []struct {
		name string
		mut  func(e *DaimonEndpoint)
	}{
		{"URL", func(e *DaimonEndpoint) { e.URL = "quic://b:1" }},
		{"TransportPubKey", func(e *DaimonEndpoint) { e.TransportPubKey = "z6MkB" }},
		{"Protocols+1", func(e *DaimonEndpoint) { e.Protocols = append(e.Protocols, "peer.ask") }},
		{"Protocols-1", func(e *DaimonEndpoint) { e.Protocols = nil }},
		{"PaymentAddress", func(e *DaimonEndpoint) { e.PaymentAddress = "evm:base:0x02" }},
	}
	baseHash := string(signaturePayload(base))
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			mutated := base
			mutated.Protocols = append([]string(nil), base.Protocols...) // independent slice
			m.mut(&mutated)
			if string(signaturePayload(mutated)) == baseHash {
				t.Errorf("signaturePayload didn't change for %s mutation — field is being dropped from the signed bytes", m.name)
			}
		})
	}
}

// --- Sign + Verify round-trip ----------------------------------------------

func TestSignEndpoint_RoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	e := DaimonEndpoint{
		URL:             "quic://daimon.example.com:4242",
		TransportPubKey: "z6MkABC",
		Protocols:       []string{"peer.echo", "peer.ask"},
	}
	signed, err := SignEndpoint(priv, e)
	if err != nil {
		t.Fatalf("SignEndpoint: %v", err)
	}
	if signed.Signature == "" {
		t.Error("signed.Signature is empty")
	}
	if err := VerifyEndpoint(signed, pub); err != nil {
		t.Errorf("VerifyEndpoint with correct pubkey: %v", err)
	}
}

func TestSignEndpoint_RejectsBadInputs(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := SignEndpoint(priv[:10], DaimonEndpoint{URL: "x", TransportPubKey: "y"}); err == nil {
		t.Error("expected error for too-short private key")
	}
	if _, err := SignEndpoint(priv, DaimonEndpoint{TransportPubKey: "y"}); err == nil {
		t.Error("expected error for missing URL")
	}
	if _, err := SignEndpoint(priv, DaimonEndpoint{URL: "x"}); err == nil {
		t.Error("expected error for missing TransportPubKey")
	}
}

func TestVerifyEndpoint_RejectsWrongPubKey(t *testing.T) {
	_, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader) // different key

	signed, err := SignEndpoint(priv1, DaimonEndpoint{URL: "x", TransportPubKey: "y"})
	if err != nil {
		t.Fatalf("SignEndpoint: %v", err)
	}
	if err := VerifyEndpoint(signed, pub2); err == nil {
		t.Error("expected VerifyEndpoint to fail with wrong pubkey")
	}
}

func TestVerifyEndpoint_RejectsTamperedFields(t *testing.T) {
	// Any modification to the signed endpoint MUST invalidate
	// the signature. Anti-regression for "the signature only
	// covers some fields" bugs.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	e := DaimonEndpoint{
		URL:             "quic://a:1",
		TransportPubKey: "z6MkA",
		Protocols:       []string{"peer.echo"},
		PaymentAddress:  "evm:base:0x01",
	}
	signed, err := SignEndpoint(priv, e)
	if err != nil {
		t.Fatalf("SignEndpoint: %v", err)
	}
	// Sanity: original verifies.
	if err := VerifyEndpoint(signed, pub); err != nil {
		t.Fatalf("baseline VerifyEndpoint: %v", err)
	}

	mutations := []struct {
		name string
		mut  func(s *SignedEndpoint)
	}{
		{"URL", func(s *SignedEndpoint) { s.Endpoint.URL = "quic://EVIL:1" }},
		{"TransportPubKey", func(s *SignedEndpoint) { s.Endpoint.TransportPubKey = "z6MkEVIL" }},
		{"Protocols_add", func(s *SignedEndpoint) {
			s.Endpoint.Protocols = append(s.Endpoint.Protocols, "peer.evil")
		}},
		{"Protocols_remove", func(s *SignedEndpoint) { s.Endpoint.Protocols = nil }},
		{"PaymentAddress", func(s *SignedEndpoint) { s.Endpoint.PaymentAddress = "evm:base:0xEVIL" }},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			tampered := *signed
			tampered.Endpoint = signed.Endpoint
			tampered.Endpoint.Protocols = append([]string(nil), signed.Endpoint.Protocols...)
			m.mut(&tampered)
			if err := VerifyEndpoint(&tampered, pub); err == nil {
				t.Errorf("VerifyEndpoint accepted tampered %s field — signature is incomplete", m.name)
			}
		})
	}
}

func TestVerifyEndpoint_RejectsBadSignatureEncoding(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	signed := &SignedEndpoint{
		Endpoint:  DaimonEndpoint{URL: "x", TransportPubKey: "y"},
		Signature: "not-valid-base64-!!!",
	}
	if err := VerifyEndpoint(signed, pub); err == nil {
		t.Error("expected VerifyEndpoint to fail on malformed signature base64")
	}
}

func TestVerifyEndpoint_RejectsNilSignedOrWrongPubSize(t *testing.T) {
	if err := VerifyEndpoint(nil, make([]byte, ed25519.PublicKeySize)); err == nil {
		t.Error("expected error for nil signed endpoint")
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signed, _ := SignEndpoint(priv, DaimonEndpoint{URL: "x", TransportPubKey: "y"})
	if err := VerifyEndpoint(signed, make([]byte, 10)); err == nil {
		t.Error("expected error for wrong pubkey size")
	}
}

// --- ExtractSignedEndpoint --------------------------------------------------

func TestExtractSignedEndpoint_HappyPath(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	signed, _ := SignEndpoint(priv, DaimonEndpoint{
		URL:             "quic://example.com:4242",
		TransportPubKey: "z6MkA",
		Protocols:       []string{"peer.echo"},
	})
	body, _ := json.Marshal(signed)

	doc := &Document{
		Service: []Service{
			{ID: "did:web:example.com#daimon", Type: DaimonServiceType, ServiceEndpoint: body},
		},
	}
	got, err := ExtractSignedEndpoint(doc)
	if err != nil {
		t.Fatalf("ExtractSignedEndpoint: %v", err)
	}
	if got.Endpoint.URL != "quic://example.com:4242" {
		t.Errorf("URL roundtrip = %q", got.Endpoint.URL)
	}
	if err := VerifyEndpoint(got, pub); err != nil {
		t.Errorf("VerifyEndpoint on roundtripped endpoint: %v", err)
	}
}

func TestExtractSignedEndpoint_NoDaimonService(t *testing.T) {
	doc := &Document{
		Service: []Service{
			{ID: "did:web:example.com#some-other", Type: "SomeOtherType", ServiceEndpoint: json.RawMessage(`"https://elsewhere"`)},
		},
	}
	_, err := ExtractSignedEndpoint(doc)
	if !errors.Is(err, ErrNoDaimonEndpoint) {
		t.Errorf("got %v, want ErrNoDaimonEndpoint", err)
	}
}

func TestExtractSignedEndpoint_NilDoc(t *testing.T) {
	if _, err := ExtractSignedEndpoint(nil); err == nil {
		t.Error("expected error for nil document")
	}
}

func TestExtractSignedEndpoint_MalformedService(t *testing.T) {
	doc := &Document{
		Service: []Service{
			{ID: "x", Type: DaimonServiceType, ServiceEndpoint: json.RawMessage(`{"endpoint": broken json`)},
		},
	}
	if _, err := ExtractSignedEndpoint(doc); err == nil || errors.Is(err, ErrNoDaimonEndpoint) {
		t.Errorf("expected JSON-parse error (not ErrNoDaimonEndpoint), got %v", err)
	}
}

// --- ID-mismatch defense interaction ----------------------------------------

func TestSignedEndpoint_FullVerifyFlow(t *testing.T) {
	// Sanity test that mirrors the production flow:
	//   resolve DID → parse document → extract signed endpoint →
	//   verify signature against the document's verificationMethod
	//   pubkey.
	//
	// We don't import internal/identity here — the multibase
	// encoding of an Ed25519 pubkey is not part of this slice (it's
	// in identity/keymultibase.go in v0.1). For the test, we just
	// roundtrip raw pubkey bytes through SignEndpoint and verify.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	endpoint := DaimonEndpoint{
		URL:             "quic://daimon.example.com:4242",
		TransportPubKey: "z6MkA",
		Protocols:       []string{"peer.echo", "peer.pay"},
		PaymentAddress:  "evm:base:0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
	}
	signed, err := SignEndpoint(priv, endpoint)
	if err != nil {
		t.Fatalf("SignEndpoint: %v", err)
	}
	body, _ := json.Marshal(signed)
	doc := &Document{
		Context: []string{DIDv1Context},
		ID:      "did:web:daimon.example.com",
		VerificationMethod: []VerificationMethod{
			{
				ID:                 "did:web:daimon.example.com#key-1",
				Type:               "Ed25519VerificationKey2020",
				Controller:         "did:web:daimon.example.com",
				PublicKeyMultibase: "z6MkA",
			},
		},
		Service: []Service{
			{ID: "did:web:daimon.example.com#daimon", Type: DaimonServiceType, ServiceEndpoint: body},
		},
	}

	extracted, err := ExtractSignedEndpoint(doc)
	if err != nil {
		t.Fatalf("ExtractSignedEndpoint: %v", err)
	}
	if err := VerifyEndpoint(extracted, pub); err != nil {
		t.Errorf("full flow VerifyEndpoint: %v", err)
	}
	if !strings.HasPrefix(extracted.Endpoint.URL, "quic://") {
		t.Errorf("URL roundtrip dropped: %q", extracted.Endpoint.URL)
	}
}
