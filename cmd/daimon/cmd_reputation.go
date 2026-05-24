package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

// cmdReputation routes the `daimon reputation` subcommand surface (v0.4 phase 45).
//
// Subcommand tree:
//
//	daimon reputation receipts [--direction issued|received] [--json]
func cmdReputation(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon reputation <receipts> [args]\n" +
			"Run 'daimon help' for full usage.")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "receipts":
		return cmdReputationReceipts(rest)
	default:
		return fmt.Errorf("daimon reputation: unknown subcommand %q", sub)
	}
}

// ---------------------------------------------------------------------------
// Wire type (mirrors internal/server/reputation_handlers.go)
// ---------------------------------------------------------------------------

type reputationReceiptsWire struct {
	Receipts []reputationReceiptItemWire `json:"receipts"`
}

type reputationReceiptItemWire struct {
	ReceiptID    string `json:"receipt_id"`
	Direction    string `json:"direction"`
	ServedAt     string `json:"served_at"`
	Verb         string `json:"verb"`
	ServerDID    string `json:"server_did"`
	CallerDID    string `json:"caller_did,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	DurationMS   int64  `json:"duration_ms"`
	Signature    string `json:"signature,omitempty"`
}

// ---------------------------------------------------------------------------
// daimon reputation receipts
// ---------------------------------------------------------------------------

func cmdReputationReceipts(args []string) error {
	fs := flag.NewFlagSet("daimon reputation receipts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	direction := fs.String("direction", "", `Filter: "issued" (we served the call) | "received" (remote served us) | omit for both`)
	asJSON := fs.Bool("json", false, "Emit full result as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon reputation receipts [--direction issued|received] [--json]")
	}

	params := map[string]any{}
	if *direction != "" {
		params["direction"] = *direction
	}

	var result reputationReceiptsWire
	if err := daemonCall("daimon.reputation.receipts", params, &result); err != nil {
		return err
	}

	if *asJSON {
		return printJSON(result)
	}

	if len(result.Receipts) == 0 {
		fmt.Println("(no reputation receipts)")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RECEIPT_ID\tDIR\tSERVED_AT\tVERB\tMODEL\tIN\tOUT\tMS\tSERVER")
	for _, r := range result.Receipts {
		dir := r.Direction
		if dir == "" {
			dir = "?"
		}
		model := r.Model
		if model == "" {
			model = "–"
		}
		server := truncate(r.ServerDID, 24)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			truncate(r.ReceiptID, 26),
			dir,
			r.ServedAt,
			r.Verb,
			model,
			r.InputTokens,
			r.OutputTokens,
			r.DurationMS,
			server,
		)
	}
	return w.Flush()
}
