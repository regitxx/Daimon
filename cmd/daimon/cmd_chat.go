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
func runTurn(w io.Writer, history *[]chatTurn, cfg chatConfig, prompt string) (*providerResponse, json.RawMessage, error) {
	now := time.Now().UnixMilli()
	userTurn := chatTurn{
		Role:    "user",
		Content: prompt,
		TS:      now,
	}

	// Build messages[] from history + this user turn, but don't commit the
	// turn to history or the file until the RPC succeeds.
	msgs := make([]providerMessage, 0, len(*history)+1)
	for _, t := range *history {
		msgs = append(msgs, providerMessage{Role: t.Role, Content: t.Content})
	}
	msgs = append(msgs, providerMessage{Role: userTurn.Role, Content: userTurn.Content})

	req := providerRequest{
		Model:    cfg.model,
		Messages: msgs,
		System:   cfg.system,
	}
	if cfg.maxTokens > 0 {
		req.MaxTokens = cfg.maxTokens
	}
	if cfg.temperature != nil {
		req.Temperature = cfg.temperature
	}

	params := providerInvokeParams{
		Provider: cfg.provider,
		Request:  req,
	}
	if cfg.inject {
		q := cfg.injectQuery
		if q == "" {
			q = prompt
		}
		params.InjectContext = &injectContextWire{Query: q}
	}

	var raw json.RawMessage
	if err := daemonCall("daimon.provider.invoke", params, &raw); err != nil {
		return nil, nil, err
	}
	var resp providerResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("parse response: %w", err)
	}

	astTurn := chatTurn{
		Role:     "assistant",
		Content:  resp.Content,
		TS:       time.Now().UnixMilli(),
		Provider: cfg.provider,
		Model:    resp.Model,
	}
	if err := appendChatTurn(w, userTurn); err != nil {
		return nil, nil, err
	}
	if err := appendChatTurn(w, astTurn); err != nil {
		return nil, nil, err
	}
	*history = append(*history, userTurn, astTurn)
	return &resp, raw, nil
}

// --- one-shot mode -----------------------------------------------------------

// runChatTurnOnce sends one turn and exits. Default output is just the
// assistant content on stdout — composes with `daimon chat --once "..." | tee`
// and `... > out.txt`. --json emits the full envelope. Inject-context is
// signalled to stderr only when --json is off (otherwise we'd corrupt the JSON
// on stdout consumers... actually stderr is separate, but keeping it quiet in
// --json mode matches `provider invoke --verbose` discipline).
func runChatTurnOnce(w io.Writer, history *[]chatTurn, cfg chatConfig, prompt string, asJSON bool) error {
	if cfg.inject && !asJSON {
		announceInject(cfg, prompt)
	}
	resp, raw, err := runTurn(w, history, cfg, prompt)
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
	return nil
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
		if cfg.inject {
			announceInject(cfg, line)
		}
		resp, _, err := runTurn(w, history, cfg, line)
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

// announceInject prints the retrieval query to stderr so the user can see when
// memory is about to be folded into the prompt. v0.1 doesn't surface the
// matched memory IDs (the activity log captures them under
// injected_memory_ids); a count display would require the server to include
// the IDs in the invoke response, deferred to v0.1.x.
func announceInject(cfg chatConfig, prompt string) {
	q := cfg.injectQuery
	if q == "" {
		q = prompt
	}
	fmt.Fprintf(os.Stderr, "[inject_context: query=%q]\n", truncate(q, 80))
}
