package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// cmdMemory routes the `memory` subcommand surface. Every subcommand is a
// thin wrapper over an existing daimon.memory.* RPC; the CLI's job here is
// flag parsing, stdin/argv plumbing, and human-friendly rendering.
func cmdMemory(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon memory <write|read|list|search|delete|export|import> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "write":
		return cmdMemoryWrite(rest)
	case "read":
		return cmdMemoryRead(rest)
	case "list":
		return cmdMemoryList(rest)
	case "search":
		return cmdMemorySearch(rest)
	case "delete":
		return cmdMemoryDelete(rest)
	case "export":
		return cmdMemoryExport(rest)
	case "import":
		return cmdMemoryImport(rest)
	default:
		return fmt.Errorf("daimon memory: unknown subcommand %q", sub)
	}
}

// --- daimon memory write -----------------------------------------------------

// cmdMemoryWrite calls daimon.memory.write. Content comes from argv or, if
// the positional argument is "-", from stdin (matches cat/sort/jq).
//
// Default output: just the new ID on stdout, so `ID=$(daimon memory write …)`
// works in shell scripts. --json emits the full result envelope.
func cmdMemoryWrite(args []string) error {
	fs := flag.NewFlagSet("daimon memory write", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kind := fs.String("kind", "", "memory kind: fact|preference|observation|task (required)")
	source := fs.String("source", "", "free-form source string (e.g. 'cli', 'imported-from-X')")
	metadata := fs.String("metadata", "", "JSON object literal attached to the row, e.g. '{\"topic\":\"x\"}'")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind == "" {
		return fmt.Errorf("--kind is required (fact|preference|observation|task)")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon memory write --kind <k> [--source <s>] [--metadata <json>] <content|->")
	}
	content, err := readContent(fs.Arg(0))
	if err != nil {
		return err
	}
	if content == "" {
		return fmt.Errorf("content is required (use - to read from stdin)")
	}

	params := map[string]any{
		"kind":    *kind,
		"content": content,
	}
	if *source != "" {
		params["source"] = *source
	}
	if *metadata != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(*metadata), &m); err != nil {
			return fmt.Errorf("--metadata must be a JSON object: %w", err)
		}
		params["metadata"] = m
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := daemonCall("daimon.memory.write", params, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	fmt.Println(result.ID)
	return nil
}

// --- daimon memory read ------------------------------------------------------

// memoryRecord mirrors *memory.Memory just enough to deserialise + render it.
// Reproducing the shape locally keeps cmd/daimon free of the internal/memory
// dependency (and the cgo-free distribution promise that comes with it).
type memoryRecord struct {
	ID        string          `json:"id"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
	Kind      string          `json:"kind"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	Source    string          `json:"source,omitempty"`
	Signature []byte          `json:"signature"`
	// Embedding is intentionally omitted from the CLI's struct — a vector blob
	// is not interesting to a human reader. --json still surfaces the full
	// shape because it deserializes through json.RawMessage at that path.
}

func cmdMemoryRead(args []string) error {
	fs := flag.NewFlagSet("daimon memory read", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon memory read <id>")
	}
	id := fs.Arg(0)

	if *asJSON {
		var raw json.RawMessage
		if err := daemonCall("daimon.memory.read", map[string]any{"id": id}, &raw); err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return printJSON(v)
	}

	var rec memoryRecord
	if err := daemonCall("daimon.memory.read", map[string]any{"id": id}, &rec); err != nil {
		return err
	}
	fmt.Printf("ID:        %s\n", rec.ID)
	fmt.Printf("Kind:      %s\n", rec.Kind)
	fmt.Printf("Created:   %s\n", formatTimestamp(rec.CreatedAt))
	if rec.UpdatedAt != rec.CreatedAt {
		fmt.Printf("Updated:   %s\n", formatTimestamp(rec.UpdatedAt))
	}
	if rec.Source != "" {
		fmt.Printf("Source:    %s\n", rec.Source)
	}
	if len(rec.Metadata) > 0 && string(rec.Metadata) != "null" {
		fmt.Printf("Metadata:  %s\n", string(rec.Metadata))
	}
	fmt.Printf("Content:\n%s\n", rec.Content)
	return nil
}

// --- daimon memory list / search ---------------------------------------------

// scoredRecord is the wire shape of daimon.memory.search: a memory plus a
// similarity score. Same field-omission policy as memoryRecord.
type scoredRecord struct {
	memoryRecord
	Score float64 `json:"score"`
}

// cmdMemoryList is daimon.memory.search with an empty query — surfaces every
// memory the principal owns, recency-tiebroken by the server's ranker.
func cmdMemoryList(args []string) error {
	fs := flag.NewFlagSet("daimon memory list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kind := fs.String("kind", "", "filter by kind: fact|preference|observation|task")
	limit := fs.Int("limit", 50, "maximum number of rows to return")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daimon memory list takes no positional arguments (use 'memory search' for queries)")
	}
	return runMemorySearch("", *kind, *limit, *asJSON, false)
}

// cmdMemorySearch runs a similarity search against a query. Empty query is a
// usage error here (use `memory list` instead) so the two subcommands behave
// distinctly even though they share a backend RPC.
//
// With --inject-preview, the subcommand routes to daimon.context.get instead
// of daimon.memory.search and renders the SPEC §11 retrieval block + matched
// memory IDs + token estimate. This is the dry-run inspection that closes the
// inject_context UX trinity (preview → invoke-with-annotation → audit-via-query
// → integrity-via-verify): tune queries before live provider calls fire.
func cmdMemorySearch(args []string) error {
	fs := flag.NewFlagSet("daimon memory search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var kinds kindsFlag
	fs.Var(&kinds, "kind", "filter by kind: fact|preference|observation|task (repeatable in --inject-preview mode)")
	limit := fs.Int("limit", 10, "maximum number of rows to return (search mode)")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	injectPreview := fs.Bool("inject-preview", false, "render the inject_context view: dry-run what would be folded into a prompt")
	maxTokens := fs.Int("max-tokens", 0, "max_tokens budget for inject_context (default: server-side 2000); only valid with --inject-preview")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		if *injectPreview {
			return fmt.Errorf("usage: daimon memory search --inject-preview [--kind <k> ...] [--max-tokens <n>] [--json] <query>")
		}
		return fmt.Errorf("usage: daimon memory search [--kind <k>] [--limit <n>] [--json] <query>")
	}
	query := fs.Arg(0)

	if *injectPreview {
		// --limit is meaningless under inject_context (the budget is token-
		// based, not row-count). Surface the mismatch only when the user set
		// --limit explicitly — the default 10 is a search-mode artifact and
		// shouldn't trip an error in --inject-preview's path.
		var limitSet bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "limit" {
				limitSet = true
			}
		})
		if limitSet {
			return fmt.Errorf("--limit is meaningless with --inject-preview; use --max-tokens instead")
		}
		return runMemorySearchInjectPreview(os.Stdout, os.Stderr, query, []string(kinds), *maxTokens, *asJSON)
	}

	// Search mode keeps its single-kind contract — multi-kind retrieval is
	// the inject-preview path's domain (the SPEC §11 kindAllowlist). Surface
	// the mismatch as a usage error rather than silently dropping kinds[1:].
	if len(kinds) > 1 {
		return fmt.Errorf("daimon memory search: --kind is single-valued in search mode; use --inject-preview for multi-kind retrieval")
	}
	if *maxTokens != 0 {
		return fmt.Errorf("--max-tokens is only valid with --inject-preview")
	}
	var kind string
	if len(kinds) == 1 {
		kind = kinds[0]
	}
	return runMemorySearch(query, kind, *limit, *asJSON, true)
}

