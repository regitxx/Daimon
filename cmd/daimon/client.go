package main

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/regitxx/Daimon/internal/daimonhome"
)

// daemonCall is the canonical wrapper for "send one RPC against the running
// daemon": resolves $DAIMON_HOME, dials the persistent socket, and rewrites
// the two failure modes a user is likely to hit (daemon not running; daemon
// running but locked) into actionable hints.
//
// Does NOT auto-spawn — auto-spawn is reserved for `daimon unlock` per the
// session-13 lifecycle decision (auto-spawning here would silently start a
// locked daemon and immediately fail with CodeIdentityLocked, which is more
// confusing than the explicit hint).
//
// Every memory / provider / identity-get subcommand is one line of glue over
// this helper; centralising the error humanisation means the user sees the
// same recovery hint regardless of which RPC tripped.
func daemonCall(method string, params, out any) error {
	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}
	socket, _, err := daimonhome.SocketPath(home)
	if err != nil {
		return err
	}
	if err := rpcCall(socket, method, params, out); err != nil {
		if rpcErr, ok := asRPCError(err); ok && rpcErr.Code == codeIdentityLocked {
			return fmt.Errorf("daemon is locked — run `daimon unlock` first")
		}
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("daemon not running — run `daimon unlock` first")
		}
		return err
	}
	return nil
}
