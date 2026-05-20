// Package web implements the did:web DID method per the W3C
// Credentials Community Group spec at
// https://w3c-ccg.github.io/did-method-web/.
//
// did:web is the discovery primitive for v0.3 federation
// (design/v0.3-federation.md, Decision 2). A daimon is addressed
// by its DID; did:web resolves a DID to an HTTPS URL hosting a
// W3C DID Document, which carries the daimon's network endpoint
// + transport pubkey + supported protocols.
//
// This file: parsing. Resolution lives in resolve.go, the
// document shape in document.go.
package web

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrInvalidDID is returned when the input is not a syntactically
// valid did:web identifier. Resolution errors (network failure,
// 404, malformed JSON) are returned as wrapped errors from
// Resolve, not as ErrInvalidDID.
var ErrInvalidDID = errors.New("did:web: invalid identifier")

// Identifier is a parsed did:web. Constructed via Parse; the only
// behaviour is producing the canonical resolution URL via DocURL
// and the canonical string form via String.
//
// Per the did:web spec section 3.4, the method-specific-id has the
// shape `<authority>(:<path>)*`. The authority MAY contain a
// percent-encoded colon for a port (e.g. `example.com%3A8443`).
// Subsequent colons (after the authority) become path slashes in
// the resolution URL.
type Identifier struct {
	// Authority is the host (and optional port) from the
	// method-specific-id. Percent-encoded characters are decoded
	// here, so a DID like `did:web:example.com%3A8443` produces
	// Authority="example.com:8443".
	Authority string

	// Path is the slice of path components after the authority,
	// in order. Empty for a bare did:web:<authority> identifier.
	// Components are percent-decoded.
	Path []string
}

// Parse parses a did:web identifier string per the W3C spec. Returns
// ErrInvalidDID for malformed input.
//
// Examples (all valid):
//   did:web:example.com                 → Authority="example.com", Path=[]
//   did:web:example.com%3A8443          → Authority="example.com:8443", Path=[]
//   did:web:example.com:user:alice      → Authority="example.com", Path=["user", "alice"]
//   did:web:example.com:.well-known     → Authority="example.com", Path=[".well-known"]
//
// Rejected (return ErrInvalidDID):
//   ""                                  → empty input
//   "did:key:..."                       → wrong method
//   "did:web:"                          → empty method-specific-id
//   "did:web:/foo"                      → authority can't start with /
//   "did:web:example.com/user/alice"    → must use colons, not slashes
//   "did:web:example.com:user/alice"    → no slashes in path components
//   "did:web:example.com:..:escape"     → path-traversal segments rejected
//   "did:web:example.com:"              → trailing colon implies empty path component
func Parse(s string) (*Identifier, error) {
	const prefix = "did:web:"
	if !strings.HasPrefix(s, prefix) {
		return nil, fmt.Errorf("%w: missing %q prefix", ErrInvalidDID, prefix)
	}
	rest := s[len(prefix):]
	if rest == "" {
		return nil, fmt.Errorf("%w: empty method-specific-id", ErrInvalidDID)
	}

	// Strip any fragment (#...) — DIDs can carry a key identifier
	// fragment (e.g. did:web:example.com#key-1) which is meaningful
	// for verification-method references but not for document
	// resolution. The fragment is not part of the resolution URL.
	if i := strings.Index(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	// Same treatment for query string (?...) — strip per spec; it's
	// a DID URL feature, not a document-resolution input.
	if i := strings.Index(rest, "?"); i >= 0 {
		rest = rest[:i]
	}

	// Slashes are never legal in did:web; the spec uses colons as
	// the path-component separator. Catching this here gives a
	// clean error rather than a confusing URL-construction failure
	// later.
	if strings.ContainsAny(rest, "/") {
		return nil, fmt.Errorf("%w: did:web uses ':' as path separator, not '/'", ErrInvalidDID)
	}

	parts := strings.Split(rest, ":")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("%w: empty authority", ErrInvalidDID)
	}

	// Percent-decode each component. The W3C spec specifically calls
	// out percent-encoding as the way to embed a colon in the
	// authority (for a port). PathUnescape is the right choice over
	// QueryUnescape because the latter treats '+' as space, which
	// we don't want.
	auth, err := url.PathUnescape(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: percent-decoding authority: %v", ErrInvalidDID, err)
	}
	if auth == "" {
		return nil, fmt.Errorf("%w: empty authority after percent-decoding", ErrInvalidDID)
	}

	path := make([]string, 0, len(parts)-1)
	for i, p := range parts[1:] {
		if p == "" {
			return nil, fmt.Errorf("%w: empty path component at index %d (trailing or doubled colon)", ErrInvalidDID, i)
		}
		decoded, err := url.PathUnescape(p)
		if err != nil {
			return nil, fmt.Errorf("%w: percent-decoding path component %q: %v", ErrInvalidDID, p, err)
		}
		// Path-traversal defense: refuse "." and ".." components.
		// These would (depending on the resolver's URL-joining
		// semantics) escape the document URL into adjacent paths
		// or even other hosts. The W3C spec doesn't explicitly
		// forbid these — we're being defensive on top of it.
		if decoded == "." || decoded == ".." {
			return nil, fmt.Errorf("%w: path-traversal component %q not allowed", ErrInvalidDID, decoded)
		}
		// Same defense for slashes in a decoded component — the
		// raw form's slashes were caught above, but a
		// percent-encoded %2F could slip through. After decode
		// we reject those too.
		if strings.ContainsAny(decoded, "/") {
			return nil, fmt.Errorf("%w: decoded path component %q contains '/' (would corrupt URL construction)", ErrInvalidDID, decoded)
		}
		path = append(path, decoded)
	}

	return &Identifier{Authority: auth, Path: path}, nil
}

// String renders the canonical did:web form (lossless inverse of
// Parse for inputs that don't carry meaningless encoding variants
// like percent-encoded letters). The authority gets minimal
// percent-encoding (only the port-separator colon, if present);
// path components get the same treatment so a String→Parse
// round-trip is byte-stable for in-practice DIDs.
func (id *Identifier) String() string {
	var b strings.Builder
	b.WriteString("did:web:")
	// Encode a single colon in the authority as %3A (for port).
	// Other characters in real-world authorities are alphanumeric +
	// dot + hyphen, which don't need encoding.
	b.WriteString(strings.ReplaceAll(id.Authority, ":", "%3A"))
	for _, p := range id.Path {
		b.WriteByte(':')
		b.WriteString(url.PathEscape(p))
	}
	return b.String()
}

// DocURL returns the canonical HTTPS URL where this DID's document
// is published, per W3C did:web section 3.4. Always HTTPS — the
// spec explicitly forbids HTTP for resolution.
//
//   did:web:example.com              → https://example.com/.well-known/did.json
//   did:web:example.com:user:alice   → https://example.com/user/alice/did.json
func (id *Identifier) DocURL() string {
	// Authority is used in the URL host position. If it contained a
	// colon (port), it stays in raw form here — net/url's URL.Host
	// accepts host:port directly.
	if len(id.Path) == 0 {
		return "https://" + id.Authority + "/.well-known/did.json"
	}
	var b strings.Builder
	b.WriteString("https://")
	b.WriteString(id.Authority)
	for _, p := range id.Path {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(p))
	}
	b.WriteString("/did.json")
	return b.String()
}