// --- daimon memory search --inject-preview -----------------------------------

// contextGetWire is the params struct sent to daimon.context.get. Mirror of
// internal/server.contextGetParams; SPEC §6.1. omitempty on max_tokens and
// kinds so an unset budget defers to the server's SPEC §11 default (2000) and
// an unset filter retrieves across every kind.
type contextGetWire struct {
	Query     string   `json:"query"`
	MaxTokens int      `json:"max_tokens,omitempty"`
	Kinds     []string `json:"kinds,omitempty"`
}

// contextGetResult mirrors internal/server.contextGetResult — the wire shape
// returned by daimon.context.get. The Context field is the formatted block
// the server would prepend to a provider's system prompt; MemoryIDs is the
// ordered list of memory rows that contributed to the block; TokenEstimate
// is the (estimated) token count of Context, useful for budget tuning.
type contextGetResult struct {
	Context       string   `json:"context"`
	MemoryIDs     []string `json:"memory_ids"`
	TokenEstimate int      `json:"token_estimate"`
}

// runMemorySearchInjectPreview is the testable body of `daimon memory search
// --inject-preview`. The stdout/stderr writers are injected so tests can
// capture rendered output without swapping os.Stdout. cmdMemorySearch wires
// os.Stdout/os.Stderr.
//
// Default human output:
//
//	[inject_preview] query="<q>" matched=<N> tokens≈<estimate>/<budget>
//
//	[1] (fact) ...
//	[2] (preference) ...
//
// Empty match: prints "no memories matched" to stderr, empty stdout, exit 0
// (a search-with-no-hits is not an error). --json emits the raw RPC envelope.
func runMemorySearchInjectPreview(stdout, stderr io.Writer, query string, kinds []string, maxTokens int, asJSON bool) error {
	params := contextGetWire{Query: query}
	if maxTokens > 0 {
		params.MaxTokens = maxTokens
	}
	if len(kinds) > 0 {
		params.Kinds = kinds
	}

	if asJSON {
		var raw json.RawMessage
		if err := daemonCall("daimon.context.get", params, &raw); err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return printJSONTo(stdout, v)
	}

	var res contextGetResult
	if err := daemonCall("daimon.context.get", params, &res); err != nil {
		return err
	}
	if len(res.MemoryIDs) == 0 {
		fmt.Fprintln(stderr, "no memories matched.")
		return nil
	}
	budget := maxTokens
	if budget <= 0 {
		// Mirror SPEC §11 — keeps the displayed denominator honest when the
		// user didn't set --max-tokens and the server applied its default.
		budget = 2000
	}
	fmt.Fprintf(stdout, "[inject_preview] query=%q matched=%d tokens≈%d/%d\n",
		query, len(res.MemoryIDs), res.TokenEstimate, budget)
	if res.Context != "" {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, res.Context)
	}
	return nil
}

