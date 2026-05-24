package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// cmdCapability routes the `daimon capability` subcommand surface (v0.4 phase 45).
//
// Subcommand tree:
//
//	daimon capability issue --verb <verb> [--verb …] [options]   Mint a new token
//	daimon capability list [--all] [--json]                      List issued tokens
//	daimon capability revoke <token_id>                          Revoke a token
//	daimon capability attenuate <token> [options]                Add tighter constraints
func cmdCapability(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon capability <issue|list|revoke|attenuate> [args]\n" +
			"Run 'daimon help' for full usage.")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "issue":
		return cmdCapabilityIssue(rest)
	case "list":
		return cmdCapabilityList(rest)
	case "revoke":
		return cmdCapabilityRevoke(rest)
	case "attenuate":
		return cmdCapabilityAttenuate(rest)
	default:
		return fmt.Errorf("daimon capability: unknown subcommand %q", sub)
	}
}

// ---------------------------------------------------------------------------
// Wire types (mirror internal/server/capability_handlers.go)
// ---------------------------------------------------------------------------

type capabilityIssueWire struct {
	TokenID   string `json:"token_id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type capabilityTokenWireItem struct {
	TokenID         string   `json:"token_id"`
	Verbs           []string `json:"verbs"`
	GranteeDID      string   `json:"grantee_did,omitempty"`
	TargetDID       string   `json:"target_did,omitempty"`
	ValidUntil      string   `json:"valid_until,omitempty"`
	MaxCalls        int64    `json:"max_calls,omitempty"`
	ModelConstraint string   `json:"model_constraint,omitempty"`
	IssuedAt        string   `json:"issued_at"`
	Revoked         bool     `json:"revoked"`
	RevokedAt       string   `json:"revoked_at,omitempty"`
}

type capabilityListWire struct {
	Tokens []capabilityTokenWireItem `json:"tokens"`
}

type capabilityAttenuateWire struct {
	Token string `json:"token"`
}

// ---------------------------------------------------------------------------
// multiVerb: a repeatable --verb flag
// ---------------------------------------------------------------------------

type multiVerb []string

func (m *multiVerb) String() string  { return strings.Join(*m, ",") }
func (m *multiVerb) Set(s string) error { *m = append(*m, s); return nil }

// ---------------------------------------------------------------------------
// daimon capability issue
// ---------------------------------------------------------------------------

func cmdCapabilityIssue(args []string) error {
	fs := flag.NewFlagSet("daimon capability issue", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var verbs multiVerb
	fs.Var(&verbs, "verb", "Verb to grant (e.g. peer.ask). Repeatable.")
	validUntil := fs.String("valid-until", "", "Expiry timestamp, RFC3339 (e.g. 2026-12-31T00:00:00Z)")
	maxCalls := fs.Int64("max-calls", 0, "Maximum number of calls; 0 = unlimited")
	model := fs.String("model", "", "Constrain the token to a single model (e.g. claude-haiku-4-5)")
	grantee := fs.String("grantee", "", "Grantee DID (did:key:…); omit to issue an any-target token")
	asJSON := fs.Bool("json", false, "Emit full result as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(verbs) == 0 {
		return fmt.Errorf("daimon capability issue: at least one --verb is required")
	}

	params := map[string]any{"verbs": []string(verbs)}
	if *validUntil != "" {
		// Validate before sending — the daemon will also validate, but a
		// client-side check produces a friendlier error message.
		if _, err := time.Parse(time.RFC3339, *validUntil); err != nil {
			return fmt.Errorf("--valid-until: invalid RFC3339 timestamp: %v", err)
		}
		params["valid_until"] = *validUntil
	}
	if *maxCalls > 0 {
		params["max_calls"] = *maxCalls
	}
	if *model != "" {
		params["model_constraint"] = *model
	}
	if *grantee != "" {
		params["grantee_did"] = *grantee
	}

	var result capabilityIssueWire
	if err := daemonCall("daimon.capability.issue", params, &result); err != nil {
		return err
	}

	if *asJSON {
		return printJSON(result)
	}

	fmt.Printf("token_id:   %s\n", result.TokenID)
	fmt.Printf("token:      %s\n", result.Token)
	if result.ExpiresAt != "" {
		fmt.Printf("expires_at: %s\n", result.ExpiresAt)
	} else {
		fmt.Printf("expires_at: (no expiry)\n")
	}
	return nil
}

// ---------------------------------------------------------------------------
// daimon capability list
// ---------------------------------------------------------------------------

func cmdCapabilityList(args []string) error {
	fs := flag.NewFlagSet("daimon capability list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	all := fs.Bool("all", false, "Include revoked tokens")
	asJSON := fs.Bool("json", false, "Emit full result as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	var result capabilityListWire
	if err := daemonCall("daimon.capability.list", map[string]any{"include_revoked": *all}, &result); err != nil {
		return err
	}

	if *asJSON {
		return printJSON(result)
	}

	if len(result.Tokens) == 0 {
		fmt.Println("(no capability tokens issued)")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TOKEN_ID\tVERBS\tGRANTEE\tEXPIRES\tMAX_CALLS\tISSUED\tSTATUS")
	for _, t := range result.Tokens {
		expires := "–"
		if t.ValidUntil != "" {
			expires = t.ValidUntil
		}
		grantee := t.GranteeDID
		if grantee == "" {
			grantee = "(any)"
		}
		maxCalls := "–"
		if t.MaxCalls > 0 {
			maxCalls = fmt.Sprintf("%d", t.MaxCalls)
		}
		status := "active"
		if t.Revoked {
			status = "revoked"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			truncate(t.TokenID, 26),
			strings.Join(t.Verbs, ","),
			truncate(grantee, 24),
			expires,
			maxCalls,
			t.IssuedAt,
			status,
		)
	}
	return w.Flush()
}

// ---------------------------------------------------------------------------
// daimon capability revoke
// ---------------------------------------------------------------------------

func cmdCapabilityRevoke(args []string) error {
	fs := flag.NewFlagSet("daimon capability revoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon capability revoke <token_id>")
	}
	tokenID := fs.Arg(0)

	if err := daemonCall("daimon.capability.revoke", map[string]any{"token_id": tokenID}, nil); err != nil {
		return err
	}
	fmt.Printf("revoked %s\n", tokenID)
	return nil
}

// ---------------------------------------------------------------------------
// daimon capability attenuate
// ---------------------------------------------------------------------------

func cmdCapabilityAttenuate(args []string) error {
	fs := flag.NewFlagSet("daimon capability attenuate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	validUntil := fs.String("valid-until", "", "Tighter expiry, RFC3339")
	maxCalls := fs.Int64("max-calls", 0, "Tighter max-calls ceiling")
	model := fs.String("model", "", "Add or tighten model constraint")
	asJSON := fs.Bool("json", false, "Emit full result as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon capability attenuate <base64url-token> [--valid-until T] [--max-calls N] [--model M]")
	}
	token := fs.Arg(0)

	params := map[string]any{"token": token}
	if *validUntil != "" {
		if _, err := time.Parse(time.RFC3339, *validUntil); err != nil {
			return fmt.Errorf("--valid-until: invalid RFC3339 timestamp: %v", err)
		}
		params["valid_until"] = *validUntil
	}
	if *maxCalls > 0 {
		params["max_calls"] = *maxCalls
	}
	if *model != "" {
		params["model_constraint"] = *model
	}

	var result capabilityAttenuateWire
	if err := daemonCall("daimon.capability.attenuate", params, &result); err != nil {
		return err
	}

	if *asJSON {
		return printJSON(result)
	}
	fmt.Println(result.Token)
	return nil
}
