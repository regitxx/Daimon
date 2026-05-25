package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/daimonhome"
)

// cmdChat is the conversational REPL that wraps daimon.provider.invoke with
// multi-turn history persisted across CLI invocations.
//
// History is JSONL at $DAIMON_HOME/chat-sessions/<name>.jsonl, one line per
// turn. Append-only (matches the activity-log instinct) and grep-friendly.
// `--resume` parses the file line-by-line and threads the prior turns into
// the next provider call.
//
// --inject-context defaults ON for chat (vs OFF for `provider invoke`): chat
// is the conversational case where the user expects "the daimon knows me",
// while invoke is one-shot scripting where the user wants explicit control.
// SPEC §6 auditability is preserved because every injection is logged with
// injected_memory_ids in the activity payload (landed session 8).
//
// Streaming is buffered for v0.1 — the daimon.provider.invoke RPC is request/
// response. A streaming variant lands in v0.1.x; the demo video can fake the
// effect at render time if needed.
func cmdChat(args []string) error {
	fs := flag.NewFlagSet("daimon chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	providerName := fs.String("provider", "", "provider name (required)")
	model := fs.String("model", "", "model id (empty: provider default)")
	system := fs.String("system", "", "system prompt prepended to every turn")
	tempStr := fs.String("temperature", "", "sampling temperature (empty: provider default)")
	maxTokens := fs.Int("max-tokens", 0, "maximum output tokens per turn (0: provider default)")
	name := fs.String("name", "current", "session name; history is $DAIMON_HOME/chat-sessions/<name>.jsonl")
	once := fs.String("once", "", "one-shot mode: send <prompt|-> as a single turn, print response, exit")
	noInject := fs.Bool("no-inject-context", false, "disable SPEC §11 memory retrieval (default: enabled)")
	injectQuery := fs.String("inject-query", "", "explicit retrieval query (default: each user prompt)")
	asJSON := fs.Bool("json", false, "emit each response envelope as JSON (one-shot only; ignored in REPL)")
	// Tri-state flag: unset → mode-specific default (REPL on, --once off);
	// explicit --stream / --stream=false honours the user's call. Implemented
	// via a custom Var so we can detect "user did not pass it".
	stream := newStreamFlag()
	fs.Var(stream, "stream", "stream tokens via daimon.provider.stream (default: on for REPL, off for --once)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *providerName == "" {
		return fmt.Errorf("--provider is required")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daimon chat takes no positional arguments (use --once for one-shot)")
	}
	if !validSessionName(*name) {
		return fmt.Errorf("--name must be alphanumeric with - or _ (got %q)", *name)
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}
	sessionsDir := filepath.Join(home, "chat-sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return fmt.Errorf("create chat-sessions dir: %w", err)
	}
	sessionPath := filepath.Join(sessionsDir, *name+".jsonl")

	// History always loads from the named session file. The demo-video story
	// — switch Claude → OpenAI → Ollama with memory intact — depends on this
	// being implicit, not opt-in: forgetting a flag must not silently drop
	// the conversation. To start fresh, pass a different --name (or rm the
	// session file).
	history, err := loadChatSession(sessionPath)
	if err != nil {
		return err
	}

	var temperature *float64
	if *tempStr != "" {
		var t float64
		if _, err := fmt.Sscanf(*tempStr, "%f", &t); err != nil {
			return fmt.Errorf("--temperature must be a number: %w", err)
		}
		temperature = &t
	}
	cfg := chatConfig{
		provider:    *providerName,
		model:       *model,
		system:      *system,
		temperature: temperature,
		maxTokens:   *maxTokens,
		inject:      !*noInject,
		injectQuery: *injectQuery,
		stream:      stream.resolve(*once == ""),
	}

	f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	if *once != "" {
		prompt, err := readContent(*once)
		if err != nil {
			return err
		}
		if strings.TrimSpace(prompt) == "" {
			return fmt.Errorf("prompt is required (use - to read from stdin)")
		}
		return runChatTurnOnce(f, &history, cfg, prompt, *asJSON)
	}

	return runChatREPL(f, &history, cfg, sessionPath)
}

// --- types -------------------------------------------------------------------

// chatTurn is one persisted JSONL line. Provider/Model are populated only on
// assistant turns so a transcript that switched providers mid-conversation
// renders honestly on resume (e.g. "[claude]" then "[openai]" then "[ollama]").
type chatTurn struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	TS       int64  `json:"ts"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// chatConfig is the per-invocation knob set carried through every turn.
type chatConfig struct {
	provider    string
	model       string
	system      string
	temperature *float64
	maxTokens   int
	inject      bool
	injectQuery string
	stream      bool
}

// streamFlag is a tri-state stdlib-flag value: when the user does not pass
// --stream, resolve() falls back to the mode-specific default (REPL on,
// --once off); when they pass --stream / --stream=true / --stream=false the
// explicit value wins. We can't use *bool because flag's BoolVar conflates
// "default false" with "explicitly set to false".
type streamFlag struct {
	set   bool
	value bool
}

func newStreamFlag() *streamFlag { return &streamFlag{} }

func (f *streamFlag) String() string {
	if f == nil {
		return ""
	}
	if f.value {
		return "true"
	}
	return "false"
}

func (f *streamFlag) Set(s string) error {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		f.set, f.value = true, true
	case "false", "0", "no", "off":
		f.set, f.value = true, false
	default:
		return fmt.Errorf("--stream: expected true|false, got %q", s)
	}
	return nil
}

func (f *streamFlag) IsBoolFlag() bool { return true }

// resolve returns the effective value: explicit when set, otherwise the
// supplied per-mode default (true for REPL, false for --once).
func (f *streamFlag) resolve(defaultOn bool) bool {
	if f == nil || !f.set {
		return defaultOn
	}
	return f.value
}

// --- session I/O -------------------------------------------------------------

// validSessionName guards the file path against traversal and nasty characters
// since the name lands in a real filesystem path.
func validSessionName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// loadChatSession reads every JSONL line from path. A missing file is not an
// error — `--resume` against a fresh session name simply starts empty, which
// matches the "name a session and pick it back up later" UX.
func loadChatSession(path string) ([]chatTurn, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var out []chatTurn
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var t chatTurn
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			return nil, fmt.Errorf("parse session line: %w", err)
		}
		out = append(out, t)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	return out, nil
}

// appendChatTurn writes one JSONL line. fsync is not requested — we accept the
// risk that an SIGKILL between RPC return and fsync drops the last turn, since
// the activity log already captures the provider call durably (SPEC §8.2).
func appendChatTurn(w io.Writer, t chatTurn) error {
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal turn: %w", err)
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write turn: %w", err)
	}
	return nil
}

// renderResumedHistory prints the prior conversation to stderr so the user
// has visual context before the REPL prompt drops. Stderr (not stdout) keeps
// it out of any redirect chain — the user is reading, not piping.
func renderResumedHistory(history []chatTurn) {
	if len(history) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "─── resumed history (%d turns) ───\n", len(history))
	for _, t := range history {
		ts := formatTimestamp(t.TS)
		switch t.Role {
		case "user":
			fmt.Fprintf(os.Stderr, "[%s] you: %s\n", ts, t.Content)
		case "assistant":
			tag := t.Provider
			if t.Model != "" {
				tag = t.Provider + "/" + t.Model
			}
			fmt.Fprintf(os.Stderr, "[%s] [%s]: %s\n", ts, tag, t.Content)
		}
	}
	fmt.Fprintln(os.Stderr, "─── end history ───")
}

// --- turn execution ----------------------------------------------------------

// runTurn is the shared body for one user→assistant cycle. Returns the
// assistant turn so callers can decide how to render and where to write it.
//
// Persists user + assistant atomically, only after a successful RPC. Failed
// invocations leave nothing in the chat file — the conversation log is
// always a coherent sequence of (user, assistant) pairs, which keeps
// `--resume`'s messages[] reconstruction honest. Audit visibility for
// failed calls is the activity log's job, not the chat file's.
//
// Streaming flows through runTurnStream below; this function is the
// buffered/unary path used for one-shot scripting and as the fallback when
// the chosen provider does not implement Streamer.
//
// The middle return is the slice of memory IDs the daimon folded into the
// prompt via inject_context (nil/empty when retrieval ran but matched
// nothing, or when inject_context was not set). The REPL's post-RPC
// `[inject_context: query=... matched=N]` line uses this length.
func runTurn(w io.Writer, history *[]chatTurn, cfg chatConfig, prompt string) (*providerResponse, injectInfo, json.RawMessage, error) {
	userTurn, params := buildTurnRequest(*history, cfg, prompt)

	var raw json.RawMessage
	if err := daemonCall("daimon.provider.invoke", params, &raw); err != nil {
		return nil, injectInfo{}, nil, err
	}
	var env providerInvokeResult
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, injectInfo{}, nil, fmt.Errorf("parse response: %w", err)
	}
	if env.Response == nil {
		return nil, injectInfo{}, nil, fmt.Errorf("daemon returned envelope with no response")
	}

	if err := persistTurnPair(w, history, userTurn, env.Response, cfg.provider); err != nil {
		return nil, injectInfo{}, nil, err
	}
	return env.Response, injectInfo{ids: env.InjectedMemoryIDs, context: env.InjectedContext}, raw, nil
}

// runTurnStream mirrors runTurn but invokes daimon.provider.stream and calls
// onDelta for each token-fragment as it arrives. The terminal envelope is
// the same shape Invoke returns since session 24 — providerInvokeResult with
// the optional injected_memory_ids field. Persistence rules match runTurn —
// only on success, both turns committed atomically.
//
// On a "provider does not support streaming" error (CodeNotFound + the
// canonical message), runTurnStream surfaces a sentinel that the caller can
// translate into a transparent fallback to runTurn — the locked decision
// puts the fallback choice in the CLI, not the server.
func runTurnStream(w io.Writer, history *[]chatTurn, cfg chatConfig, prompt string, onDelta func(string)) (*providerResponse, injectInfo, error) {
	userTurn, params := buildTurnRequest(*history, cfg, prompt)

	var env providerInvokeResult
	if err := daemonStream("daimon.provider.stream", params, onDelta, &env); err != nil {
		return nil, injectInfo{}, err
	}
	if env.Response == nil {
		return nil, injectInfo{}, fmt.Errorf("daemon returned envelope with no response")
	}
	if err := persistTurnPair(w, history, userTurn, env.Response, cfg.provider); err != nil {
		return nil, injectInfo{}, err
	}
	return env.Response, injectInfo{ids: env.InjectedMemoryIDs, context: env.InjectedContext}, nil
}

// injectInfo bundles the two outputs of inject_context retrieval that the
// REPL surfaces to the user after a successful turn. Kept as a small struct
// instead of separate return params so the REPL plumbing doesn't need to
// thread two values everywhere.
type injectInfo struct {
	ids     []string
	context string // the rendered "[i] (kind) content" block, or "" when nothing matched
}

// buildTurnRequest produces the user turn and the provider invoke params for
// one cycle. Shared by streaming and unary paths so the wire shape is
// identical — the only difference between the two is the RPC method name.
func buildTurnRequest(history []chatTurn, cfg chatConfig, prompt string) (chatTurn, providerInvokeParams) {
	userTurn := chatTurn{
		Role:    "user",
		Content: prompt,
		TS:      time.Now().UnixMilli(),
	}
	msgs := make([]providerMessage, 0, len(history)+1)
	for _, t := range history {
		msgs = append(msgs, providerMessage{Role: t.Role, Content: t.Content})
	}
	msgs = append(msgs, providerMessage{Role: userTurn.Role, Content: userTurn.Content})

	req := providerRequest{Model: cfg.model, Messages: msgs, System: cfg.system}
	if cfg.maxTokens > 0 {
		req.MaxTokens = cfg.maxTokens
	}
	if cfg.temperature != nil {
		req.Temperature = cfg.temperature
	}
	params := providerInvokeParams{Provider: cfg.provider, Request: req}
	if cfg.inject {
		q := cfg.injectQuery
		if q == "" {
			q = prompt
		}
		params.InjectContext = &injectContextWire{Query: q}
	}
	return userTurn, params
}

// persistTurnPair writes the user + assistant turns to the session JSONL and
// appends them to the in-memory history slice. Atomic from the caller's
// perspective — either both lines land or neither (the session file is
// append-only, so a partial write would only ever truncate the assistant
// line, which loadChatSession's per-line parser handles by returning the
// truncated line as a parse error on next load).
func persistTurnPair(w io.Writer, history *[]chatTurn, userTurn chatTurn, resp *providerResponse, providerName string) error {
	astTurn := chatTurn{
		Role:     "assistant",
		Content:  resp.Content,
		TS:       time.Now().UnixMilli(),
		Provider: providerName,
		Model:    resp.Model,
	}
	if err := appendChatTurn(w, userTurn); err != nil {
		return err
	}
	if err := appendChatTurn(w, astTurn); err != nil {
		return err
	}
	*history = append(*history, userTurn, astTurn)
	return nil
}

// isStreamUnsupported reports whether err is the daemon's "provider does not
// support streaming" rejection — used by the CLI fallback to retry against
// daimon.provider.invoke transparently. We match on the message text rather
// than the code (which is shared with "unknown provider") because the
// fallback is only safe for the streaming-unsupported case.
func isStreamUnsupported(err error) bool {
	rpc, ok := asRPCError(err)
	if !ok {
		return false
	}
	return rpc.Code == codeNotFound && strings.Contains(rpc.Message, "does not support streaming")
}

// --- one-shot mode -----------------------------------------------------------

// runChatTurnOnce sends one turn and exits. Default output is just the
// assistant content on stdout — composes with `daimon chat --once "..." | tee`
// and `... > out.txt`. --json emits the full envelope. Inject-context is
// signalled to stderr only when --json is off (otherwise we'd corrupt the JSON
// on stdout consumers... actually stderr is separate, but keeping it quiet in
// --json mode matches `provider invoke --verbose` discipline).
//
// When cfg.stream is on (user passed --stream to a --once invocation),
// tokens print as they arrive instead of one buffered Println. --json
// disables streaming because the JSON envelope only exists at terminal time.
//
// announceInject fires AFTER the RPC succeeds so the printed "matched=N" can
// reflect what actually came back from the daemon. Failure paths print no
// announce line — the RPC error message itself tells the story, and silence
// on failure is strictly less stderr noise than the pre-session-24 design.
func runChatTurnOnce(w io.Writer, history *[]chatTurn, cfg chatConfig, prompt string, asJSON bool) error {
	if cfg.stream && !asJSON {
		resp, injected, err := runTurnStreamWithFallback(w, history, cfg, prompt, func(chunk string) {
			fmt.Print(chunk)
		})
		if err != nil {
			fmt.Println()
			return err
		}
		_ = resp
		fmt.Println()
		if cfg.inject {
			announceInject(cfg, prompt, injected)
		}
		return nil
	}
	resp, injected, raw, err := runTurn(w, history, cfg, prompt)
	if err != nil {
		return err
	}
	if asJSON {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return printJSON(v)
	}
	fmt.Println(resp.Content)
	if cfg.inject {
		announceInject(cfg, prompt, injected)
	}
	return nil
}

// runTurnStreamWithFallback wraps runTurnStream with the locked CLI-side
// behaviour: if the daemon reports the chosen provider does not support
// streaming, drop to runTurn transparently after a one-line stderr note.
// Buffered output happens after the announcement so the user sees the same
// content shape in both modes.
func runTurnStreamWithFallback(w io.Writer, history *[]chatTurn, cfg chatConfig, prompt string, onDelta func(string)) (*providerResponse, injectInfo, error) {
	resp, injected, err := runTurnStream(w, history, cfg, prompt, onDelta)
	if err == nil {
		return resp, injected, nil
	}
	if !isStreamUnsupported(err) {
		return nil, injectInfo{}, err
	}
	fmt.Fprintf(os.Stderr, "[stream: %s does not support streaming, falling back to invoke]\n", cfg.provider)
	r, injected, _, err := runTurn(w, history, cfg, prompt)
	if err != nil {
		return nil, injectInfo{}, err
	}
	// Replay the buffered content through onDelta so the caller's stdout
	// rendering still produces the assistant turn.
	onDelta(r.Content)
	return r, injected, nil
}

// --- REPL --------------------------------------------------------------------

// runChatREPL drives the interactive loop. Each turn: read a line, send it,
// print the response prefixed with the active provider tag. Slash commands
// (/help, /exit, /quit) are handled before the line goes on the wire.
//
// Multi-line input is not supported in v0.1 — pipe through `daimon chat
// --once -` for that. Editing/history is not supported either; users who
// want it can `rlwrap daimon chat ...`.
func runChatREPL(w io.Writer, history *[]chatTurn, cfg chatConfig, sessionPath string) error {
	// Auto-render prior history when the named session has any: the user is
	// continuing a conversation, the past should be visible.
	renderResumedHistory(*history)
	fmt.Fprintf(os.Stderr, "daimon chat — provider=%s session=%s\n", cfg.provider, filepath.Base(sessionPath))
	if cfg.inject {
		fmt.Fprintln(os.Stderr, "inject_context: on (memory retrieval before each call) — pass --no-inject-context to disable")
	} else {
		fmt.Fprintln(os.Stderr, "inject_context: off")
	}
	if cfg.stream {
		fmt.Fprintln(os.Stderr, "stream: on (token-by-token rendering) — pass --stream=false to disable")
	} else {
		fmt.Fprintln(os.Stderr, "stream: off")
	}
	fmt.Fprintln(os.Stderr, "Ctrl+D to exit, /help for commands.")

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for {
		fmt.Fprint(os.Stderr, "you> ")
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr)
			return nil
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			done, err := handleSlash(line, *history)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			if done {
				return nil
			}
			continue
		}
		if cfg.stream {
			// Print the prefix once, then each delta inline; trailing newline
			// closes the assistant line. Errors mid-stream still print a
			// newline so the next "you> " prompt isn't glued to a half-line.
			tagPrefix := fmt.Sprintf("[%s]: ", cfg.provider)
			fmt.Print(tagPrefix)
			resp, injected, err := runTurnStreamWithFallback(w, history, cfg, line, func(chunk string) {
				fmt.Print(chunk)
			})
			fmt.Println()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			// If the model field arrived from the daemon, refine the prefix
			// retrospectively for /history's benefit. (Not retroactively
			// rewriting stdout; the persisted assistant turn already carries
			// the model.)
			_ = resp
			if cfg.inject {
				announceInject(cfg, line, injected)
			}
			continue
		}
		resp, injected, _, err := runTurn(w, history, cfg, line)
		if err != nil {
			// Don't kill the REPL — let the user retry, edit, or /exit.
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		tag := cfg.provider
		if resp.Model != "" {
			tag = cfg.provider + "/" + resp.Model
		}
		fmt.Printf("[%s]: %s\n", tag, resp.Content)
		if cfg.inject {
			announceInject(cfg, line, injected)
		}
	}
}

