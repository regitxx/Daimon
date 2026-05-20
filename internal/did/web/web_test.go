package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Parse: positive cases --------------------------------------------------

func TestParse_BareAuthority(t *testing.T) {
	id, err := Parse("did:web:example.com")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Authority != "example.com" {
		t.Errorf("Authority = %q, want example.com", id.Authority)
	}
	if len(id.Path) != 0 {
		t.Errorf("Path = %v, want empty", id.Path)
	}
}

func TestParse_AuthorityWithPort(t *testing.T) {
	// Percent-encoded colon = port. Per W3C spec section 3.4.
	id, err := Parse("did:web:example.com%3A8443")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Authority != "example.com:8443" {
		t.Errorf("Authority = %q, want example.com:8443", id.Authority)
	}
	if len(id.Path) != 0 {
		t.Errorf("Path = %v, want empty", id.Path)
	}
}

func TestParse_WithPath(t *testing.T) {
	id, err := Parse("did:web:example.com:user:alice")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Authority != "example.com" {
		t.Errorf("Authority = %q, want example.com", id.Authority)
	}
	if got, want := strings.Join(id.Path, "/"), "user/alice"; got != want {
		t.Errorf("Path = %v, want [user alice]", id.Path)
	}
}

func TestParse_StripsFragment(t *testing.T) {
	id, err := Parse("did:web:example.com#key-1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Authority != "example.com" {
		t.Errorf("Authority = %q, want example.com (fragment should be stripped)", id.Authority)
	}
}

func TestParse_StripsQuery(t *testing.T) {
	id, err := Parse("did:web:example.com?versionId=1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id.Authority != "example.com" {
		t.Errorf("Authority = %q, want example.com (query should be stripped)", id.Authority)
	}
}

// --- Parse: rejection cases -------------------------------------------------

func TestParse_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		did  string
	}{
		{"empty", ""},
		{"wrong_method", "did:key:z6Mk..."},
		{"empty_method_specific_id", "did:web:"},
		{"empty_authority_with_path", "did:web::user"},
		{"slash_in_id", "did:web:example.com/user/alice"},
		{"slash_in_path", "did:web:example.com:user/alice"},
		{"path_traversal_dotdot", "did:web:example.com:..:escape"},
		{"path_traversal_dot", "did:web:example.com:.:foo"},
		{"trailing_colon", "did:web:example.com:"},
		{"doubled_colon", "did:web:example.com::user"},
		{"slash_after_decode", "did:web:example.com:foo%2Fbar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.did); !errors.Is(err, ErrInvalidDID) {
				t.Errorf("Parse(%q): got %v, want ErrInvalidDID", c.did, err)
			}
		})
	}
}

// --- DocURL -----------------------------------------------------------------

func TestDocURL(t *testing.T) {
	cases := []struct {
		did  string
		want string
	}{
		{"did:web:example.com", "https://example.com/.well-known/did.json"},
		{"did:web:example.com%3A8443", "https://example.com:8443/.well-known/did.json"},
		{"did:web:example.com:user:alice", "https://example.com/user/alice/did.json"},
		{"did:web:example.com:.well-known:custom", "https://example.com/.well-known/custom/did.json"},
	}
	for _, c := range cases {
		t.Run(c.did, func(t *testing.T) {
			id, err := Parse(c.did)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := id.DocURL(); got != c.want {
				t.Errorf("DocURL() = %q, want %q", got, c.want)
			}
		})
	}
}

// --- String round-trip ------------------------------------------------------

func TestString_RoundTrip(t *testing.T) {
	// Parse → String → Parse should be byte-stable for in-practice
	// DIDs (no percent-encoding variants in the alphanumeric path).
	for _, s := range []string{
		"did:web:example.com",
		"did:web:example.com:user:alice",
		"did:web:example.com%3A8443",
		"did:web:example.com%3A8443:user:alice",
	} {
		t.Run(s, func(t *testing.T) {
			id, err := Parse(s)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := id.String(); got != s {
				t.Errorf("String() = %q, want %q", got, s)
			}
			// And re-parse the rendered string yields an equal Identifier.
			id2, err := Parse(id.String())
			if err != nil {
				t.Fatalf("Parse(round-trip): %v", err)
			}
			if id2.Authority != id.Authority {
				t.Errorf("Authority drift across roundtrip: %q → %q", id.Authority, id2.Authority)
			}
		})
	}
}

