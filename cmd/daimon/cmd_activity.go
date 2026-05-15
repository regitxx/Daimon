package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// cmdActivity routes the `activity` subcommand surface. `query` is the
// human-readable view over the audit trail every other subcommand writes to;
// `verify` walks the chain end-to-end (prev_hash continuity + BLAKE3 hash
// recomputation + Ed25519 signature) and reports pass/fail. Both are thin
// wrappers over their corresponding RPC.
func cmdActivity(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon activity <query|verify> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "query":
		return runActivityQuery(os.Stdout, os.Stderr, rest)
	case "verify":
		return runActivityVerify(os.Stdout, os.Stderr, rest)
	default:
		return fmt.Errorf("daimon activity: unknown subcommand %q", sub)
	}
}

// --- daimon activity query ---------------------------------------------------

// activityEntry mirrors the on-the-wire shape of internal/activity.Entry.
// Re-declared here so cmd/daimon stays a pure client; the wire shape is the
// stable contract per SPEC §8.2 (id/ts/kind plain-text, payload AEAD-decrypted
// by the daemon before responding, prev_hash/hash plain-text for chain walks).
type activityEntry struct {
	ID        string          `json:"id"`
	Timestamp int64           `json:"ts"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	PrevHash  string          `json:"prev_hash"`
	Hash      string          `json:"hash"`
}

// activityQueryWire is the params struct sent to daimon.activity.query. Mirror
// of internal/server.activityQueryParams; see SPEC §6.1. omitempty on every
// field so an unset filter is omitted on the wire and the server's "all
// kinds, no time bound, no limit" defaults apply.
type activityQueryWire struct {
	Since int64  `json:"since,omitempty"`
	Until int64  `json:"until,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// sinceFlag accepts either a Go duration ("1h", "30m") interpreted as a
// relative lower bound from now, or an RFC3339 timestamp interpreted as an
// absolute lower bound. Resolves to a unix-millisecond value at Set time so
// the wire shape is uniform regardless of how the user expressed it.
type sinceFlag struct {
	set    bool
	unixMs int64
	raw    string
}

func (f *sinceFlag) String() string {
	if f == nil {
		return ""
	}
	return f.raw
}

func (f *sinceFlag) Set(s string) error {
	if d, err := time.ParseDuration(s); err == nil {
		f.set = true
		f.raw = s
		f.unixMs = time.Now().Add(-d).UnixMilli()
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		f.set = true
		f.raw = s
		f.unixMs = t.UnixMilli()
		return nil
	}
	return fmt.Errorf("--since must be a Go duration (e.g. 1h) or RFC3339 timestamp (e.g. 2026-05-05T00:00:00Z); got %q", s)
}

// runActivityQuery is the testable body of `daimon activity query`. The
// stdout/stderr writers are injected so tests can capture rendered output
// without swapping os.Stdout. cmdActivity wires os.Stdout/os.Stderr.
func runActivityQuery(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("daimon activity query", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var since sinceFlag
	fs.Var(&since, "since", "lower bound: Go duration (e.g. 1h) or RFC3339 timestamp")
	var kinds kindsFlag
	fs.Var(&kinds, "kind", "filter by kind (repeatable for OR filter)")
	limit := fs.Int("limit", 50, "maximum number of rows to return (0: unlimited)")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daimon activity query takes no positional arguments")
	}

	params := activityQueryWire{}
	if since.set {
		params.Since = since.unixMs
	}
	switch len(kinds) {
	case 0:
		params.Limit = *limit
	case 1:
		params.Kind = kinds[0]
		params.Limit = *limit
	default:
		// Multi-kind OR filter is applied client-side: leave the wire Kind
		// empty (server filter is single-kind) and unset Limit so client-side
		// filtering sees the full window. We accept the cost of pulling more
		// rows than we render — keeps to one round-trip and avoids polluting
		// the activity.queried log with N entries.
	}

	if *asJSON {
		var raw json.RawMessage
		if err := daemonCall("daimon.activity.query", params, &raw); err != nil {
			return err
		}
		// In the multi-kind case the JSON path still returns the raw server
		// response (no client-side filter); tooling that wants OR-filtering
		// over JSON can repeat with one --kind per pass. Documented in usage.
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return printJSONTo(stdout, v)
	}

	var entries []activityEntry
	if err := daemonCall("daimon.activity.query", params, &entries); err != nil {
		return err
	}
	if len(kinds) > 1 {
		entries = filterEntriesByKinds(entries, kinds, *limit)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stderr, "no activity entries.")
		return nil
	}
	return renderActivityEntries(stdout, entries)
}

