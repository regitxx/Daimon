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
//	daimon identity  — identity.get post-unlock smoke test
//	daimon memory    — write / read / list / search / delete / export / import
//	daimon provider  — list / invoke (with optional SPEC §11 inject_context)
//	daimon chat      — (v0.1.x — wraps provider.invoke with conversation
//	                   state across CLI invocations)
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
	case "memory":
		exitOnErr(cmdMemory(args))
	case "provider":
		exitOnErr(cmdProvider(args))
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

  daimon memory write       Append a memory row. Requires unlock.
              --kind <fact|preference|observation|task> (required)
              [--source <s>] [--metadata <json>] [--json] <content|->
  daimon memory read        Print one memory by id.
              [--json] <id>
  daimon memory list        List recent memories (table).
              [--kind <k>] [--limit <n>] [--json]
  daimon memory search      Similarity search against a query.
              [--kind <k>] [--limit <n>] [--json] <query>
  daimon memory delete      Delete one memory by id.
              [--json] <id>
  daimon memory export      Emit a signed export document.
              [--out <path>]
  daimon memory import      Ingest a signed export document.
              [--no-verify] [--json] <path|->

  daimon provider list      List configured providers (table).
              [--json]
  daimon provider invoke    Call a provider; print assistant text on stdout.
              [--model <id>] [--system <s>] [--temperature <f>]
              [--max-tokens <n>] [--inject-context[=<query>]]
              [--verbose] [--json] <provider> <prompt|->

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
