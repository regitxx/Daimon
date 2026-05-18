package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/payment"
)

// Payment handler — daimon.payment.pay.
//
// SPEC §6.1.X (proposed, v0.2 draft — see design/v0.2-wallet.md §4.2). The
// handler wraps internal/payment.Client around the unlocked daimon's wallet
// + activity log and executes a single outbound HTTP request through the
// x402 402-retry flow. Callers express the request as fields in the RPC
// params; the handler returns the resource response shape (status + body
// bytes + relevant headers + parsed PAYMENT-RESPONSE if present).
//
// The wallet keystore MUST be loaded for this handler to work. Without it,
// every paid endpoint would yield a "no compatible requirement" error
// after one unnecessary round-trip; surfacing that earlier as a precondition
// failure is friendlier.

// --- daimon.payment.pay ------------------------------------------------------

type paymentPayParams struct {
	URL string `json:"url"`
	// Method defaults to GET. Case-insensitive; normalised to upper.
	Method string `json:"method,omitempty"`
	// Headers are extra request headers the caller wants set on the
	// outbound request. The payment client adds PAYMENT-SIGNATURE
	// internally on the retry; callers should not set it themselves.
	Headers map[string]string `json:"headers,omitempty"`
	// Body content. Mutually exclusive — only one of body_base64 /
	// body_text should be set. Both empty means no body.
	BodyBase64 string `json:"body_base64,omitempty"`
	BodyText   string `json:"body_text,omitempty"`
	// CeilingSmallestUnit caps the smallest-unit value the daimon will
	// sign in a single payment. Decimal string. Empty means "no ceiling"
	// — strongly discouraged in production; the CLI defaults this to
	// 100000 ($0.10 USDC) when the user doesn't override.
	CeilingSmallestUnit string `json:"ceiling_smallest_unit,omitempty"`
	// ValiditySeconds overrides the EIP-3009 validBefore window. Default
	// 300 (5 min). Smaller windows tighten replay risk at the cost of
	// re-signing if the server takes a while to settle.
	ValiditySeconds int `json:"validity_seconds,omitempty"`
}

type paymentPayResult struct {
	StatusCode       int                      `json:"status_code"`
	ResponseHeaders  map[string]string        `json:"response_headers,omitempty"`
	ResponseBodyB64  string                   `json:"response_body_base64"`
	PaymentResponse  *payment.PaymentResponse `json:"payment_response,omitempty"`
}

func (s *Server) handlePaymentPay(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.wstore == nil {
		return nil, newError(CodeInvalidRequest, "wallet keystore not loaded; cannot make x402 payments")
	}
	if s.alog == nil {
		return nil, newError(CodeInternalError, "activity log not loaded")
	}

	var p paymentPayParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.URL == "" {
		return nil, newError(CodeInvalidParams, "url is required")
	}
	if p.BodyBase64 != "" && p.BodyText != "" {
		return nil, newError(CodeInvalidParams, "body_base64 and body_text are mutually exclusive")
	}

	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = http.MethodGet
	}

	var body io.Reader
	if p.BodyBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(p.BodyBase64)
		if err != nil {
			return nil, newError(CodeInvalidParams, "body_base64 is not valid base64", err.Error())
		}
		body = bytes.NewReader(decoded)
	} else if p.BodyText != "" {
		body = strings.NewReader(p.BodyText)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.URL, body)
	if err != nil {
		return nil, newError(CodeInvalidParams, "construct request", err.Error())
	}
	for k, v := range p.Headers {
		// Caller-set PAYMENT-SIGNATURE would be overwritten by the
		// payment client anyway, but rejecting it explicitly keeps the
		// audit-log story unambiguous about who signed what.
		if strings.EqualFold(k, payment.HeaderPaymentSignature) {
			return nil, newError(CodeInvalidParams, "callers must not set PAYMENT-SIGNATURE; the daemon constructs it from the wallet")
		}
		req.Header.Set(k, v)
	}

	// Build the per-call payment.Client. Cheap (just three pointers
	// + Config); no caching needed.
	cfg := payment.Config{}
	if p.CeilingSmallestUnit != "" {
		ceiling, ok := new(big.Int).SetString(p.CeilingSmallestUnit, 10)
		if !ok || ceiling.Sign() < 0 {
			return nil, newError(CodeInvalidParams, "ceiling_smallest_unit must be a non-negative decimal integer", p.CeilingSmallestUnit)
		}
		cfg.CeilingSmallestUnit = ceiling
	}
	if p.ValiditySeconds > 0 {
		cfg.ValidityWindow = secondsToDuration(p.ValiditySeconds)
	}

	pc, err := payment.NewClient(s.wstore, s.alog, cfg)
	if err != nil {
		return nil, newError(CodeInternalError, "construct payment client", err.Error())
	}

	resp, err := pc.Do(ctx, req)
	if err != nil {
		// Map the typed errors to RPC codes so SDK consumers can branch
		// without string-matching.
		switch {
		case errors.Is(err, payment.ErrCeilingExceeded):
			return nil, newError(CodePaymentCeiling, "payment exceeds local ceiling", err.Error())
		case errors.Is(err, payment.ErrNoCompatibleRequirement):
			return nil, newError(CodePaymentUnsupported, "no wallet matches the resource's payment requirements", err.Error())
		case errors.Is(err, payment.ErrInvalidRequiredHeader):
			return nil, newError(CodeInternalError, "server emitted malformed PAYMENT-REQUIRED header", err.Error())
		}
		return nil, newError(CodeInternalError, "payment request failed", err.Error())
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newError(CodeInternalError, "read response body", err.Error())
	}

	out := paymentPayResult{
		StatusCode:      resp.StatusCode,
		ResponseBodyB64: base64.StdEncoding.EncodeToString(bodyBytes),
	}
	// Surface a small allowlist of headers — full headers would leak a
	// lot of server framing into the audit-log-adjacent RPC surface.
	// PAYMENT-RESPONSE + standard content-type are enough for callers
	// that need to know what they bought.
	headersToSurface := []string{
		payment.HeaderPaymentResponse,
		"Content-Type",
		"Content-Length",
	}
	if len(resp.Header) > 0 {
		out.ResponseHeaders = make(map[string]string, len(headersToSurface))
		for _, h := range headersToSurface {
			if v := resp.Header.Get(h); v != "" {
				out.ResponseHeaders[h] = v
			}
		}
	}
	out.PaymentResponse = parsePaymentResponseHeader(resp.Header.Get(payment.HeaderPaymentResponse))

	return out, nil
}

// secondsToDuration accepts an int (seconds) and returns the
// time.Duration; isolated for readability.
func secondsToDuration(s int) time.Duration {
	return time.Duration(s) * time.Second
}

// parsePaymentResponseHeader mirrors internal/payment's private parser
// but is duplicated here because we want to return the PaymentResponse
// in the handler's result struct. Calling into the payment package's
// already-tested decoding via a tiny helper would be cleaner; left as
// a follow-up to keep this commit small.
func parsePaymentResponseHeader(value string) *payment.PaymentResponse {
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
	var pr payment.PaymentResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil
	}
	return &pr
}

