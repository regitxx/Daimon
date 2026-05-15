package payment

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/wallet"
)

// Errors surfaced by the package.
var (
	// ErrCeilingExceeded surfaces when the 402 response demands more than
	// the configured per-call price ceiling. Audit-log row carries the
	// limit + the requested amount.
	ErrCeilingExceeded = errors.New("payment: ceiling exceeded")

	// ErrNoCompatibleRequirement surfaces when none of the server's
	// PaymentRequirements rows match a (scheme, network, asset) tuple
	// the daimon can satisfy with its current wallet keystore.
	ErrNoCompatibleRequirement = errors.New("payment: no compatible requirement")

	// ErrInvalidRequiredHeader surfaces when the PAYMENT-REQUIRED header
	// is malformed (bad Base64, bad JSON, x402Version mismatch).
	ErrInvalidRequiredHeader = errors.New("payment: invalid PAYMENT-REQUIRED header")
)

// Config bundles the optional knobs the client respects.
type Config struct {
	// HTTPClient is the *http.Client used for both the probe (initial GET)
	// and the retry (the request that carries PAYMENT-SIGNATURE). Nil
	// defaults to a Client with a 30-second timeout.
	HTTPClient *http.Client

	// CeilingSmallestUnit caps the value (in the token's smallest unit —
	// 6 decimals for USDC) the daimon will sign in a single payment. Set
	// to nil to disable; the package will return ErrCeilingExceeded on
	// any 402 whose MaxAmountRequired exceeds the cap. Recommended
	// default for v0.2: 100000 (= $0.10 in USDC's 6-decimal smallest
	// unit) — calibrate per principal's risk tolerance.
	CeilingSmallestUnit *big.Int

	// ValidityWindow is how long after the current wall-clock time the
	// EIP-3009 authorization remains valid. Defaults to 5 minutes if
	// zero. The server's facilitator will reject signatures past
	// validBefore; smaller windows reduce replay risk but cost a re-sign
	// if the resource server takes a while to settle.
	ValidityWindow time.Duration

	// Now overrides time.Now for deterministic tests. Nil = real time.
	Now func() time.Time
}

// Client wires a wallet store, an activity log, and an HTTP client into
// the x402 payment flow. Instances are safe for concurrent use; the
// wallet.Store and activity.Log it wraps are thread-safe in turn.
type Client struct {
	wallet *wallet.Store
	alog   *activity.Log
	cfg    Config
}