// handleSlash returns (exit, err). If exit is true the REPL terminates.
func handleSlash(line string, history []chatTurn) (bool, error) {
	switch line {
	case "/exit", "/quit", "/q":
		return true, nil
	case "/help", "/?":
		fmt.Fprintln(os.Stderr, "  /help, /?       this message")
		fmt.Fprintln(os.Stderr, "  /history        replay the current session")
		fmt.Fprintln(os.Stderr, "  /exit, /quit    leave the REPL (Ctrl+D also works)")
		return false, nil
	case "/history":
		renderResumedHistory(history)
		return false, nil
	default:
		return false, fmt.Errorf("unknown command %q (try /help)", line)
	}
}

// announceInject prints the retrieval query, count, and (since 2026-05-25)
// the actual recalled content to stderr after a successful turn — so the
// user can SEE what daimon told the LLM about them, not just a cryptic
// "matched=N" tally. The content comes from the InjectedContext field on
// the provider.invoke envelope; matched=0 still prints (the user asked for
// retrieval, ran it, got nothing; that's a real signal worth surfacing).
//
// Failure paths skip the announce entirely; the RPC error message itself
// describes what went wrong, no need to prepend a query line that suggests
// retrieval succeeded.
//
// Why stderr: the assistant's answer goes to stdout, so `daimon chat ... |
// pbcopy` still copies just the answer. The recall meta is for the human
// in the terminal, not the downstream pipe.
func announceInject(cfg chatConfig, prompt string, info injectInfo) {
	q := cfg.injectQuery
	if q == "" {
		q = prompt
	}
	matched := len(info.ids)
	if matched == 0 {
		fmt.Fprintf(os.Stderr, "[memory: nothing recalled for %q]\n", truncate(q, 80))
		return
	}
	fmt.Fprintf(os.Stderr, "[memory: %d recalled for %q]\n", matched, truncate(q, 80))
	if info.context != "" {
		// runContextRetrieval formats lines as "[i] (kind) content"; indent
		// them for visual separation from the assistant turn above.
		for _, line := range strings.Split(strings.TrimRight(info.context, "\n"), "\n") {
			fmt.Fprintf(os.Stderr, "  %s\n", line)
		}
	}
}
