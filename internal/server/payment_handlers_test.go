package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/payment"
)

// --- helpers ----------------------------------------------------------------

// paymentMockServer is a tiny stand-in for an x402-protected resource. It
// emits a single PaymentRequirements row matching the daimon's evm:base
// wallet, then validates the retry's PAYMENT-SIGNATURE shape (not the
// cryptography — internal/payment's tests already cover that — just the
// presence and Base64-correctness) before returning 200.
func newPaymentMockServer(t *testing.T, payTo, asset, amount string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(payment.HeaderPaymentSignature) == "" {
			env := payment.PaymentRequiredEnvelope{
				X402Version: 1,
				Accepts: []payment.PaymentRequirements{
					{
						Scheme:            payment.SchemeExact,
						Network:           "base",
						MaxAmountRequired: amount,
						Resource:          "http://" + r.Host + r.URL.Path,
						Description:       "test paid resource",
						PayTo:             payTo,
						MaxTimeoutSeconds: 60,
						Asset:             asset,
					},
				},
			}
			raw, _ := json.Marshal(env)
			w.Header().Set(payment.HeaderPaymentRequired, base64.StdEncoding.EncodeToString(raw))
			w.WriteHeader(http.StatusPaymentRequired)
			return
		}

		// Verify the retry header is well-formed Base64 JSON, but skip
		// the cryptographic recovery check — internal/payment's tests
		// already cover signature recovery against the wallet pubkey.
		sigB64 := r.Header.Get(payment.HeaderPaymentSignature)
		raw, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			t.Fatalf("mock server: PAYMENT-SIGNATURE not valid base64: %v", err)
		}
		var outer payment.PaymentPayload
		if err := json.Unmarshal(raw, &outer); err != nil {
			t.Fatalf("mock server: PAYMENT-SIGNATURE not valid JSON: %v", err)
		}
		if outer.Scheme != payment.SchemeExact || outer.Network != "base" {
			t.Fatalf("mock server: outer scheme/network = %s/%s, want exact/base", outer.Scheme, outer.Network)
		}

		// Emit a settled PAYMENT-RESPONSE.
		pr := payment.PaymentResponse{
			Success:     true,
			Transaction: "0x" + strings.Repeat("dd", 32),
			Network:     "base",
		}
		prRaw, _ := json.Marshal(pr)
		w.Header().Set(payment.HeaderPaymentResponse, base64.StdEncoding.EncodeToString(prRaw))
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("the body you paid for"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- handler tests -----------------------------------------------------------

func TestHandlePaymentPay_RoundTripsThrough402(t *testing.T) {
	f, _ := newWalletFixture(t)
	// Create the wallet for evm:base so the payment client can satisfy
	// the mock server's requirement.
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	mustNoError(t, resp)

	chain, err := payment.LookupByX402Network("base")
	if err != nil {
		t.Fatalf("LookupByX402Network: %v", err)
	}
	srv := newPaymentMockServer(t, "0xffaa00000000000000000000000000000000aaff", chain.USDCAddress, "100")

	resp = f.call(t, "daimon.payment.pay", map[string]any{
		"url":                   srv.URL + "/resource",
		"method":                "GET",
		"ceiling_smallest_unit": "100000", // $0.10 USDC
	})
	var out paymentPayResult
	resultAs(t, resp, &out)

	if out.StatusCode != http.StatusOK {
		t.Fatalf("status_code = %d, want 200", out.StatusCode)
	}
	body, err := base64.StdEncoding.DecodeString(out.ResponseBodyB64)
	if err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if string(body) != "the body you paid for" {
		t.Fatalf("response body = %q, want %q", string(body), "the body you paid for")
	}
	if out.PaymentResponse == nil || !out.PaymentResponse.Success {
		t.Fatalf("expected PaymentResponse.Success=true, got %+v", out.PaymentResponse)
	}
	if out.ResponseHeaders["Content-Type"] != "text/plain" {
		t.Fatalf("content-type header not surfaced: %+v", out.ResponseHeaders)
	}

	// Audit log should now have payment.signed + payment.settled.
	entries, err := f.alog.Query(t.Context(), activity.QueryOptions{})
	if err != nil {
		t.Fatalf("activity.Query: %v", err)
	}
	var signed, settled int
	for _, e := range entries {
		switch e.Kind {
		case activity.KindPaymentSigned:
			signed++
		case activity.KindPaymentSettled:
			settled++
		}
	}
	if signed != 1 {
		t.Fatalf("expected 1 payment.signed row, got %d", signed)
	}
	if settled != 1 {
		t.Fatalf("expected 1 payment.settled row, got %d", settled)
	}
}

func TestHandlePaymentPay_FailsWithoutWalletKeystore(t *testing.T) {
	// newFixture has wstore=nil — every payment call should be rejected
	// with CodeInvalidRequest before any HTTP is attempted.
	f := newFixture(t)
	resp := f.call(t, "daimon.payment.pay", map[string]any{
		"url": "http://example.invalid/",
	})
	if resp.Error == nil {
		t.Fatal("expected error when wallet keystore is not loaded")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Fatalf("error code = %d, want CodeInvalidRequest (%d)", resp.Error.Code, CodeInvalidRequest)
	}
}

func TestHandlePaymentPay_RejectsMissingURL(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.payment.pay", map[string]any{})
	if resp.Error == nil {
		t.Fatal("expected error for missing url")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error code = %d, want CodeInvalidParams (%d)", resp.Error.Code, CodeInvalidParams)
	}
}

func TestHandlePaymentPay_RejectsConflictingBodies(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.payment.pay", map[string]any{
		"url":         "http://example.invalid/",
		"body_text":   "hello",
		"body_base64": "aGVsbG8=",
	})
	if resp.Error == nil {
		t.Fatal("expected error for conflicting body params")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error code = %d, want CodeInvalidParams", resp.Error.Code)
	}
}

