package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/daimonhome"
	"github.com/regitxx/Daimon/internal/provider/lmstudio"
	"github.com/regitxx/Daimon/internal/provider/ollama"
)

// cmdDoctor reports the daimon's environment health: build info, $DAIMON_HOME
// layout (keystore present? memory store + activity log on disk?), the daemon
// state (not-running / locked / unlocked), API-key presence (presence only,
// never the value), and reachability of the local LM Studio + Ollama
// runtimes. Pure read-only — never auto-spawns daimond, never mutates state,
// safe to run at any moment.
//
// The intent is to formalise the session-start probe ("is anything live? what
// would unblock?") that has been hand-rolled at every kickoff since session 19,
// into a single subcommand both humans and tooling can shell out to. JSON
// output (--json) makes it scriptable.
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("daimon doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the full report as JSON instead of human-formatted text")
	timeoutStr := fs.String("timeout", "1500ms", "per-probe HTTP/dial timeout (Go duration)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("daimon doctor takes no positional arguments")
	}
	timeout, err := time.ParseDuration(*timeoutStr)
	if err != nil {
		return fmt.Errorf("--timeout: %w", err)
	}

	cfg := newDoctorConfig(timeout)
	rep := gatherDoctorReport(context.Background(), cfg)
	if *asJSON {
		return printJSON(rep)
	}
	return renderDoctorText(os.Stdout, rep)
}

// --- report shape ------------------------------------------------------------

type doctorReport struct {
	Build    doctorBuild    `json:"build"`
	Home     doctorHome     `json:"home"`
	Daemon   doctorDaemon   `json:"daemon"`
	Env      doctorEnv      `json:"env"`
	Runtimes doctorRuntimes `json:"runtimes"`
}

