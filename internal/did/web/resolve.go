package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNotFound is returned when the DID document URL returns 404.
// Other HTTP errors (5xx, 403, etc.) are wrapped as plain errors.
var ErrNotFound = errors.New("did:web: document not found")

// DefaultMaxDocBytes caps the document size at 256 KiB. A
// well-formed Daimon DID document is well under 4 KiB; the cap
// exists as defense-in-depth against a server that streams an
// unbounded body (memory exhaustion DoS).
const DefaultMaxDocBytes = 256 * 1024

// DefaultTimeout bounds resolution at 5 seconds. Real-world did:web
// servers are static-file HTTPS endpoints; if one takes longer than
// 5 seconds it's broken.
const DefaultTimeout = 5 * time.Second

// Resolver fetches DID documents via HTTPS. The zero value is
// usable; New() with a custom HTTP client is the override path
// (e.g. for tests against httptest servers, or for callers that
// want connection pooling tuned).
type Resolver struct {
	HTTPClient  *http.Client
	MaxDocBytes int64
	Timeout     time.Duration
}

// New constructs a Resolver with sensible defaults: a fresh
// http.Client with the standard transport, the default 256 KiB
// document cap, and the default 5-second timeout.
func New() *Resolver {
	return &Resolver{
		HTTPClient:  &http.Client{Timeout: DefaultTimeout},
		MaxDocBytes: DefaultMaxDocBytes,
		Timeout:     DefaultTimeout,
	}
}

// Resolve fetches the DID Document for the given identifier. The
// returned document's ID is verified to match the requested DID —
// a mismatch is a strong signal that the document was served by
// the wrong host (or someone tampered with the static file) and
// returns an error.
//
// Returns ErrNotFound on HTTP 404; other HTTP errors are wrapped
// as plain errors. Network timeouts are returned via the
// underlying http.Client error.
func (r *Resolver) Resolve(ctx context.Context, did string) (*Document, error) {
	id, err := Parse(did)
	if err != nil {
		return nil, err
	}

	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	maxBytes := r.MaxDocBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxDocBytes
	}

	url := id.DocURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("did:web resolve: build request for %s: %w", url, err)
	}
	// Accept the canonical DID Document media types per W3C DID
	// Core 7.1.2. Some servers serve as text/html or */*; we
	// don't enforce the response Content-Type — the JSON body is
	// what matters, and we let json.Decoder reject anything that
	// isn't parseable.
	req.Header.Set("Accept", "application/did+ld+json, application/did+json, application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("did:web resolve %s: %w", url, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to parse
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, url)
	default:
		return nil, fmt.Errorf("did:web resolve %s: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("did:web resolve %s: read body: %w", url, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("did:web resolve %s: document exceeds %d-byte cap", url, maxBytes)
	}

	doc, err := parseDocument(body)
	if err != nil {
		return nil, fmt.Errorf("did:web resolve %s: %w", url, err)
	}

	// Self-identity check: the document's id MUST match the DID we
	// were asked to resolve. Otherwise a wildcard catch-all on the
	// server could return one daimon's document for any DID query,
	// silently impersonating others.
	if doc.ID != did {
		return nil, fmt.Errorf("did:web resolve: document.id = %q, expected %q", doc.ID, did)
	}

	return doc, nil
}

// parseDocument unmarshals + minimally validates a DID Document
// JSON body. Separated from Resolve so unit tests can drive it
// with canonical fixtures without spinning up an HTTP server.
func parseDocument(body []byte) (*Document, error) {
	// Custom-decode the @context field because it's polymorphic
	// (string OR array of strings OR array of strings+objects per
	// W3C JSON-LD).
	var raw struct {
		Context            json.RawMessage      `json:"@context"`
		ID                 string               `json:"id"`
		VerificationMethod []VerificationMethod `json:"verificationMethod"`
		Authentication    []json.RawMessage    `json:"authentication"`
		Service            []Service            `json:"service"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse document JSON: %w", err)
	}
	if raw.ID == "" {
		return nil, errors.New("parse document: missing required \"id\" field")
	}

	var ctx rawContext
	if len(raw.Context) > 0 {
		if err := ctx.UnmarshalJSON(raw.Context); err != nil {
			return nil, fmt.Errorf("parse @context: %w", err)
		}
	}
	// W3C DID Core 5.2: @context MUST include "https://www.w3.org/ns/did/v1".
	// Many real-world documents put it first. We accept it anywhere
	// in the array.
	hasV1 := false
	for _, c := range ctx.value {
		if c == DIDv1Context {
			hasV1 = true
			break
		}
	}
	if !hasV1 {
		return nil, fmt.Errorf("parse document: @context missing required %q", DIDv1Context)
	}

	// Authentication is polymorphic too — per W3C it can be either
	// a DID URL string OR an embedded verification method object.
	// We only care about the string-reference form for v0.3; embedded
	// methods don't add capability at this level.
	auth := make([]string, 0, len(raw.Authentication))
	for _, a := range raw.Authentication {
		var s string
		if err := json.Unmarshal(a, &s); err == nil && s != "" {
			auth = append(auth, s)
		}
		// Silently skip embedded-object form — not relevant at v0.3.
	}

	return &Document{
		Context:            ctx.value,
		ID:                 raw.ID,
		VerificationMethod: raw.VerificationMethod,
		Authentication:    auth,
		Service:            raw.Service,
	}, nil
}