func TestHandlePaymentPay_RejectsCallerSetPaymentSignature(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.payment.pay", map[string]any{
		"url": "http://example.invalid/",
		"headers": map[string]string{
			payment.HeaderPaymentSignature: "ignore-me",
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error when caller sets PAYMENT-SIGNATURE")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Fatalf("error code = %d, want CodeInvalidParams", resp.Error.Code)
	}
}

func TestHandlePaymentPay_CeilingExceededMappedToTypedCode(t *testing.T) {
	f, _ := newWalletFixture(t)
	resp := f.call(t, "daimon.wallet.create", map[string]any{"chain": "evm:base"})
	mustNoError(t, resp)

	chain, _ := payment.LookupByX402Network("base")
	// Mock demands more than the ceiling will allow.
	srv := newPaymentMockServer(t, "0xff000000000000000000000000000000000000ff", chain.USDCAddress, "999999999")

	resp = f.call(t, "daimon.payment.pay", map[string]any{
		"url":                   srv.URL + "/expensive",
		"ceiling_smallest_unit": "100", // 100 smallest units = $0.0001 USDC
	})
	if resp.Error == nil {
		t.Fatal("expected error for ceiling-exceeded")
	}
	if resp.Error.Code != CodePaymentCeiling {
		t.Fatalf("error code = %d, want CodePaymentCeiling (%d)", resp.Error.Code, CodePaymentCeiling)
	}
}

func TestHandlePaymentPay_NoCompatibleRequirementMappedToTypedCode(t *testing.T) {
	f, _ := newWalletFixture(t)
	// Note: NO wallet.create — the wallet store has no entries, so no
	// requirement can be satisfied.

	chain, _ := payment.LookupByX402Network("base")
	srv := newPaymentMockServer(t, "0xff000000000000000000000000000000000000ff", chain.USDCAddress, "100")

	resp := f.call(t, "daimon.payment.pay", map[string]any{
		"url": srv.URL + "/",
	})
	if resp.Error == nil {
		t.Fatal("expected error for no compatible requirement")
	}
	if resp.Error.Code != CodePaymentUnsupported {
		t.Fatalf("error code = %d, want CodePaymentUnsupported (%d)", resp.Error.Code, CodePaymentUnsupported)
	}
}