type doctorBuild struct {
	Version string `json:"version"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

type doctorHome struct {
	Resolved       string       `json:"resolved"`
	ResolveError   string       `json:"resolve_error,omitempty"`
	SourceFromEnv  bool         `json:"source_from_env"`
	Socket         string       `json:"socket,omitempty"`
	SocketFallback bool         `json:"socket_fallback,omitempty"`
	Keystore       fileStat     `json:"keystore"`
	MemoryDB       fileStat     `json:"memory_db"`
	ActivityLog    activityStat `json:"activity_log"`
}

type fileStat struct {
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type activityStat struct {
	fileStat
	Entries     int    `json:"entries,omitempty"`
	LastHash    string `json:"last_hash,omitempty"`
	ScanError   string `json:"scan_error,omitempty"`
}

type doctorDaemon struct {
	State    string `json:"state"` // not_running | locked | unlocked | error
	DID      string `json:"did,omitempty"`
	DialError string `json:"dial_error,omitempty"`
}

type doctorEnv struct {
	AnthropicAPIKey bool `json:"anthropic_api_key_set"`
	OpenAIAPIKey    bool `json:"openai_api_key_set"`
	LMStudioAPIKey  bool `json:"lmstudio_api_key_set"`
}

type doctorRuntimes struct {
	Ollama   runtimeStat `json:"ollama"`
	LMStudio runtimeStat `json:"lmstudio"`
}

type runtimeStat struct {
	Endpoint  string   `json:"endpoint"`
	Reachable bool     `json:"reachable"`
	Models    []string `json:"models,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// --- config (injectable so tests can swap endpoints / socket paths) ---------

type doctorConfig struct {
	// HomeOverride lets a test point doctor at a temp $DAIMON_HOME without
	// having to mutate the process env. Empty: use daimonhome.Resolve().
	HomeOverride string

	// SocketOverride forces the daemon-probe socket path. Empty: derive from
	// the resolved home via daimonhome.SocketPath.
	SocketOverride string

	// OllamaEndpoint / LMStudioEndpoint default to the runtime DefaultEndpoints
	// + the runtime's HOST env var; tests inject httptest URLs.
	OllamaEndpoint   string
	LMStudioEndpoint string

	// HTTPClient is used for the runtime probes; tests can swap a client with
	// a controlled transport. nil → a fresh client with Timeout set.
	HTTPClient *http.Client

	Timeout time.Duration
}

func newDoctorConfig(timeout time.Duration) doctorConfig {
	cfg := doctorConfig{
		Timeout:          timeout,
		OllamaEndpoint:   ollama.DefaultEndpoint,
		LMStudioEndpoint: lmstudio.DefaultEndpoint,
	}
	if v := os.Getenv("OLLAMA_HOST"); v != "" {
		cfg.OllamaEndpoint = v
	}
	if v := os.Getenv("LMSTUDIO_HOST"); v != "" {
		cfg.LMStudioEndpoint = v
	}
	cfg.HTTPClient = &http.Client{Timeout: timeout}
	return cfg
}

// --- gatherDoctorReport -----------------------------------------------------

// gatherDoctorReport collects every probe in parallel-friendly but currently
// sequential order (the slowest leg is the runtime probes at <=2× timeout).
// Errors during gathering are captured in the report rather than returned —
// the whole point of doctor is to surface broken state, not abort on it.
func gatherDoctorReport(ctx context.Context, cfg doctorConfig) doctorReport {
	rep := doctorReport{
		Build: doctorBuild{
			Version: version,
			Go:      runtime.Version(),
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
		},
		Env: doctorEnv{
			AnthropicAPIKey: envKeyPresent("ANTHROPIC_API_KEY"),
			OpenAIAPIKey:    envKeyPresent("OPENAI_API_KEY"),
			LMStudioAPIKey:  envKeyPresent("LMSTUDIO_API_KEY"),
		},
	}

	// $DAIMON_HOME resolution + on-disk file stats.
	rep.Home = gatherHomeReport(cfg)

	// Daemon dial: only meaningful when we resolved a socket path.
	if rep.Home.Socket != "" {
		rep.Daemon = probeDaemon(rep.Home.Socket, cfg.Timeout)
	} else {
		rep.Daemon = doctorDaemon{State: "error", DialError: "no socket path resolved"}
	}

	// Runtime probes — independent of the daemon, run regardless.
	rep.Runtimes.Ollama = probeOllama(ctx, cfg)
	rep.Runtimes.LMStudio = probeLMStudio(ctx, cfg)

	return rep
}

func gatherHomeReport(cfg doctorConfig) doctorHome {
	home := cfg.HomeOverride
	sourceFromEnv := os.Getenv(daimonhome.EnvVar) != ""
	if home == "" {
		resolved, err := daimonhome.Resolve()
		if err != nil {
			return doctorHome{ResolveError: err.Error(), SourceFromEnv: sourceFromEnv}
		}
		home = resolved
	}
	out := doctorHome{
		Resolved:      home,
		SourceFromEnv: sourceFromEnv,
	}

	socket := cfg.SocketOverride
	if socket == "" {
		s, fb, err := daimonhome.SocketPath(home)
		if err != nil {
			out.Socket = ""
			out.ResolveError = "socket path: " + err.Error()
		} else {
			socket = s
			out.SocketFallback = fb
		}
	}
	out.Socket = socket

	out.Keystore = statFile(daimonhome.KeystorePath(home))
	out.MemoryDB = statFile(filepath.Join(home, "memory.db"))

	logPath := filepath.Join(home, "activity.log")
	out.ActivityLog.fileStat = statFile(logPath)
	if out.ActivityLog.Present {
		hash, count, err := activity.ScanLastHash(logPath)
		if err != nil {
			out.ActivityLog.ScanError = err.Error()
		} else {
			out.ActivityLog.Entries = count
			out.ActivityLog.LastHash = hash
		}
	}
	return out
}

func statFile(path string) fileStat {
	info, err := os.Stat(path)
	if err != nil {
		return fileStat{Present: false, Path: path}
	}
	return fileStat{
		Present: true,
		Path:    path,
		Size:    info.Size(),
		Mode:    info.Mode().Perm().String(),
	}
}

// --- daemon probe -----------------------------------------------------------

// isDaemonAbsent reports whether a Unix-socket dial error means "no daemon is
// listening on the other end" — ECONNREFUSED for a stale socket file, ENOENT
// for an absent file. Mirrors spawn.go's isSpawnableMiss including the
// *os.PathError fallback some platforms surface for missing socket nodes.
func isDaemonAbsent(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, syscall.ENOENT) {
		return true
	}
	return false
}