// --- parseDocument ----------------------------------------------------------

func TestParseDocument_HappyPath(t *testing.T) {
	body := []byte(`{
		"@context": "https://www.w3.org/ns/did/v1",
		"id": "did:web:example.com",
		"verificationMethod": [{
			"id": "did:web:example.com#key-1",
			"type": "Ed25519VerificationKey2020",
			"controller": "did:web:example.com",
			"publicKeyMultibase": "z6MkAB"
		}],
		"authentication": ["did:web:example.com#key-1"]
	}`)
	doc, err := parseDocument(body)
	if err != nil {
		t.Fatalf("parseDocument: %v", err)
	}
	if doc.ID != "did:web:example.com" {
		t.Errorf("ID = %q", doc.ID)
	}
	if len(doc.VerificationMethod) != 1 {
		t.Fatalf("VerificationMethod count = %d, want 1", len(doc.VerificationMethod))
	}
	if doc.VerificationMethod[0].Type != "Ed25519VerificationKey2020" {
		t.Errorf("VM type = %q", doc.VerificationMethod[0].Type)
	}
	if len(doc.Authentication) != 1 || doc.Authentication[0] != "did:web:example.com#key-1" {
		t.Errorf("Authentication = %v", doc.Authentication)
	}
}

func TestParseDocument_AcceptsContextArray(t *testing.T) {
	body := []byte(`{
		"@context": ["https://www.w3.org/ns/did/v1", "https://w3id.org/security/v2"],
		"id": "did:web:example.com"
	}`)
	doc, err := parseDocument(body)
	if err != nil {
		t.Fatalf("parseDocument: %v", err)
	}
	if len(doc.Context) != 2 {
		t.Errorf("Context = %v", doc.Context)
	}
}

func TestParseDocument_RejectsMissingContext(t *testing.T) {
	body := []byte(`{"id": "did:web:example.com"}`)
	if _, err := parseDocument(body); err == nil {
		t.Error("parseDocument: expected error for missing @context")
	}
}

func TestParseDocument_RejectsWrongContext(t *testing.T) {
	body := []byte(`{
		"@context": "https://example.com/some-other-context",
		"id": "did:web:example.com"
	}`)
	if _, err := parseDocument(body); err == nil {
		t.Error("parseDocument: expected error for missing did/v1 context")
	}
}

func TestParseDocument_RejectsMissingID(t *testing.T) {
	body := []byte(`{"@context": "https://www.w3.org/ns/did/v1"}`)
	if _, err := parseDocument(body); err == nil {
		t.Error("parseDocument: expected error for missing id")
	}
}

func TestParseDocument_RejectsMalformedJSON(t *testing.T) {
	if _, err := parseDocument([]byte("not json")); err == nil {
		t.Error("parseDocument: expected error for non-JSON input")
	}
}

// --- Resolve (httptest) -----------------------------------------------------

func TestResolve_HappyPath(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/did.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/did+json")
		// Note the document's id matches the DID we're going to ask for.
		// r.Host is the host:port the client connected to, which is the
		// httptest server's randomly-assigned address. We can't capture
		// srv.URL here because srv isn't assigned until httptest.NewTLSServer
		// returns — classic closure-bootstrap problem.
		_, _ = fmt.Fprintf(w, `{
			"@context": "https://www.w3.org/ns/did/v1",
			"id": "did:web:%s"
		}`, percentEncodeHost(r.Host))
	}))
	defer srv.Close()

	r := New()
	r.HTTPClient = srv.Client() // accepts httptest's self-signed TLS cert
	id := "did:web:" + percentEncodeHost(hostFromURL(t, srv.URL))

	doc, err := resolveAgainst(r, id, srv.URL+"/.well-known/did.json")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if doc.ID != id {
		t.Errorf("doc.ID = %q, want %q", doc.ID, id)
	}
}