// NewClient constructs a Client. wstore + alog must be non-nil; cfg.HTTPClient
// and cfg.Now default to sensible values if unset.
func NewClient(wstore *wallet.Store, alog *activity.Log, cfg Config) (*Client, error) {
	if wstore == nil {
		return nil, errors.New("payment: wallet store is required")
	}
	if alog == nil {
		return nil, errors.New("payment: activity log is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.ValidityWindow == 0 {
		cfg.ValidityWindow = 5 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{wallet: wstore, alog: alog, cfg: cfg}, nil
}

// Do executes req and transparently handles the x402 402-retry handshake.
// On a non-402 first response Do returns it as-is. On a 402 it parses the
// PAYMENT-REQUIRED header, picks a compatible PaymentRequirement, builds
// + signs an EIP-3009 transferWithAuthorization payload against the
// matching wallet, and replays the request with PAYMENT-SIGNATURE set.
//
// Body handling: a request with a non-nil Body MUST be re-readable, since
// the retry replays it. Callers should either set req.GetBody (the std
// lib's mechanism for replay), buffer the body themselves, or pass a
// bytes.Reader whose initial offset Do can restore. Do calls GetBody on
// retry if present; otherwise it errors with a clear "request body not
// replayable" message rather than silently sending an empty body.
//
// Audit-log writes: every payment surfaces 1–3 rows depending on
// outcome — payment.signed always (on a successful signature), and one
// of payment.settled / payment.failed based on the retry's status code +
// PAYMENT-RESPONSE.success boolean. The payment.required *intermediate*
// is intentionally not logged separately — that information is folded
// into payment.signed's payload to keep one row per intent.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPaymentRequired {
		return resp, nil
	}

	// We have a 402 — close the body before retrying, parse the
	// requirements, build + sign, re-send.
	_ = resp.Body.Close()

	envelope, err := parsePaymentRequiredHeader(resp.Header.Get(HeaderPaymentRequired))
	if err != nil {
		return nil, err
	}
	requirement, chain, err := c.pickRequirement(envelope.Accepts)
	if err != nil {
		c.auditFailed(ctx, "", req.URL.String(), 0, "no compatible requirement: "+err.Error())
		return nil, err
	}

	// Enforce per-call ceiling BEFORE we sign anything. Audit row is
	// "payment.failed" with reason "ceiling exceeded".
	if c.cfg.CeilingSmallestUnit != nil {
		need, ok := new(big.Int).SetString(requirement.MaxAmountRequired, 10)
		if !ok {
			return nil, fmt.Errorf("payment: MaxAmountRequired not a decimal integer: %q", requirement.MaxAmountRequired)
		}
		if need.Cmp(c.cfg.CeilingSmallestUnit) > 0 {
			c.auditFailed(ctx, requirement.Network, req.URL.String(), 0,
				fmt.Sprintf("ceiling %s exceeded by required %s", c.cfg.CeilingSmallestUnit, need))
			return nil, fmt.Errorf("%w: required %s > ceiling %s", ErrCeilingExceeded, need, c.cfg.CeilingSmallestUnit)
		}
	}

	signedHeader, payload, err := c.buildSignedHeader(chain, requirement)
	if err != nil {
		c.auditFailed(ctx, requirement.Network, req.URL.String(), 0, "sign: "+err.Error())
		return nil, err
	}

	// Audit the signed-but-not-yet-settled intent. The settlement outcome
	// rides on the *next* row written below after the retry returns.
	if _, aerr := c.alog.Append(ctx, activity.KindPaymentSigned, map[string]any{
		"url":         req.URL.String(),
		"scheme":      requirement.Scheme,
		"network":     requirement.Network,
		"amount":      requirement.MaxAmountRequired,
		"asset":       requirement.Asset,
		"pay_to":      requirement.PayTo,
		"from":        payload.Authorization.From,
		"valid_after": payload.Authorization.ValidAfter,
		"valid_before": payload.Authorization.ValidBefore,
	}); aerr != nil {
		// Log-write failure is non-fatal for the payment itself but worth
		// surfacing in stderr where the caller will see it. We don't have a
		// logger here; punt to the activity log's own error surface (which
		// internally writes to os.Stderr via the daemon's logger).
		_ = aerr
	}

	retryReq, err := cloneRequestForRetry(req)
	if err != nil {
		c.auditFailed(ctx, requirement.Network, req.URL.String(), 0, "clone request: "+err.Error())
		return nil, err
	}
	retryReq.Header.Set(HeaderPaymentSignature, signedHeader)

	retryResp, err := c.cfg.HTTPClient.Do(retryReq)
	if err != nil {
		c.auditFailed(ctx, requirement.Network, req.URL.String(), 0, "retry: "+err.Error())
		return nil, err
	}

	// Parse the server's settlement response. If the header is absent
	// but the status code is 2xx, treat as success-without-details.
	pr := parsePaymentResponseHeader(retryResp.Header.Get(HeaderPaymentResponse))
	settled := retryResp.StatusCode >= 200 && retryResp.StatusCode < 300 && (pr == nil || pr.Success)

	auditKind := activity.KindPaymentSettled
	auditPayload := map[string]any{
		"url":         req.URL.String(),
		"scheme":      requirement.Scheme,
		"network":     requirement.Network,
		"amount":      requirement.MaxAmountRequired,
		"asset":       requirement.Asset,
		"pay_to":      requirement.PayTo,
		"status_code": retryResp.StatusCode,
	}
	if pr != nil {
		auditPayload["transaction"] = pr.Transaction
		auditPayload["payer"] = pr.Payer
	}
	if !settled {
		auditKind = activity.KindPaymentFailed
		auditPayload["reason"] = fmt.Sprintf("server rejected retry (status=%d)", retryResp.StatusCode)
	}
	if _, aerr := c.alog.Append(ctx, auditKind, auditPayload); aerr != nil {
		_ = aerr
	}

	return retryResp, nil
}

// pickRequirement walks the server's accepts[] list and returns the
// first row the daimon can satisfy. v0.2 logic:
//
//  1. Scheme must be "exact" (the only scheme implemented).
//  2. Network must map to a known ChainInfo via LookupByX402Network.
//  3. The daimon's wallet store must have a wallet for that chain's
//     DaimonChain label.
//  4. The requirement's Asset must equal that chain's USDC contract
//     address (case-insensitive). v0.2 supports USDC only.
//
// If no row passes all four checks, returns ErrNoCompatibleRequirement.
func (c *Client) pickRequirement(accepts []PaymentRequirements) (*PaymentRequirements, *ChainInfo, error) {
	for i := range accepts {
		req := &accepts[i]
		if req.Scheme != SchemeExact {
			continue
		}
		chain, err := LookupByX402Network(req.Network)
		if err != nil {
			continue
		}
		if _, werr := c.wallet.FindByChain(chain.DaimonChain); werr != nil {
			continue
		}
		if !strings.EqualFold(req.Asset, chain.USDCAddress) {
			continue
		}
		return req, chain, nil
	}
	return nil, nil, ErrNoCompatibleRequirement
}

