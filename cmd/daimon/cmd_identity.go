package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"syscall"

	"github.com/regitxx/Daimon/internal/daimonhome"
)

// cmdIdentity routes the `identity` subcommand surface. v0.1 ships only `get`.
func cmdIdentity(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon identity get")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "get":
		return cmdIdentityGet(rest)
	default:
		return fmt.Errorf("daimon identity: unknown subcommand %q (only 'get' in v0.1)", sub)
	}
}

// cmdIdentityGet calls daimon.identity.get against the running daemon and
// prints the DID. Does NOT auto-spawn — if the daemon isn't running, the
// principal needs to unlock first (auto-spawning here would silently start a
// locked daemon and immediately fail with CodeIdentityLocked, which is more
// confusing than the explicit hint).
func cmdIdentityGet(args []string) error {
	fs := flag.NewFlagSet("daimon identity get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}
	socket, _, err := daimonhome.SocketPath(home)
	if err != nil {
		return err
	}

	var result struct {
		DID        string   `json:"did"`
		PublicKey  string   `json:"public_key"`
		DIDMethods []string `json:"did_methods"`
	}
	if err := rpcCall(socket, "daimon.identity.get", nil, &result); err != nil {
		if rpcErr, ok := asRPCError(err); ok && rpcErr.Code == codeIdentityLocked {
			return fmt.Errorf("daemon is locked — run `daimon unlock` first")
		}
		// Daemon not running (no socket file, or stale file with no listener)
		// is the common case after a fresh boot; reword the cryptic dial
		// error into the actionable hint.
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("daemon not running — run `daimon unlock` first")
		}
		return err
	}
	fmt.Printf("DID:          %s\n", result.DID)
	fmt.Printf("Public key:   %s\n", result.PublicKey)
	fmt.Printf("DID methods:  %v\n", result.DIDMethods)
	return nil
}
