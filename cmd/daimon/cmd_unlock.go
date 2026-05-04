package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/regitxx/Daimon/internal/daimonhome"
)

// cmdUnlock dials the daemon (auto-spawning if necessary), prompts for the
// keystore password, and sends daimon.identity.unlock.
//
// Idempotent: calling unlock against an already-unlocked daemon returns the
// same DID without re-prompting on the daemon side. (We do still prompt for
// the password client-side to avoid the awkward "no prompt happened, did
// anything?" UX — the daemon's idempotency check makes the password value
// irrelevant on the second call, but the user gets a consistent flow.)
func cmdUnlock(args []string) error {
	fs := flag.NewFlagSet("daimon unlock", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}
	keystorePath := daimonhome.KeystorePath(home)
	if _, err := os.Stat(keystorePath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no keystore at %s — run `daimon init` first", keystorePath)
	} else if err != nil {
		return fmt.Errorf("stat keystore: %w", err)
	}
	socket, fallback, err := daimonhome.SocketPath(home)
	if err != nil {
		return err
	}
	if fallback {
		fmt.Fprintf(os.Stderr, "(socket fallback to %s — $DAIMON_HOME path too long for AF_UNIX)\n", socket)
	}

	pw, err := readPassword("Password: ")
	if err != nil {
		return err
	}
	if len(pw) == 0 {
		return errors.New("password must not be empty")
	}
	defer zero(pw)

	conn, err := dialOrSpawn(home, socket)
	if err != nil {
		return err
	}
	defer conn.Close()

	// We don't reuse rpcCall here because dialOrSpawn already gave us a
	// connection — re-dialling would defeat the point of the spawn-and-poll.
	if err := json.NewEncoder(conn).Encode(jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "daimon.identity.unlock",
		Params:  map[string]any{"password": string(pw)},
		ID:      1,
	}); err != nil {
		return fmt.Errorf("encode unlock: %w", err)
	}
	var resp jsonrpcResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("decode unlock: %w", err)
	}
	if resp.Error != nil {
		if resp.Error.Code == codeIdentityLocked {
			// The unlock RPC packs the actual reason ("wrong password or
			// corrupted keystore", "memory.Open: ...") into Data; Message is
			// always the generic "unlock failed". Surface Data when present.
			detail := resp.Error.Message
			if len(resp.Error.Data) > 0 {
				var s string
				if err := json.Unmarshal(resp.Error.Data, &s); err == nil && s != "" {
					detail = s
				}
			}
			return fmt.Errorf("unlock failed: %s", detail)
		}
		return resp.Error
	}

	var result struct {
		DID string `json:"did"`
	}
	if len(resp.Result) > 0 {
		_ = json.Unmarshal(resp.Result, &result)
	}
	fmt.Fprintln(os.Stderr, "Unlocked.")
	if result.DID != "" {
		fmt.Fprintf(os.Stderr, "  DID: %s\n", result.DID)
	}
	fmt.Fprintf(os.Stderr, "  Daemon: %s\n", socket)
	return nil
}
