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
//	daimon chat      — conversational REPL with multi-turn history persisted
//	                   as JSONL under $DAIMON_HOME/chat-sessions/, switchable
//	                   provider mid-session, inject_context default on
//	daimon doctor    — read-only environment health probe: $DAIMON_HOME state,
//	                   daemon up/locked/unlocked, API-key presence, LM Studio
//	                   + Ollama reachability. Safe at any moment.
//	daimon activity  — query the audit trail every other subcommand writes to,
//	                   or verify the chain end-to-end (prev_hash continuity +
//	                   BLAKE3 hash recomputation + Ed25519 signature).
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
	case "chat":
		exitOnErr(cmdChat(args))
	case "doctor":
		exitOnErr(cmdDoctor(args))
	case "activity":
		exitOnErr(cmdActivity(args))
	case "wallet":
		exitOnErr(cmdWallet(args))
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
              --inject-preview [--kind <k> ...] [--max-tokens <n>] [--json] <query>
                            With --inject-preview, dry-run the SPEC §11
                            inject_context retrieval: print what would be
                            folded into a provider's system prompt without
                            calling any provider. Multi-kind allowlist via
                            repeated --kind. Same RPC the chat REPL's
                            inject_context flow uses (daimon.context.get).
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

  daimon chat               Conversational REPL with multi-turn history.
              --provider <name> (required)
              [--model <id>] [--system <s>] [--temperature <f>]
              [--max-tokens <n>] [--name <s>]
              [--no-inject-context] [--inject-query <q>]
              [--once <prompt|->] [--json]
                            History always loads from
                            $DAIMON_HOME/chat-sessions/<name>.jsonl
                            (default name: 'current'). Provider can change
                            mid-session — memory persists across switches.
                            inject_context defaults ON.

  daimon doctor             Read-only health probe: $DAIMON_HOME on-disk
              [--json]      state, daemon running/locked/unlocked, API-key
              [--timeout]   presence (presence only, never the value), and
                            LM Studio + Ollama reachability. Safe at any
                            moment; never auto-spawns the daemon.

  daimon activity query     Read the principal's audit trail (table).
              [--since <duration|RFC3339>] [--kind <k> ...]
              [--limit <n>] [--json]
                            --kind is repeatable for an OR filter (applied
                            client-side; --json returns the unfiltered server
                            response and tooling should issue one call per
                            kind). Time window is inclusive; --since accepts
                            either a Go duration ("1h") or an RFC3339
                            timestamp.
  daimon activity verify    Walk the chain end-to-end. Asserts prev_hash
              [--json]      continuity, BLAKE3 hash recomputation, and Ed25519
                            signature for every entry. On success appends an
                            activity.verified entry to the log itself; on
                            failure exits non-zero with the offending entry
                            named, suitable for 'daimon activity verify &&
                            deploy' pre-flight in scripts.

  daimon wallet list        List the principal's HD-derived wallets.
              [--json]
  daimon wallet create      Derive a new wallet for the given chain.
              --chain <c>   v0.2: EVM chains only (e.g. evm:base,
              [--json]      evm:base-sepolia). Audit-logs wallet.created.
  daimon wallet address     Print the address for the wallet on the
              --chain <c>   given chain.
              [--json]
  daimon wallet sign        Low-level: sign a 32-byte digest. Most callers
              --chain <c>   should use daimon.payment.pay (phase 40.3+)
              --digest <h>  instead — this is for advanced/debug use.
              [--json]

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
