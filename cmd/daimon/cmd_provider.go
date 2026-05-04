package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// cmdProvider routes the `provider` subcommand surface (list, invoke). Both
// are thin wrappers over daimon.provider.* — invoke also threads the SPEC §11
// inject_context flow when the user opts in via --inject-context.
func cmdProvider(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon provider <list|invoke> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return cmdProviderList(rest)
	case "invoke":
		return cmdProviderInvoke(rest)
	default:
		return fmt.Errorf("daimon provider: unknown subcommand %q", sub)
	}
}

// --- daimon provider list ----------------------------------------------------

type providerListEntry struct {
	Name       string          `json:"name"`
	Models     []providerModel `json:"models"`
	Configured bool            `json:"configured"`
}

type providerModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	Context     int    `json:"context,omitempty"`
	MaxOutput   int    `json:"max_output,omitempty"`
}

func cmdProviderList(args []string) error {
	fs := flag.NewFlagSet("daimon provider list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daimon provider list takes no positional arguments")
	}

	var entries []providerListEntry
	if err := daemonCall("daimon.provider.list", nil, &entries); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no providers registered.")
		return nil
	}
	tw := tabPrinter(os.Stdout)
	fmt.Fprintln(tw, "NAME\tCONFIGURED\tMODELS")
	for _, e := range entries {
		ids := make([]string, 0, len(e.Models))
		for _, m := range e.Models {
			ids = append(ids, m.ID)
		}
		modelList := truncate(strings.Join(ids, ", "), 70)
		yes := "no"
		if e.Configured {
			yes = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Name, yes, modelList)
	}
	return tw.Flush()
}

// --- daimon provider invoke --------------------------------------------------

// injectContextFlag is a stdlib-flag value that accepts both bare invocation
// (--inject-context, treat the prompt as the retrieval query) and an explicit
// query (--inject-context=<query>, retrieve against the supplied string).
//
// IsBoolFlag() returning true is the documented convention from glog/klog: it
// tells flag.Parse to call Set("true") when the flag appears bare. We treat
// the literal "true" sentinel as "no explicit query, fall back to the prompt"
// — at the cost of denying users the ability to inject context against the
// literal four-character string "true" without quoting. Acceptable.
type injectContextFlag struct {
	set   bool
	query string
}

func (f *injectContextFlag) String() string {
	if f == nil {
		return ""
	}
	return f.query
}

func (f *injectContextFlag) Set(s string) error {
	f.set = true
	if s == "true" {
		f.query = ""
		return nil
	}
	f.query = s
	return nil
}

func (f *injectContextFlag) IsBoolFlag() bool { return true }

type providerMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type providerRequest struct {
	Model       string            `json:"model"`
	Messages    []providerMessage `json:"messages"`
	System      string            `json:"system,omitempty"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
}

type providerInvokeParams struct {
	Provider      string             `json:"provider"`
	Request       providerRequest    `json:"request"`
	InjectContext *injectContextWire `json:"inject_context,omitempty"`
}

type injectContextWire struct {
	Query     string   `json:"query"`
	MaxTokens int      `json:"max_tokens,omitempty"`
	Kinds     []string `json:"kinds,omitempty"`
}

type providerResponse struct {
	Model      string `json:"model"`
	Content    string `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// providerInvokeResult is the CLI-side mirror of internal/server's same-named
// struct. The wire shape for daimon.provider.invoke (and the terminal frame on
// daimon.provider.stream) is {response: ProviderResponse, injected_memory_ids?: [...]}
// since session 24 — the optional metadata field carries the IDs of memories
// the daimon folded into the prompt via SPEC §11 inject_context, so the chat
// REPL can render "matched=N" without round-tripping the memory store again.
// We re-declare the type rather than importing internal/server because cmd/daimon
// is a pure client.
type providerInvokeResult struct {
	Response          *providerResponse `json:"response"`
	InjectedMemoryIDs []string          `json:"injected_memory_ids,omitempty"`
}

// cmdProviderInvoke calls daimon.provider.invoke and prints the assistant's
// content to stdout. Metadata (model used, stop reason, token counts) goes
// to stderr only when --verbose is set, so the default `daimon provider
// invoke … | jq` and `… > out.txt` flows are clean.
//
// Inject-context is opt-in. Bare --inject-context retrieves against the
// prompt itself (the SPEC §11 common case); --inject-context=<query>
// retrieves against the supplied string. Silent injection on every call
// would be too much magic for v0.1.
func cmdProviderInvoke(args []string) error {
	fs := flag.NewFlagSet("daimon provider invoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	model := fs.String("model", "", "model id to invoke (empty: provider default)")
	system := fs.String("system", "", "system prompt")
	tempStr := fs.String("temperature", "", "sampling temperature (empty: provider default)")
	maxTokens := fs.Int("max-tokens", 0, "maximum output tokens (0: provider default)")
	verbose := fs.Bool("verbose", false, "print model/usage/stop-reason to stderr after the response")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON instead of plain content")
	var inject injectContextFlag
	fs.Var(&inject, "inject-context",
		"opt into SPEC §11 memory retrieval; bare uses the prompt as query, =<q> overrides")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: daimon provider invoke <provider> <prompt|->")
	}
	providerName := fs.Arg(0)
	prompt, err := readContent(fs.Arg(1))
	if err != nil {
		return err
	}
	if prompt == "" {
		return fmt.Errorf("prompt is required (use - to read from stdin)")
	}

	req := providerRequest{
		Model:    *model,
		Messages: []providerMessage{{Role: "user", Content: prompt}},
		System:   *system,
	}
	if *maxTokens > 0 {
		req.MaxTokens = *maxTokens
	}
	if *tempStr != "" {
		var t float64
		if _, err := fmt.Sscanf(*tempStr, "%f", &t); err != nil {
			return fmt.Errorf("--temperature must be a number: %w", err)
		}
		req.Temperature = &t
	}

	params := providerInvokeParams{
		Provider: providerName,
		Request:  req,
	}
	if inject.set {
		q := inject.query
		if q == "" {
			q = prompt
		}
		params.InjectContext = &injectContextWire{Query: q}
	}

	if *asJSON {
		var raw json.RawMessage
		if err := daemonCall("daimon.provider.invoke", params, &raw); err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return printJSON(v)
	}

	var env providerInvokeResult
	if err := daemonCall("daimon.provider.invoke", params, &env); err != nil {
		return err
	}
	if env.Response == nil {
		return fmt.Errorf("daemon returned envelope with no response")
	}
	resp := env.Response
	fmt.Println(resp.Content)
	if *verbose {
		fmt.Fprintf(os.Stderr,
			"\n[model=%s stop=%s in=%d out=%d]\n",
			resp.Model, resp.StopReason, resp.Usage.InputTokens, resp.Usage.OutputTokens)
		if len(env.InjectedMemoryIDs) > 0 {
			fmt.Fprintf(os.Stderr, "[inject_context: matched=%d]\n", len(env.InjectedMemoryIDs))
			for _, id := range env.InjectedMemoryIDs {
				fmt.Fprintf(os.Stderr, "  - %s\n", id)
			}
		}
	}
	return nil
}

