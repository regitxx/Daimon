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

	fmt.Fprintln(os.Stderr, "[1/6] Generating ephemeral Ed25519 identity…")
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

	fmt.Fprintln(os.Stderr, "[2/6] Opening memory store and activity log…")
	store, err := memory.Open(filepath.Join(tmpDir, "memory.db"), id, memory.NullEmbedder{})
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

	fmt.Fprintln(os.Stderr, "[3/6] Writing three signed memories (each emits an activity entry)…")
	for _, w := range []memory.WriteRequest{
		{Kind: memory.KindFact, Content: "Daimon gives every human one sovereign agent for life.", Source: "spec"},
		{Kind: memory.KindPreference, Content: "Apache 2.0 licensed; foundation governance long-term.", Source: "spec"},
		{Kind: memory.KindObservation, Content: "Day Zero: identity, memory, and activity-log primitives all landed.", Source: "self"},
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

	fmt.Fprintln(os.Stderr, "[4/6] Exporting signed memory document and re-importing into a fresh store…")
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
	freshStore, err := memory.Open(filepath.Join(tmpDir, "fresh.db"), freshID, memory.NullEmbedder{})
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

	fmt.Fprintln(os.Stderr, "[5/6] Verifying the activity log chain end-to-end…")
	n, err := log.Verify(ctx)
	if err != nil {
		return fmt.Errorf("activity verify: %w", err)
	}
	fmt.Fprintf(os.Stderr, "      Chain OK: %d entries verified, last_hash=%s…\n",
		n, log.LastHash()[:24])

	fmt.Fprintln(os.Stderr, "[6/6] Starting RPC server and round-tripping daimon.identity.get…")
	if err := demoRPC(ctx, id, store, log); err != nil {
		return fmt.Errorf("rpc demo: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Provider adapters arrive in the next milestone.")
	return nil
}

// demoRPC spins up the JSON-RPC server on a temp socket, makes one self-call,
// prints the result, and shuts the server down. This exercises the full stack
// (transport + framing + dispatch + primitive) end-to-end.
func demoRPC(ctx context.Context, id *identity.Identity, store *memory.Store, alog *activity.Log) error {
	srv, err := server.New(server.Options{Identity: id, Store: store, Log: alog})
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

	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  "daimon.identity.get",
		"id":      1,
	}
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc error: %v", resp.Error)
	}
	fmt.Fprintf(os.Stderr, "      Socket: %s (mode 0600)\n", sockPath)
	fmt.Fprintf(os.Stderr, "      Reply : did=%v methods=%v\n",
		resp.Result["did"], resp.Result["did_methods"])
	return nil
}