// probeDaemon dials the socket, sends daimon.identity.get, and classifies the
// outcome into one of {not_running, locked, unlocked, error}. Never spawns —
// doctor is read-only. Uses a fresh socket dial (not daemonCall's helper)
// because we need to distinguish CodeIdentityLocked from other RPC errors
// without humanising into prose.
func probeDaemon(socket string, timeout time.Duration) doctorDaemon {
	conn, err := net.DialTimeout("unix", socket, timeout)
	if err != nil {
		if isDaemonAbsent(err) {
			return doctorDaemon{State: "not_running"}
		}
		return doctorDaemon{State: "error", DialError: err.Error()}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if err := json.NewEncoder(conn).Encode(jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "daimon.identity.get",
		ID:      1,
	}); err != nil {
		return doctorDaemon{State: "error", DialError: "encode: " + err.Error()}
	}
	var resp jsonrpcResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return doctorDaemon{State: "error", DialError: "decode: " + err.Error()}
	}
	if resp.Error != nil {
		if resp.Error.Code == codeIdentityLocked {
			return doctorDaemon{State: "locked"}
		}
		return doctorDaemon{State: "error", DialError: resp.Error.Error()}
	}
	var got struct {
		DID string `json:"did"`
	}
	if len(resp.Result) > 0 {
		_ = json.Unmarshal(resp.Result, &got)
	}
	return doctorDaemon{State: "unlocked", DID: got.DID}
}

// --- runtime probes ---------------------------------------------------------

// probeOllama hits /api/tags and counts models. The daimon Ollama adapter
// itself probes the same endpoint at registration time; doctor mirrors the
// adapter's reachability check without taking a dependency on it (the adapter
// returns a richer error type but the user-facing doctor wants a simple
// "reachable + N models" line).
func probeOllama(ctx context.Context, cfg doctorConfig) runtimeStat {
	out := runtimeStat{Endpoint: cfg.OllamaEndpoint}
	body, err := httpProbe(ctx, cfg.HTTPClient, cfg.OllamaEndpoint+"/api/tags", cfg.Timeout)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		out.Reachable = true
		out.Error = "parse /api/tags: " + err.Error()
		return out
	}
	out.Reachable = true
	for _, m := range parsed.Models {
		out.Models = append(out.Models, m.Name)
	}
	return out
}

// probeLMStudio hits /v1/models (OpenAI-compatible) and counts models. Empty
// data array is reachable-but-no-models-loaded (LM Studio server up, no model
// loaded yet) — we surface that as Reachable=true + Models=nil.
func probeLMStudio(ctx context.Context, cfg doctorConfig) runtimeStat {
	out := runtimeStat{Endpoint: cfg.LMStudioEndpoint}
	body, err := httpProbe(ctx, cfg.HTTPClient, cfg.LMStudioEndpoint+"/v1/models", cfg.Timeout)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		out.Reachable = true
		out.Error = "parse /v1/models: " + err.Error()
		return out
	}
	out.Reachable = true
	for _, m := range parsed.Data {
		out.Models = append(out.Models, m.ID)
	}
	return out
}

func httpProbe(ctx context.Context, client *http.Client, url string, timeout time.Duration) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// --- text rendering ---------------------------------------------------------