func TestResolve_RejectsIDMismatch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/did+json")
		// id deliberately doesn't match what we ask for.
		_, _ = fmt.Fprint(w, `{
			"@context": "https://www.w3.org/ns/did/v1",
			"id": "did:web:not-the-host-you-asked-about.example"
		}`)
	}))
	defer srv.Close()

	r := New()
	r.HTTPClient = srv.Client()
	id := "did:web:" + percentEncodeHost(hostFromURL(t, srv.URL))
	if _, err := resolveAgainst(r, id, srv.URL+"/.well-known/did.json"); err == nil {
		t.Error("Resolve: expected error for ID mismatch, got nil")
	}
}

func TestResolve_404IsErrNotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	r := New()
	r.HTTPClient = srv.Client()
	id := "did:web:" + percentEncodeHost(hostFromURL(t, srv.URL))
	_, err := resolveAgainst(r, id, srv.URL+"/.well-known/did.json")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve on 404: got %v, want wrap of ErrNotFound", err)
	}
}

func TestResolve_500IsPlainError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := New()
	r.HTTPClient = srv.Client()
	id := "did:web:" + percentEncodeHost(hostFromURL(t, srv.URL))
	_, err := resolveAgainst(r, id, srv.URL+"/.well-known/did.json")
	if err == nil {
		t.Error("Resolve on 500: expected error, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("Resolve on 500: should NOT wrap ErrNotFound")
	}
}

func TestResolve_RejectsOversizedDocument(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/did+json")
		// 1 MiB of junk — way over the 256 KiB cap.
		w.Write([]byte(strings.Repeat("x", 1024*1024)))
	}))
	defer srv.Close()

	r := New()
	r.HTTPClient = srv.Client()
	r.MaxDocBytes = 1024 // very low cap for the test
	id := "did:web:" + percentEncodeHost(hostFromURL(t, srv.URL))
	if _, err := resolveAgainst(r, id, srv.URL+"/.well-known/did.json"); err == nil {
		t.Error("Resolve on oversized body: expected error")
	}
}

// --- helpers ----------------------------------------------------------------

// resolveAgainst is a test-only helper that bypasses the
// `Identifier.DocURL()`-based URL construction (which always uses
// https://<authority>/.well-known/did.json — fine in production
// but inflexible against an httptest server's randomly-assigned
// host:port). We perform the same self-id check Resolve does, but
// drive a known URL.
func resolveAgainst(r *Resolver, did, url string) (*Document, error) {
	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/did+ld+json, application/did+json, application/json")
	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		// pass
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, url)
	default:
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	maxBytes := r.MaxDocBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxDocBytes
	}
	body := make([]byte, maxBytes+1)
	n, _ := resp.Body.Read(body)
	body = body[:n]
	if int64(n) > maxBytes {
		return nil, fmt.Errorf("document exceeds %d-byte cap", maxBytes)
	}
	doc, err := parseDocument(body)
	if err != nil {
		return nil, err
	}
	if doc.ID != did {
		return nil, fmt.Errorf("document.id = %q, expected %q", doc.ID, did)
	}
	return doc, nil
}

func hostFromURL(t *testing.T, u string) string {
	t.Helper()
	// httptest server URLs are like https://127.0.0.1:54321. The
	// did:web authority needs the literal host:port.
	const prefix = "https://"
	if !strings.HasPrefix(u, prefix) {
		t.Fatalf("unexpected server URL %q", u)
	}
	return u[len(prefix):]
}

func percentEncodeHost(host string) string {
	// did:web encoding rule: a colon in the authority becomes %3A.
	return strings.ReplaceAll(host, ":", "%3A")
}