// runMemorySearch is the shared implementation. showScore controls whether
// the human renderer includes the SCORE column — meaningless for list
// (empty query), informative for search.
func runMemorySearch(query, kind string, limit int, asJSON, showScore bool) error {
	params := map[string]any{"query": query}
	if kind != "" {
		params["kind"] = kind
	}
	if limit > 0 {
		params["limit"] = limit
	}

	if asJSON {
		var raw json.RawMessage
		if err := daemonCall("daimon.memory.search", params, &raw); err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return printJSON(v)
	}

	var results []scoredRecord
	if err := daemonCall("daimon.memory.search", params, &results); err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no memories.")
		return nil
	}

	tw := tabPrinter(os.Stdout)
	if showScore {
		fmt.Fprintln(tw, "ID\tKIND\tCREATED\tSCORE\tCONTENT")
	} else {
		fmt.Fprintln(tw, "ID\tKIND\tCREATED\tCONTENT")
	}
	for _, r := range results {
		content := truncate(r.Content, 60)
		// Multiline content collapses to single line in tables; the user can
		// `memory read <id>` to see the full body.
		for i, c := range content {
			if c == '\n' || c == '\r' {
				content = content[:i] + "…"
				break
			}
		}
		if showScore {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%.3f\t%s\n",
				r.ID, r.Kind, formatTimestamp(r.CreatedAt), r.Score, content)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				r.ID, r.Kind, formatTimestamp(r.CreatedAt), content)
		}
	}
	return tw.Flush()
}

// --- daimon memory delete ----------------------------------------------------

func cmdMemoryDelete(args []string) error {
	fs := flag.NewFlagSet("daimon memory delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon memory delete <id>")
	}
	id := fs.Arg(0)

	var result struct {
		Deleted bool `json:"deleted"`
	}
	if err := daemonCall("daimon.memory.delete", map[string]any{"id": id}, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	if result.Deleted {
		fmt.Printf("deleted %s\n", id)
	} else {
		fmt.Printf("no such memory: %s\n", id)
	}
	return nil
}

// --- daimon memory export ----------------------------------------------------

// cmdMemoryExport always emits the signed ExportDocument as JSON — the
// document is the only useful representation. --out writes to a file
// (atomic-ish: O_CREATE|O_TRUNC at mode 0600) instead of stdout. The file
// permission matches the keystore's 0600 since the export is principal-
// confidential by construction.
func cmdMemoryExport(args []string) error {
	fs := flag.NewFlagSet("daimon memory export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "", "write the signed export to this file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daimon memory export takes no positional arguments")
	}

	var doc json.RawMessage
	if err := daemonCall("daimon.memory.export", nil, &doc); err != nil {
		return err
	}

	var sink io.Writer = os.Stdout
	if *out != "" {
		f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("open %s: %w", *out, err)
		}
		defer f.Close()
		sink = f
	}
	enc := json.NewEncoder(sink)
	enc.SetIndent("", "  ")
	return enc.Encode(json.RawMessage(doc))
}

// --- daimon memory import ----------------------------------------------------

func cmdMemoryImport(args []string) error {
	fs := flag.NewFlagSet("daimon memory import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	noVerify := fs.Bool("no-verify", false, "skip signature verification (unsafe)")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon memory import [--no-verify] <path|->")
	}

	var raw []byte
	var err error
	if path := fs.Arg(0); path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return fmt.Errorf("read import source: %w", err)
	}
	var doc json.RawMessage = raw

	params := map[string]any{"document": doc}
	if *noVerify {
		// SPEC §6.1 default is verify_signature=true; only send the override
		// when the user has explicitly opted out.
		params["verify_signature"] = false
	}

	var result struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	if err := daemonCall("daimon.memory.import", params, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	fmt.Printf("imported %d, skipped %d\n", result.Imported, result.Skipped)
	return nil
}