func renderDoctorText(w io.Writer, r doctorReport) error {
	tw := tabPrinter(w)
	fmt.Fprintf(tw, "daimon doctor — environment health probe\n\n")

	// Build
	fmt.Fprintf(tw, "Build\n")
	fmt.Fprintf(tw, "  daimon\t%s (%s, %s/%s)\n", r.Build.Version, r.Build.Go, r.Build.OS, r.Build.Arch)
	fmt.Fprintln(tw)

	// Home
	fmt.Fprintf(tw, "DAIMON_HOME\n")
	if r.Home.ResolveError != "" {
		fmt.Fprintf(tw, "  resolve\t%s\n", r.Home.ResolveError)
	} else {
		src := "os.UserConfigDir()/daimon"
		if r.Home.SourceFromEnv {
			src = "$DAIMON_HOME"
		}
		fmt.Fprintf(tw, "  resolved\t%s (source: %s)\n", r.Home.Resolved, src)
	}
	if r.Home.Socket != "" {
		extra := ""
		if r.Home.SocketFallback {
			extra = " (sun_path fallback to $TMPDIR)"
		}
		fmt.Fprintf(tw, "  socket\t%s%s\n", r.Home.Socket, extra)
	}
	fmt.Fprintf(tw, "  keystore\t%s\n", renderFileStat(r.Home.Keystore, "absent — run `daimon init`"))
	fmt.Fprintf(tw, "  memory.db\t%s\n", renderFileStat(r.Home.MemoryDB, "absent (will be created on first unlock)"))
	fmt.Fprintf(tw, "  activity.log\t%s\n", renderActivityStat(r.Home.ActivityLog))
	fmt.Fprintln(tw)

	// Daemon
	fmt.Fprintf(tw, "Daemon\n")
	switch r.Daemon.State {
	case "not_running":
		fmt.Fprintf(tw, "  state\tnot running — run `daimon unlock` to start\n")
	case "locked":
		fmt.Fprintf(tw, "  state\trunning, locked — run `daimon unlock`\n")
	case "unlocked":
		fmt.Fprintf(tw, "  state\trunning, unlocked\n")
		if r.Daemon.DID != "" {
			fmt.Fprintf(tw, "  did\t%s\n", r.Daemon.DID)
		}
	default:
		fmt.Fprintf(tw, "  state\terror: %s\n", r.Daemon.DialError)
	}
	fmt.Fprintln(tw)

	// Provider env (presence only — never the value, so doctor is safe to
	// share screenshots from)
	fmt.Fprintf(tw, "Provider env (presence only)\n")
	fmt.Fprintf(tw, "  ANTHROPIC_API_KEY\t%s\n", yesNo(r.Env.AnthropicAPIKey))
	fmt.Fprintf(tw, "  OPENAI_API_KEY\t%s\n", yesNo(r.Env.OpenAIAPIKey))
	fmt.Fprintf(tw, "  LMSTUDIO_API_KEY\t%s\n", yesNo(r.Env.LMStudioAPIKey))
	fmt.Fprintln(tw)

	// Local runtimes
	fmt.Fprintf(tw, "Local runtimes\n")
	fmt.Fprintf(tw, "  Ollama\t%s\n", renderRuntime(r.Runtimes.Ollama))
	fmt.Fprintf(tw, "  LM Studio\t%s\n", renderRuntime(r.Runtimes.LMStudio))
	fmt.Fprintln(tw)

	// Live-round-trip readiness summary — the practical takeaway: which of the
	// three deferred provider round-trips would unblock right now?
	fmt.Fprintf(tw, "Live round-trip readiness\n")
	fmt.Fprintf(tw, "  Claude streaming\t%s\n", readiness(r.Env.AnthropicAPIKey, "ANTHROPIC_API_KEY"))
	fmt.Fprintf(tw, "  OpenAI streaming\t%s\n", readiness(r.Env.OpenAIAPIKey, "OPENAI_API_KEY"))
	fmt.Fprintf(tw, "  LM Studio (any)\t%s\n", readiness(r.Runtimes.LMStudio.Reachable, "LM Studio server"))

	return tw.Flush()
}

func renderFileStat(s fileStat, absent string) string {
	if !s.Present {
		return absent
	}
	return fmt.Sprintf("present (%s, %s)", humanBytes(s.Size), s.Mode)
}

func renderActivityStat(s activityStat) string {
	if !s.Present {
		return "absent (will be created on first unlock)"
	}
	base := fmt.Sprintf("present (%s, %s)", humanBytes(s.Size), s.Mode)
	if s.ScanError != "" {
		return base + " — scan failed: " + s.ScanError
	}
	if s.Entries == 0 {
		return base + " — empty (no committed entries)"
	}
	hash := s.LastHash
	if len(hash) > 16 {
		hash = hash[:16] + "…"
	}
	return fmt.Sprintf("%s — %d entries, last_hash=%s", base, s.Entries, hash)
}

func renderRuntime(r runtimeStat) string {
	if !r.Reachable {
		return fmt.Sprintf("%s — unreachable (%s)", r.Endpoint, r.Error)
	}
	if len(r.Models) == 0 {
		return fmt.Sprintf("%s — reachable, no models loaded", r.Endpoint)
	}
	return fmt.Sprintf("%s — ready (%d models: %s)", r.Endpoint, len(r.Models), truncate(joinModels(r.Models), 50))
}

func readiness(ready bool, want string) string {
	if ready {
		return "READY"
	}
	return "blocked — " + want + " not present"
}

// envKeyPresent reports whether the env var is set to a non-empty,
// non-whitespace value. The Claude Code harness exports redacted env vars as
// whitespace placeholders rather than empty strings, so a literal "!= """ check
// would falsely report them as configured. Trimming whitespace matches what
// the provider adapters do at registration: an all-whitespace bearer would
// fail any real API call, so reporting it as "set" misleads the user.
func envKeyPresent(name string) bool {
	return strings.TrimSpace(os.Getenv(name)) != ""
}

func yesNo(b bool) string {
	if b {
		return "set"
	}
	return "not set"
}

func joinModels(models []string) string {
	if len(models) == 0 {
		return ""
	}
	out := models[0]
	for i := 1; i < len(models); i++ {
		out += ", " + models[i]
	}
	return out
}

// humanBytes returns a short human-readable size like "3.0 KiB". Doctor sizes
// are always small enough that KiB/MiB precision is sufficient — no need for a
// full IEC ladder.
func humanBytes(n int64) string {
	const (
		k = 1024
		m = k * 1024
	)
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < m:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/m)
	}
}
