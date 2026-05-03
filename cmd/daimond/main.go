// Package main is the entry point for daimond, the Daimon Protocol reference daemon.
//
// daimond is the long-running local process that holds a principal's identity,
// memory, and activity log, and routes LLM provider calls. See SPEC.md for the
// protocol design.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/memory/ollama"
	"github.com/regitxx/Daimon/internal/provider"
	"github.com/regitxx/Daimon/internal/provider/claude"
	"github.com/regitxx/Daimon/internal/server"
)

const version = "v0.1.0-dev"

func main() {
	fmt.Fprintf(os.Stderr, "daimond %s — Day Zero\n", version)
	fmt.Fprintln(os.Stderr, "Daimon Protocol reference implementation")
	fmt.Fprintln(os.Stderr, "https://github.com/regitxx/Daimon")
	fmt.Fprintln(os.Stderr)

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Fprintln(os.Stderr, "[1/8] Generating ephemeral Ed25519 identity…")
	id, err := identity.Generate()
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	fmt.Fprintf(os.Stderr, "      DID: %s\n", id.DID())

	tmpDir, err := os.MkdirTemp("", "daimond-demo-*")
	if err != nil {
		return fmt.Errorf("tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Fprintln(os.Stderr, "[2/8] Selecting embedder and opening memory store + activity log…")
	embedder := pickEmbedder(ctx)
	store, err := memory.Open(filepath.Join(tmpDir, "memory.db"), id, embedder)
	if err != nil {
		return fmt.Errorf("memory open: %w", err)
	}
	defer store.Close()

	log, err := activity.Open(filepath.Join(tmpDir, "activity.log"), id)
	if err != nil {
		return fmt.Errorf("activity open: %w", err)
	}
	defer log.Close()

	if _, err := log.Append(ctx, activity.KindDaimonCreated, map[string]any{
		"version": version,
		"did":     id.DID(),
	}); err != nil {
		return fmt.Errorf("activity genesis: %w", err)
	}

	fmt.Fprintln(os.Stderr, "[3/8] Writing three signed memories (each emits an activity entry)…")
	for _, w := range []memory.WriteRequest{
		{Kind: memory.KindFact, Content: "Daimon gives every human one sovereign agent for life.", Source: "spec"},
		{Kind: memory.KindPreference, Content: "Apache 2.0 licensed; foundation governance long-term.", Source: "spec"},
		{Kind: memory.KindObservation, Content: "Day Zero: identity, memory, activity-log, RPC, and provider adapters all landed.", Source: "self"},
	} {
		mem, err := store.Write(ctx, w)
		if err != nil {
			return fmt.Errorf("write: %w", err)
		}
		if _, err := log.Append(ctx, activity.KindMemoryWrite, map[string]any{
			"id":     mem.ID,
			"kind":   string(mem.Kind),
			"source": mem.Source,
		}); err != nil {
			return fmt.Errorf("activity append: %w", err)
		}
	}
	memories, err := store.List(ctx, memory.ListOptions{})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	fmt.Fprintf(os.Stderr, "      Stored %d memories, all signatures verified on read.\n", len(memories))

	fmt.Fprintln(os.Stderr, "[4/8] Searching memories for 'sovereign agent'…")
	results, err := store.Search(ctx, "sovereign agent", memory.SearchOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "      No results.")
	} else {
		path := "cosine"
		if embedder.Dimensions() == 0 {
			path = "substring (NullEmbedder fallback)"
		}
		fmt.Fprintf(os.Stderr, "      Top hit (%s, score=%.3f): %s\n", path, results[0].Score, results[0].Memory.Content)
	}

	fmt.Fprintln(os.Stderr, "[5/8] Exporting signed memory document and re-importing into a fresh store…")
	doc, err := store.Export(ctx)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	if _, err := log.Append(ctx, activity.KindMemoryExport, map[string]any{
		"memories": len(doc.Memories),
	}); err != nil {
		return fmt.Errorf("activity export: %w", err)
	}
	fmt.Fprintf(os.Stderr, "      Export: format=%s memories=%d sig_bytes=%d\n",
		doc.Format, len(doc.Memories), len(doc.Signature))

	freshID, err := identity.Generate()
	if err != nil {
		return fmt.Errorf("fresh identity: %w", err)
	}
	freshStore, err := memory.Open(filepath.Join(tmpDir, "fresh.db"), freshID, embedder)
	if err != nil {
		return fmt.Errorf("fresh store: %w", err)
	}
	defer freshStore.Close()
	res, err := freshStore.Import(ctx, doc, memory.ImportOptions{})
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	fmt.Fprintf(os.Stderr, "      Import: imported=%d skipped=%d (cross-identity signatures verified)\n",
		res.Imported, res.Skipped)

	fmt.Fprintln(os.Stderr, "[6/8] Verifying the activity log chain end-to-end…")
	n, err := log.Verify(ctx)
	if err != nil {
		return fmt.Errorf("activity verify: %w", err)
	}
	fmt.Fprintf(os.Stderr, "      Chain OK: %d entries verified, last_hash=%s…\n",
		n, log.LastHash()[:24])

	fmt.Fprintln(os.Stderr, "[7/8] Building provider registry…")
	reg, creds := buildProviderRegistry()

	fmt.Fprintln(os.Stderr, "[8/8] Starting RPC server and round-tripping daimon.identity.get + daimon.provider.list…")
	if err := demoRPC(ctx, id, store, log, reg, creds); err != nil {
		return fmt.Errorf("rpc demo: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Mediated mode is real and the cosine path is live when Ollama is running locally.")
	fmt.Fprintln(os.Stderr, "Next: OpenAI provider adapter, then SQLCipher at-rest encryption.")
	return nil
}

// pickEmbedder returns the best available memory.Embedder for this run. It
// probes a local Ollama server first; if the probe fails — Ollama not
// installed, daemon not running, model not pulled, or the host is unreachable
// — the caller transparently falls back to memory.NullEmbedder per SPEC §11
// ("if Ollama absent — semantic search disabled; key-value memory still
// functions"). The probe is bounded to a few seconds so a misconfigured
// OLLAMA_HOST cannot stall the demo.
func pickEmbedder(parent context.Context) memory.Embedder {
	endpoint := ollama.DefaultEndpoint
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		endpoint = h
	}
	probeCtx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	emb, err := ollama.New(probeCtx, ollama.WithEndpoint(endpoint))
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"      Ollama unavailable (%v); falling back to NullEmbedder — semantic search disabled, key-value memory still functions (SPEC §11)\n",
			err)
		return memory.NullEmbedder{}
	}
	fmt.Fprintf(os.Stderr,
		"      Ollama embedder ready: model=%s dim=%d endpoint=%s\n",
		emb.Name(), emb.Dimensions(), endpoint)
	return emb
}

