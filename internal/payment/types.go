// Package payment implements the x402 HTTP-402 payment flow as defined by
// the x402 v2 specification (https://docs.x402.org).
//
// v0.2 scope (Daimon phase 40.3):
//
//   - EVM `exact` scheme via EIP-3009 `transferWithAuthorization` only.
//     Permit2 (EIP-712 witness-transfer) and ERC-7710 (smart-account
//     delegation) variants are reserved for v0.2.x.
//   - Bring-your-own-HTTP-client: the package takes an *http.Client and
//     does not impose timeouts or transport choices on callers.
//   - Audit-log integration: every payment attempt writes a chained,
//     signed row to the principal's activity log. Failed signing,
//     rejected retries, and successful settlements are all distinct
//     audit-row kinds (see internal/activity/activity.go).
//   - No facilitator client. Facilitators are a SERVER-side concern (the
//     resource server calls /verify + /settle); the Daimon-as-client
//     surface just constructs valid PAYMENT-SIGNATURE headers and sends
//     them. Wallet-balance probes via facilitator land in v0.2.x.
//
// Wire framing (x402 v2, from https://docs.x402.org/core-concepts/http-402):
//
//	C ─── GET /resource ────────────────────────────────────────────► S
//	C ◄── 402 Payment Required, PAYMENT-REQUIRED: <base64(json)> ── S
//	C ─── GET /resource, PAYMENT-SIGNATURE: <base64(json)> ─────────► S
//	C ◄── 200 OK, PAYMENT-RESPONSE: <base64(json)> ──────────────── S
//
// Both Base64 envelopes wrap canonical JSON. Field names match the
// upstream specs exactly — we don't paraphrase wire-level data because
// any facilitator/server interop depends on byte-for-byte matches.
package payment

import "encoding/json"

// HeaderPaymentRequired is the HTTP header carrying the Base64-encoded
// PaymentRequirements list returned alongside HTTP 402. Spelled in all
// caps per the x402 v2 documentation; HTTP headers are case-insensitive
// but the canonical form is preserved for readability.
const HeaderPaymentRequired = "Payment-Required"

// HeaderPaymentSignature is the HTTP header the client sets on the retry
// request, carrying the Base64-encoded PaymentPayload that authorises
// settlement against the chosen requirement.
const HeaderPaymentSignature = "Payment-Signature"

// HeaderPaymentResponse is the HTTP header the server sets on the
// post-settlement response describing the on-chain outcome.
const HeaderPaymentResponse = "Payment-Response"

// SchemeExact identifies the "transfer exactly this amount" scheme. v0.2
// supports this scheme only; future schemes (upto / batch / streaming)
// register under different string constants.
const SchemeExact = "exact"

// PaymentRequirements is one row in the 402-response payload's `accepts`
// list. The server publishes N rows; the client picks one whose
// (scheme, network, asset) tuple it can satisfy and signs against it.
//
// Field names are wire-canonical per the x402 v2 spec — do NOT rename
// without confirming the rename round-trips through the upstream
// reference implementations.
type PaymentRequirements struct {
	Scheme            string          `json:"scheme"`           // "exact"
	Network           string          `json:"network"`          // "base", "base-sepolia", "ethereum", ...
	MaxAmountRequired string          `json:"maxAmountRequired"` // smallest-unit integer, decimal string
	Resource          string          `json:"resource"`         // URL of the protected endpoint
	Description       string          `json:"description"`      // human-readable purpose
	MimeType          string          `json:"mimeType,omitempty"`
	OutputSchema      json.RawMessage `json:"outputSchema,omitempty"`
	PayTo             string          `json:"payTo"`           // 0x... recipient address
	MaxTimeoutSeconds int             `json:"maxTimeoutSeconds"`
	Asset             string          `json:"asset"`           // 0x... token contract
	Extra             json.RawMessage `json:"extra,omitempty"` // scheme-specific (e.g., EIP-712 domain hints)
}

// PaymentRequiredEnvelope is the JSON object the server Base64-encodes
// into the PAYMENT-REQUIRED header. `x402Version` is currently `1` per
// x402 v2 (the protocol version int is independent of the document
// version label, by the spec's choice).
type PaymentRequiredEnvelope struct {
	X402Version int                   `json:"x402Version"`
	Accepts     []PaymentRequirements `json:"accepts"`
	Error       string                `json:"error,omitempty"`
}

// PaymentPayload is what the client Base64-encodes into the
// PAYMENT-SIGNATURE header on the retry request. The Payload field's
// concrete shape depends on Scheme+Network; for EVM `exact` it is an
// EVMExactPayload.
type PaymentPayload struct {
	X402Version int             `json:"x402Version"`
	Scheme      string          `json:"scheme"`
	Network     string          `json:"network"`
	Payload     json.RawMessage `json:"payload"`
}

// EVMExactPayload is the body of PaymentPayload.Payload for
// (scheme="exact", network="evm:..."). Mirrors the EIP-3009 path of
// `specs/schemes/exact/scheme_exact_evm.md` in the x402 repo: a 65-byte
// secp256k1 signature plus the six authorization fields the on-chain
// USDC `transferWithAuthorization` function expects.
type EVMExactPayload struct {
	Signature     string             `json:"signature"`     // 0x-prefixed 130 hex chars
	Authorization EVMAuthorizationV2 `json:"authorization"`
}

// EVMAuthorizationV2 is the EIP-3009 authorization message. All
// uint256 fields are decimal-string encoded on the wire to avoid the
// JSON-number precision cliff at 2^53; addresses use checksummed EIP-55
// (case is preserved verbatim but not enforced).
type EVMAuthorizationV2 struct {
	From        string `json:"from"`        // payer wallet address
	To          string `json:"to"`          // PaymentRequirements.PayTo
	Value       string `json:"value"`       // PaymentRequirements.MaxAmountRequired
	ValidAfter  string `json:"validAfter"`  // unix seconds, decimal
	ValidBefore string `json:"validBefore"` // unix seconds, decimal
	Nonce       string `json:"nonce"`       // 0x-prefixed 64 hex chars (32 random bytes)
}

// PaymentResponse is the post-settlement structured feedback the server
// returns in the PAYMENT-RESPONSE header on the 200 response. The exact
// fields vary by scheme but x402 v2 standardises a small core.
type PaymentResponse struct {
	Success     bool            `json:"success"`
	Transaction string          `json:"transaction,omitempty"` // tx hash on success
	Network     string          `json:"network,omitempty"`
	Payer       string          `json:"payer,omitempty"`
	Extra       json.RawMessage `json:"extra,omitempty"`
}
