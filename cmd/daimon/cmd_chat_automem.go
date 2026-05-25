package main

// Auto-memory extraction for `daimon chat`.
//
// After each successful chat turn (REPL mode by default; `--once` opt-in via
// flag), the daimon CLI runs ONE extra provider.invoke call against the same
// provider with a small "extract autobiographical facts the user just
// revealed" prompt. Any returned facts are written to memory store
// automatically — no more `daimon memory write` ritual to seed the killer
// demo.
//
// Cost: ~1 extra LLM call per chat turn, ~200 input tokens of meta-prompt
// + the exchange + a tight max_tokens=200 output cap. Roughly doubles the
// per-turn cost, hence the `--no-auto-memory` opt-out. The extraction call
// SKIPS inject_context (no recursive recall) and SKIPS persistence to the
// chat session file (this conversation is bookkeeping, not part of the
// user's narrative thread).
//
// Surfaced after the dogfood session 2026-05-25 — user's exact complaint:
// "why do I have to write everything by myself?"

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// autoMemoryExtractPrompt is the system prompt for the extraction call. Kept
// short on purpose — every extra token here is paid on every chat turn.
//
// Design notes on what's IN and what's NOT:
//   - IN: persistent autobiographical facts (name, role, location, ongoing
//     projects, stated preferences, relationships).
//   - OUT: questions the user asked, things the assistant said, transient
//     state ("I'm at the airport right now"), inferences not directly stated.
//   - OUT: ANY fact that's already a duplicate of something the daimon
//     told the LLM via inject_context — but the extraction prompt doesn't
//     know what was injected, so dedup happens server-side in the long run.
//     For v1, we accept duplicate writes and rely on `daimon memory list`
//     review to clean up.
//
// The output JSON contract is intentionally narrow: an array of objects with
// only `kind` and `content`. No source citation, no confidence score. Adding
// fields later is back-compat; subtracting is not.
const autoMemoryExtractPrompt = `You are an observer that extracts persistent autobiographical facts from a single chat exchange.

Look at the EXCHANGE below and list facts the user revealed about themselves — name, role, location, ongoing projects, preferences, opinions, relationships. Skip:
  - Questions the user asked (not facts about them)
  - Things the assistant said (only what the USER revealed)
  - Transient state ("I'm at the airport right now")
  - Inferences not directly stated by the user

Output ONLY a JSON array of {"kind", "content"}. kind ∈ {"fact","preference","observation"}. Empty array [] if nothing new.

Examples:
  USER: "I'm vegan and I work as a designer in Berlin"
  → [{"kind":"fact","content":"I'm vegan"},{"kind":"fact","content":"I work as a designer"},{"kind":"fact","content":"I live in Berlin"}]
  USER: "What's the weather like?"
  → []
  USER: "I prefer concise explanations" / ASSISTANT: "Got it"
  → [{"kind":"preference","content":"I prefer concise explanations"}]`

// proposedFact mirrors the shape the extraction model emits. Kept private —
// it's never exposed on the wire.
type proposedFact struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// runAutoMemoryExtraction invokes the provider with a meta-prompt that asks
// it to extract persistent facts from the just-completed exchange, then
// writes each fact to the memory store. Prints a single-line summary to
// stderr after the writes complete.
//
// Errors are NEVER fatal: if extraction fails (provider error, malformed
// JSON, write failure), we log a quiet stderr note and continue. The user's
// chat turn already succeeded; auto-memory is a best-effort bonus.
func runAutoMemoryExtraction(cfg chatConfig, userMsg, assistantMsg string) {
	if !cfg.autoMemory {
		return
	}
	if strings.TrimSpace(userMsg) == "" || strings.TrimSpace(assistantMsg) == "" {
		// Skip degenerate exchanges (empty prompt, error response).
		return
	}

	exchange := fmt.Sprintf("USER: %s\n\nASSISTANT: %s", userMsg, assistantMsg)

	params := providerInvokeParams{
		Provider: cfg.provider,
		Request: providerRequest{
			Model:  cfg.model,
			System: autoMemoryExtractPrompt,
			Messages: []providerMessage{
				{Role: "user", Content: exchange},
			},
			MaxTokens: 300,
		},
		// Crucially: no InjectContext. Avoids recursive recall.
	}
	var env providerInvokeResult
	if err := daemonCall("daimon.provider.invoke", params, &env); err != nil {
		fmt.Fprintf(os.Stderr, "[auto-memory: extraction skipped — %v]\n", err)
		return
	}
	if env.Response == nil {
		return
	}

	facts := parseProposedFacts(env.Response.Content)
	if len(facts) == 0 {
		// Silent — most turns won't produce new facts and printing
		// "[auto-memory: 0 new]" every turn would be visual noise.
		return
	}

	written := writeProposedFacts(facts)
	if written == 0 {
		return
	}
	if written == 1 {
		fmt.Fprintf(os.Stderr, "[auto-memory: learned 1 new fact about you]\n")
	} else {
		fmt.Fprintf(os.Stderr, "[auto-memory: learned %d new facts about you]\n", written)
	}
	// One-liner preview so the user knows roughly what was captured. Capped
	// at 3 lines so a chatty extraction doesn't overflow the terminal.
	for i, f := range facts {
		if i >= 3 {
			fmt.Fprintf(os.Stderr, "  (+%d more — `daimon memory list` to inspect)\n", len(facts)-3)
			break
		}
		fmt.Fprintf(os.Stderr, "  • (%s) %s\n", f.Kind, truncate(f.Content, 80))
	}
}

// parseProposedFacts pulls the JSON array out of the model's response.
// Models sometimes wrap JSON in code fences or prose; we tolerate both by
// scanning for the first `[` and last `]` and parsing the substring.
//
// Any parse failure → empty result. The user's primary chat turn already
// succeeded; we won't surface auto-memory errors loudly.
func parseProposedFacts(raw string) []proposedFact {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end < 0 || end <= start {
		return nil
	}
	candidate := raw[start : end+1]
	var facts []proposedFact
	if err := json.Unmarshal([]byte(candidate), &facts); err != nil {
		return nil
	}
	// Sanity-filter: drop entries with empty content or unknown kind.
	// Valid kinds match memory.Kind from internal/memory.
	valid := facts[:0]
	for _, f := range facts {
		if strings.TrimSpace(f.Content) == "" {
			continue
		}
		switch f.Kind {
		case "fact", "preference", "observation", "task":
			valid = append(valid, f)
		default:
			// Coerce unknown kinds to observation rather than dropping
			// outright — the content might still be useful and "observation"
			// is the most generic SPEC §5.2 kind.
			f.Kind = "observation"
			valid = append(valid, f)
		}
	}
	return valid
}

// writeProposedFacts writes each accepted fact via daimon.memory.write and
// returns the count successfully persisted. Failures are tallied but not
// surfaced — see the comment on runAutoMemoryExtraction about best-effort.
func writeProposedFacts(facts []proposedFact) int {
	written := 0
	for _, f := range facts {
		params := map[string]any{
			"kind":    f.Kind,
			"content": f.Content,
			"source":  "auto-memory",
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := daemonCall("daimon.memory.write", params, &out); err != nil {
			continue
		}
		written++
	}
	return written
}