// buildProviderRegistry returns the provider registry and credential store
// the daimon will expose via daimon.provider.{list,invoke}. v0.1 ships with
// the Claude adapter; we register it only when ANTHROPIC_API_KEY is set in
// the environment so the demo is safe to run without keys (and never spends
// money on its own).
func buildProviderRegistry() (*provider.Registry, *provider.CredentialStore) {
	reg := provider.NewRegistry()
	creds := provider.NewCredentialStore()

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		ad, err := claude.New(key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "      ! claude adapter: %v (skipping)\n", err)
		} else {
			reg.Register(ad)
			creds.Set(ad.Name(), key)
			fmt.Fprintf(os.Stderr, "      Registered: %s (%d models)\n", ad.Name(), len(ad.Models()))
		}
	} else {
		fmt.Fprintln(os.Stderr, "      ANTHROPIC_API_KEY not set; Claude adapter not registered (demo will list zero providers)")
	}
	return reg, creds
}

// demoRPC spins up the JSON-RPC server on a temp socket, makes two self-calls
// (identity.get and provider.list), prints the results, and shuts the server
// down. This exercises the full stack — transport, framing, dispatch, every
// primitive — end-to-end.
func demoRPC(
	ctx context.Context,
	id *identity.Identity,
	store *memory.Store,
	alog *activity.Log,
	reg *provider.Registry,
	creds *provider.CredentialStore,
) error {
	srv, err := server.New(server.Options{
		Identity:    id,
		Store:       store,
		Log:         alog,
		Providers:   reg,
		Credentials: creds,
	})
	if err != nil {
		return err
	}
	// Short socket path: macOS sun_path is capped at ~104 bytes and
	// $TMPDIR alone usually consumes most of it.
	sockDir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		return err
	}
	defer os.RemoveAll(sockDir)
	sockPath := filepath.Join(sockDir, "s.sock")

	if err := srv.Listen(sockPath); err != nil {
		return err
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(serveCtx) }()
	defer func() {
		_ = srv.Close()
		<-serveErr
	}()

	c, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	enc := json.NewEncoder(c)
	dec := json.NewDecoder(c)

	// daimon.identity.get
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  "daimon.identity.get",
		"id":      1,
	}); err != nil {
		return fmt.Errorf("encode identity.get: %w", err)
	}
	var idResp struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := dec.Decode(&idResp); err != nil {
		return fmt.Errorf("decode identity.get: %w", err)
	}
	if idResp.Error != nil {
		return fmt.Errorf("identity.get rpc error: %v", idResp.Error)
	}
	fmt.Fprintf(os.Stderr, "      Socket: %s (mode 0600)\n", sockPath)
	fmt.Fprintf(os.Stderr, "      identity.get: did=%v methods=%v\n",
		idResp.Result["did"], idResp.Result["did_methods"])

	// daimon.provider.list
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  "daimon.provider.list",
		"id":      2,
	}); err != nil {
		return fmt.Errorf("encode provider.list: %w", err)
	}
	var listResp struct {
		Result []map[string]any `json:"result"`
		Error  any              `json:"error"`
	}
	if err := dec.Decode(&listResp); err != nil {
		return fmt.Errorf("decode provider.list: %w", err)
	}
	if listResp.Error != nil {
		return fmt.Errorf("provider.list rpc error: %v", listResp.Error)
	}
	fmt.Fprintf(os.Stderr, "      provider.list: %d adapter(s)\n", len(listResp.Result))
	for _, p := range listResp.Result {
		fmt.Fprintf(os.Stderr, "        - %v (configured=%v, models=%v)\n",
			p["name"], p["configured"], p["models"])
	}
	return nil
}
