package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"
)

// printJSON pretty-prints v as JSON to stdout. Used by the --json escape
// hatch — the structured output every subcommand offers for shell pipelines
// that want the full envelope rather than the human-formatted form.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// tabPrinter wraps text/tabwriter for aligned columnar tables. Default cell
// padding of 2 spaces matches the look of `kubectl get` and `gh pr list`.
func tabPrinter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// truncate trims s to n display characters and appends an ellipsis if it
// overflowed. Operates on runes so multi-byte content (emoji, CJK) is sliced
// at character boundaries, not byte boundaries.
func truncate(s string, n int) string {
	if n <= 1 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// formatTimestamp renders a unix-millisecond timestamp as an RFC3339 string in
// the local timezone. Returns "-" for the zero value so empty cells in tables
// don't show as "1970-01-01T00:00:00Z".
func formatTimestamp(unixMs int64) string {
	if unixMs <= 0 {
		return "-"
	}
	return time.UnixMilli(unixMs).Format(time.RFC3339)
}

// readContent resolves the conventional CLI content argument: a literal string
// or "-" meaning "read all of stdin". The empty string is returned verbatim;
// callers should validate non-emptiness if they need to.
func readContent(arg string) (string, error) {
	if arg != "-" {
		return arg, nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(b), nil
}