// filterEntriesByKinds is the client-side OR filter for multi --kind. Applied
// only after the server's single-kind path is exhausted; the limit is applied
// post-filter so --limit N --kind a --kind b returns N rows of either kind,
// not the first N rows that happen to match.
func filterEntriesByKinds(entries []activityEntry, kinds []string, limit int) []activityEntry {
	want := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	out := make([]activityEntry, 0, len(entries))
	for _, e := range entries {
		if !want[e.Kind] {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// --- daimon activity verify --------------------------------------------------

// activityVerifyResult mirrors internal/server.activityVerifyResult — the wire
// shape returned by daimon.activity.verify on success. The verifier walks the
// whole chain or nothing, so there are no input flags besides --json.
type activityVerifyResult struct {
	Verified int  `json:"verified"`
	OK       bool `json:"ok"`
}

// runActivityVerify is the testable body of `daimon activity verify`. Returns
// nil on success (chain ok), an error on failure (chain corrupt / signature
// failure / AEAD authentication failure / daemon locked / not running). The
// non-nil error path drives `daimon activity verify`'s exit code 1, which lets
// `daimon activity verify && deploy` work as a pre-flight check in scripts.
func runActivityVerify(stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("daimon activity verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daimon activity verify takes no positional arguments")
	}

	var res activityVerifyResult
	if err := daemonCall("daimon.activity.verify", struct{}{}, &res); err != nil {
		// Server-side failures (chain broken / signature failed / AEAD) arrive
		// here as a CodeInternalError carrying the activity error text. In JSON
		// mode emit a structured failure envelope on stdout so tooling can
		// switch on ok:false; in human mode let exitOnErr render the reason
		// once via the wrapped error (avoids the stdout/stderr duplicate the
		// pre-print pattern produces).
		if *asJSON {
			_ = printJSONTo(stdout, map[string]any{"ok": false, "error": err.Error()})
		}
		return fmt.Errorf("chain INVALID: %w", err)
	}
	if *asJSON {
		return printJSONTo(stdout, res)
	}
	fmt.Fprintf(stdout, "verified %d entries — chain ok\n", res.Verified)
	return nil
}

// --- rendering ---------------------------------------------------------------

func renderActivityEntries(w io.Writer, entries []activityEntry) error {
	tw := tabPrinter(w)
	fmt.Fprintln(tw, "TIME\tKIND\tID\tSUMMARY")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			formatTimestamp(e.Timestamp), e.Kind, e.ID, summarizeEntry(e))
	}
	return tw.Flush()
}

// summarizeEntry returns a per-kind one-liner derived from the decrypted
// payload. The shapes mirror what internal/server emits at each Append call
// site (handlers.go); changes there should be reflected here. Unrecognised
// kinds return the empty string — the KIND column already names the kind, so
// a redundant "kind=X" line in SUMMARY would be noise.
func summarizeEntry(e activityEntry) string {
	if len(e.Payload) == 0 {
		return ""
	}
	var p map[string]any
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return ""
	}
	switch e.Kind {
	case "provider.invoke":
		matched := 0
		if ids, ok := p["injected_memory_ids"].([]any); ok {
			matched = len(ids)
		}
		return fmt.Sprintf("%s/%s streamed=%t matched=%d",
			stringField(p, "provider"),
			stringField(p, "model"),
			boolField(p, "streamed"),
			matched,
		)
	case "memory.write":
		out := "id=" + stringField(p, "id")
		if k := stringField(p, "kind"); k != "" {
			out += " kind=" + k
		}
		return out
	case "memory.export":
		return fmt.Sprintf("memories=%d", intField(p, "memories"))
	case "memory.import":
		return fmt.Sprintf("imported=%d skipped=%d", intField(p, "imported"), intField(p, "skipped"))
	case "activity.queried":
		return fmt.Sprintf("matched=%d", intField(p, "matched"))
	case "activity.verified":
		return fmt.Sprintf("verified=%d", intField(p, "verified"))
	case "context.previewed":
		return fmt.Sprintf("query=%q matched=%d", stringField(p, "query"), intField(p, "matched"))
	case "daimon.created":
		return "did=" + stringField(p, "did")
	case "wallet.created":
		return fmt.Sprintf("%s %s", stringField(p, "chain"), stringField(p, "address"))
	default:
		return ""
	}
}

// payload-field coercers: json.Unmarshal into map[string]any decodes numbers
// as float64 and booleans as bool. These helpers tolerate missing keys (zero
// value) so summaries don't blow up on a partial payload.

func stringField(p map[string]any, k string) string {
	if v, ok := p[k].(string); ok {
		return v
	}
	return ""
}

func boolField(p map[string]any, k string) bool {
	if v, ok := p[k].(bool); ok {
		return v
	}
	return false
}

func intField(p map[string]any, k string) int {
	switch v := p[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

// printJSONTo is the writer-injectable variant of printJSON. Used by
// runActivityQuery so tests can capture --json output without swapping
// os.Stdout. printJSON's behaviour (2-space indent, encode v) is preserved.
func printJSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
