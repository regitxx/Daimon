// Package main is the entry point for daimon, the Daimon Protocol CLI.
//
// daimon is a thin client over the JSON-RPC socket exposed by daimond. It
// matches the gpg-agent / ssh-agent / 1password-cli pattern: the CLI is
// always a client; `daimon unlock` auto-spawns daimond if it's not already
// running; subsequent invocations dial the existing socket.
//
// SPEC §11 surface (v0.1):
//
//	daimon init      — provision a keystore in $DAIMON_HOME (run once)
//	daimon unlock    — load the keystore (auto-spawns daimond if needed)
//	daimon identity  — `identity get` post-unlock smoke test for v0.1
//	daimon memory    — (lands in v0.1.x — wraps daimon.memory.{write,…})
//	daimon provider  — (lands in v0.1.x — wraps daimon.provider.{list,invoke})
//	daimon chat      — (lands in v0.1.x — wraps daimon.provider.invoke with
//	                   conversation-state management)
//
// v0.1 ships init / unlock / identity get only — the lifecycle proof. The
// remaining subcommands are mechanical wrappers over existing RPC methods and
// fall out trivially once the lifecycle works.
package main

import (
	"fmt"
	"os"
)

const version = "v0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "init":
		exitOnErr(cmdInit(args))
	case "unlock":
		exitOnErr(cmdUnlock(args))
	case "identity":
		exitOnErr(cmdIdentity(args))
	case "version", "--version", "-v":
		fmt.Printf("daimon %s\n", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "daimon: unknown subcommand %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `daimon %s — Daimon Protocol CLI

Usage:
  daimon init               Provision a fresh keystore in $DAIMON_HOME.
                            Run once per principal.

  daimon unlock             Load the keystore and unlock the daemon. Spawns
                            daimond automatically if it is not already
                            running. Prompts for the keystore password.

  daimon identity get       Print the principal's DID. Requires unlock.

  daimon version            Print the CLI version.
  daimon help               Show this message.

Environment:
  DAIMON_HOME               Override the home directory (default:
                            $XDG_CONFIG_HOME/daimon, or
                            ~/Library/Application Support/daimon on macOS).

The lifecycle: 'daimon init' (once, ever) -> 'daimon unlock' (once per
session) -> any other subcommand. The daemon stays running until killed;
'daimon unlock' twice in a row is a no-op on the second call.

https://github.com/regitxx/Daimon
`, version)
}

func exitOnErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "daimon: %v\n", err)
	os.Exit(1)
}
