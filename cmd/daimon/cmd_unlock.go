package main

import (
	"bytes"
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
	peerAddr := fs.String("peer-addr", "", `TCP address to start the inbound peer listener on after
unlock, e.g. "tcp://0.0.0.0:9999" or "127.0.0.1:0". Equivalent
to running 'daimon peer listen --addr <a>' as a separate step.
Omit to skip starting the listener (default behaviour).`)
	passwordFile := fs.String("password-file", "", `Read the keystore password from this file
(first line, trailing newline trimmed) instead of prompting.
File MUST be chmod 0400 or 0600 and owned by the invoking user;
the CLI refuses to read anything more permissive to avoid
accidentally exposing the password to other local users.
Intended for systemd / supervisor setups where 'daimon unlock'
needs to run non-interactively on every boot — see
docs/systemd.md for the recommended hosted-daimon pattern.`)
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

	var pw []byte
	if *passwordFile != "" {
		// --password-file: non-interactive unlock for systemd / supervisor
		// setups. The only security property we require is that no user
		// OUTSIDE owner+group can read the file — i.e. world bits must
		// be zero. Group-readable is explicitly allowed because the
		// recommended hosted-daimon pattern (docs/systemd.md) is
		// root:daimon 0640 so the daimon user can read it at runtime via
		// group membership while root retains write authority.
		//
		// Bailing early with a friendly error here is belt-and-suspenders:
		// the kernel already enforces ownership for read access, but a
		// pre-flight check beats a confusing "permission denied" deep in
		// the unlock path.
		info, statErr := os.Stat(*passwordFile)
		if statErr != nil {
			return fmt.Errorf("--password-file: %w", statErr)
		}
		mode := info.Mode().Perm()
		if mode&0o007 != 0 {
			return fmt.Errorf("--password-file: %s has permissions %#o; world bits must be 0 (chmod o-rwx %s to fix)",
				*passwordFile, mode, *passwordFile)
		}
		raw, readErr := os.ReadFile(*passwordFile)
		if readErr != nil {
			return fmt.Errorf("--password-file: %w", readErr)
		}
		// Trim a single trailing newline (the common case for files written
		// by `echo "..." > file`). DO NOT trim arbitrary whitespace — the
		// password might legitimately end in a space, and silently eating
		// it would produce a wrong-password error with no clear cause.
		pw = bytes.TrimRight(raw, "\n")
		// Also handle CRLF if someone edited the file on Windows.
		pw = bytes.TrimRight(pw, "\r")
	} else {
		var readErr error
		pw, readErr = readPassword("Password: ")
		if readErr != nil {
			return readErr
		}
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
		DID      string   `json:"did"`
		Mnemonic []string `json:"mnemonic"`
	}
	if len(resp.Result) > 0 {
		_ = json.Unmarshal(resp.Result, &result)
	}
	fmt.Fprintln(os.Stderr, "Unlocked.")
	if result.DID != "" {
		fmt.Fprintf(os.Stderr, "  DID: %s\n", result.DID)
	}
	fmt.Fprintf(os.Stderr, "  Daemon: %s\n", socket)

	// --peer-addr: auto-start the inbound Noise IK TCP listener right after
	// unlock. Convenience shortcut for users who always want their daimon
	// reachable by peers (e.g. when running two daimons for the v0.3 dogfood).
	// Non-fatal: a listener failure prints a warning but the daimon is still
	// unlocked — nothing the unlock itself did is rolled back.
	if *peerAddr != "" {
		var listenResult struct {
			Endpoint string `json:"endpoint"`
		}
		params := map[string]any{"addr": *peerAddr}
		if lerr := daemonCall("daimon.peer.listen", params, &listenResult); lerr != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not start peer listener: %v\n", lerr)
			fmt.Fprintln(os.Stderr, "  Identity is still unlocked. Run 'daimon peer listen' manually.")
		} else {
			fmt.Fprintf(os.Stderr, "  Peer listener: %s\n", listenResult.Endpoint)
		}
	}

	// Mnemonic surfaces ONLY on the first unlock that auto-created the
	// wallet keystore. The daemon never keeps a copy after this RPC
	// returns — losing it now means losing the only way to recover the
	// wallet's keys, so the framing here is deliberately attention-getting.
	if len(result.Mnemonic) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")
		fmt.Fprintln(os.Stderr, "Wallet keystore initialised — back up this recovery phrase NOW.")
		fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────────────────────────")
		fmt.Fprintln(os.Stderr, "Write these 24 words down on paper, in order. This is the only")
		fmt.Fprintln(os.Stderr, "copy. The daemon does NOT keep a separate backup. If you lose")
		fmt.Fprintln(os.Stderr, "both this phrase AND the wallet.keystore file, every wallet you")
		fmt.Fprintln(os.Stderr, "ever derive from it is permanently inaccessible — including any")
		fmt.Fprintln(os.Stderr, "funds those wallets hold.")
		fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────────────────────────")
		// Print 4 words per line, numbered, for legibility.
		for i := 0; i < len(result.Mnemonic); i += 4 {
			end := i + 4
			if end > len(result.Mnemonic) {
				end = len(result.Mnemonic)
			}
			line := ""
			for j := i; j < end; j++ {
				line += fmt.Sprintf("  %2d. %-9s", j+1, result.Mnemonic[j])
			}
			fmt.Fprintln(os.Stderr, line)
		}
		fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")
	}
	return nil
}