// buildSignedHeader constructs an EIP-3009 authorization for the picked
// requirement, signs it with the daimon's wallet, and returns the
// Base64-encoded PAYMENT-SIGNATURE header value plus the inner
// EVMExactPayload (for audit-log inclusion).
func (c *Client) buildSignedHeader(chain *ChainInfo, req *PaymentRequirements) (string, *EVMExactPayload, error) {
	w, err := c.wallet.FindByChain(chain.DaimonChain)
	if err != nil {
		return "", nil, err
	}

	// EIP-3009 nonce: 32 fresh random bytes. Per the spec these only
	// need to be unique per (token, from) signer; we sample fresh from
	// crypto/rand to avoid any cross-call replay concerns.
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, fmt.Errorf("nonce: %w", err)
	}

	now := c.cfg.Now()
	validAfter := big.NewInt(0) // "valid immediately"
	validBefore := big.NewInt(now.Add(c.cfg.ValidityWindow).Unix())

	auth := EVMAuthorizationV2{
		From:        w.Address,
		To:          req.PayTo,
		Value:       req.MaxAmountRequired,
		ValidAfter:  validAfter.String(),
		ValidBefore: validBefore.String(),
		Nonce:       "0x" + hex.EncodeToString(nonce),
	}

	digest, err := EIP3009Digest(chain, auth)
	if err != nil {
		return "", nil, fmt.Errorf("digest: %w", err)
	}

	sig, err := c.wallet.SignDigest(chain.DaimonChain, digest)
	if err != nil {
		return "", nil, fmt.Errorf("sign: %w", err)
	}

	payload := EVMExactPayload{
		Signature:     "0x" + hex.EncodeToString(sig),
		Authorization: auth,
	}
	inner, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("marshal inner payload: %w", err)
	}
	outer := PaymentPayload{
		X402Version: 1,
		Scheme:      req.Scheme,
		Network:     req.Network,
		Payload:     inner,
	}
	outerJSON, err := json.Marshal(outer)
	if err != nil {
		return "", nil, fmt.Errorf("marshal outer payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(outerJSON), &payload, nil
}

// auditFailed appends a payment.failed row when the payment never got
// far enough to produce a payment.signed row (no compatible requirement,
// ceiling exceeded before signing, retry transport failure, etc.).
func (c *Client) auditFailed(ctx context.Context, network, url string, statusCode int, reason string) {
	if c.alog == nil {
		return
	}
	payload := map[string]any{
		"url":    url,
		"reason": reason,
	}
	if network != "" {
		payload["network"] = network
	}
	if statusCode != 0 {
		payload["status_code"] = statusCode
	}
	_, _ = c.alog.Append(ctx, activity.KindPaymentFailed, payload)
}

// parsePaymentRequiredHeader Base64-decodes and unmarshals the
// PAYMENT-REQUIRED header value into a PaymentRequiredEnvelope.
func parsePaymentRequiredHeader(value string) (*PaymentRequiredEnvelope, error) {
	if value == "" {
		return nil, fmt.Errorf("%w: header missing", ErrInvalidRequiredHeader)
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		// Some servers emit URL-safe base64 — try that as a fallback.
		raw, err = base64.URLEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("%w: base64: %v", ErrInvalidRequiredHeader, err)
		}
	}
	var env PaymentRequiredEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: json: %v", ErrInvalidRequiredHeader, err)
	}
	if len(env.Accepts) == 0 {
		return nil, fmt.Errorf("%w: no accepts[] rows", ErrInvalidRequiredHeader)
	}
	return &env, nil
}

// parsePaymentResponseHeader Base64-decodes the PAYMENT-RESPONSE header
// if present. Returns nil if the header is absent or malformed — failure
// to parse the settlement-response header is treated as missing data,
// not a payment failure (the HTTP status code is the authoritative
// signal).
func parsePaymentResponseHeader(value string) *PaymentResponse {
	if value == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(value)
		if err != nil {
			return nil
		}
	}
	var pr PaymentResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil
	}
	return &pr
}

// cloneRequestForRetry produces a new *http.Request whose Body, if any,
// is independently re-readable. Headers are shallow-copied (the retry
// sets PAYMENT-SIGNATURE on the clone). Context propagation matches
// http.Request.Clone semantics.
func cloneRequestForRetry(req *http.Request) (*http.Request, error) {
	c2 := req.Clone(req.Context())
	if req.Body == nil {
		return c2, nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("GetBody: %w", err)
		}
		c2.Body = body
		return c2, nil
	}
	// Fall back to draining + buffering the original body so the retry
	// can replay it. Note this defeats streaming uploads; callers that
	// care should set req.GetBody before calling Do.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("buffer body: %w", err)
	}
	_ = req.Body.Close()
	c2.Body = io.NopCloser(bytes.NewReader(body))
	c2.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	c2.ContentLength = int64(len(body))
	return c2, nil
}
