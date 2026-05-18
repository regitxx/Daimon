package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
)

// cmdPayment routes the `payment` subcommand surface. v0.2 ships one verb,
// `pay`, which wraps daimon.payment.pay end-to-end: the CLI buffers any
// request body, dispatches the RPC, base64-decodes the response, and
// writes it to stdout the way curl(1) would.
//
// Future v0.2.x verbs (`payment status <id>`, `payment ceiling get/set`)
// will land under the same router.
func cmdPayment(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon payment <pay> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "pay":
		return cmdPaymentPay(rest)
	default:
		return fmt.Errorf("daimon payment: unknown subcommand %q", sub)
	}
}

// --- daimon payment pay ------------------------------------------------------

// cmdPaymentPay calls daimon.payment.pay. The URL is positional; flags map
// to the RPC param shape. The response body is base64-decoded and written
// to stdout — by default raw to support piping into file or jq; --json
// emits the full result envelope including the parsed PaymentResponse.
//
// Ceiling defaults: --ceiling-usd 0.10 (= 100000 USDC smallest units; USDC
// has 6 decimals). The flag accepts a decimal USD amount for ergonomics
// and converts internally. Set to 0 to disable (NOT recommended — the
// ceiling is the canonical defense against a malicious endpoint draining
// the wallet).
func cmdPaymentPay(args []string) error {
	fs := flag.NewFlagSet("daimon payment pay", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	method := fs.String("method", "GET", "HTTP method (GET, POST, etc.)")
	bodyFile := fs.String("body-file", "", "path to a file whose contents become the request body; '-' means stdin")
	bodyText := fs.String("body", "", "request body as a literal string (mutually exclusive with --body-file)")
	header := fs.String("header", "", "extra request header in 'Name: value' form; repeatable via comma separation")
	ceilingUSD := fs.Float64("ceiling-usd", 0.10, "per-payment USD ceiling (0 disables — not recommended)")
	validitySeconds := fs.Int("validity-seconds", 0, "override EIP-3009 validBefore window in seconds (0 = 5 min default)")
	asJSON := fs.Bool("json", false, "emit the full RPC result envelope as JSON (default: write decoded body to stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon payment pay [flags] <url>")
	}
	url := fs.Arg(0)

	if *bodyText != "" && *bodyFile != "" {
		return fmt.Errorf("--body and --body-file are mutually exclusive")
	}

	params := map[string]any{
		"url":    url,
		"method": *method,
	}

	// Body resolution.
	var bodyBytes []byte
	switch {
	case *bodyFile == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		bodyBytes = b
	case *bodyFile != "":
		b, err := os.ReadFile(*bodyFile)
		if err != nil {
			return fmt.Errorf("read --body-file %s: %w", *bodyFile, err)
		}
		bodyBytes = b
	case *bodyText != "":
		bodyBytes = []byte(*bodyText)
	}
	if len(bodyBytes) > 0 {
		params["body_base64"] = base64.StdEncoding.EncodeToString(bodyBytes)
	}

	// Headers.
	if *header != "" {
		hdrs := map[string]string{}
		for _, h := range strings.Split(*header, ",") {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			i := strings.IndexByte(h, ':')
			if i < 0 {
				return fmt.Errorf("--header %q: expected 'Name: value' form", h)
			}
			hdrs[strings.TrimSpace(h[:i])] = strings.TrimSpace(h[i+1:])
		}
		if len(hdrs) > 0 {
			params["headers"] = hdrs
		}
	}

	// Ceiling: USD → USDC smallest units (6 decimals). Convert via
	// big.Float to avoid the float64 precision cliff at $0.000001 scale.
	if *ceilingUSD > 0 {
		usd := new(big.Float).SetFloat64(*ceilingUSD)
		usd.Mul(usd, big.NewFloat(1e6)) // 6 decimals on USDC
		smallest, _ := usd.Int(nil)
		params["ceiling_smallest_unit"] = smallest.String()
	}

	if *validitySeconds > 0 {
		params["validity_seconds"] = *validitySeconds
	}

	var result struct {
		StatusCode      int               `json:"status_code"`
		ResponseHeaders map[string]string `json:"response_headers"`
		ResponseBodyB64 string            `json:"response_body_base64"`
		PaymentResponse *struct {
			Success     bool   `json:"success"`
			Transaction string `json:"transaction"`
			Network     string `json:"network"`
			Payer       string `json:"payer"`
		} `json:"payment_response"`
	}
	if err := daemonCall("daimon.payment.pay", params, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}

	// Default: print HTTP-style header to stderr, decoded body to stdout
	// — pipeable: `daimon payment pay URL > /tmp/out.bin`.
	fmt.Fprintf(os.Stderr, "HTTP %d\n", result.StatusCode)
	if ct := result.ResponseHeaders["Content-Type"]; ct != "" {
		fmt.Fprintf(os.Stderr, "Content-Type: %s\n", ct)
	}
	if result.PaymentResponse != nil {
		fmt.Fprintf(os.Stderr, "Payment: success=%v tx=%s network=%s payer=%s\n",
			result.PaymentResponse.Success,
			result.PaymentResponse.Transaction,
			result.PaymentResponse.Network,
			result.PaymentResponse.Payer,
		)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.ResponseBodyB64)
	if err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	if _, err := os.Stdout.Write(decoded); err != nil {
		return err
	}
	return nil
}
