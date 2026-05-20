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

// version is the binary's reported version. The default "dev" is the
// fallback for builds that don't pass `-ldflags "-X main.version=..."`
// (e.g. `go install`, IDE builds, ad-hoc `go build`). The release
// workflow + Makefile both inject the real version via that ldflag,
// derived from `git describe --tags --dirty --always` so binaries
// built off a tagged commit report the tag, builds off main between
// tags report `<tag>-<n>-g<sha>`, and dirty checkouts get a `-dirty`
// suffix. See .github/workflows/release.yml + Makefile.
var version = "dev"

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
	case "payment":
		exitOnErr(cmdPayment(args))
	case "rotate-password":
		exitOnErr(cmdRotatePassword(args))
	case "backup":
		exitOnErr(cmdBackup(args))
	case "restore":
		exitOnErr(cmdRestore(args))
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
              --chain <c>   should use daimon.payment.pay (phase 40.5+)
              --digest <h>  instead — this is for advanced/debug use.
              [--json]
  daimon wallet show-mnemonic
              [--json]      Re-display the BIP-39 mnemonic. Prompts for
                            the keystore password (no echo) and re-runs
                            the full KDF + decrypt against the on-disk
                            file as a "prove you know the password right
                            now" attestation. Use to verify backup, or
                            to import the seed into MetaMask / Phantom.

  daimon payment pay        Pay an x402-protected URL end-to-end. Buffers
              <url>         the request body if any, dispatches the
              [--method m]  outbound HTTP, parses 402 → builds + signs
              [--body s]    EIP-3009 → retries with PAYMENT-SIGNATURE.
              [--ceiling-usd 0.10]  Per-payment USD cap; 0 disables
                            (not recommended). Default: $0.10.
              [--validity-seconds 300]  Override EIP-3009 validBefore
                            window. Default: 5 min.
              [--header H]  Extra request headers, 'Name: value' form,
                            comma-separated.
              [--json]      Emit full RPC result envelope. Default:
                            HTTP status to stderr, body to stdout.

  daimon rotate-password    Change the at-rest password for the identity +
                            wallet keystores. Offline-only (daemon must be
                            stopped). DID, mnemonic, derived addresses, and
                            audit chain are all preserved across the rotate;
                            only the Argon2id KEK changes.

  daimon backup             Snapshot the whole daimon (identity + wallet +
              --to <path>   memory + activity log) into a single file for
              [--no-encrypt] migration or archival. Encrypted by default
              [--force]     under a user-supplied passphrase (Argon2id +
                            AES-256-GCM); --no-encrypt produces a plain
                            .dbk wrapper for rsync-style copying. Inner
                            keystores stay encrypted regardless. Offline.

  daimon restore <path>     Inverse of backup. Auto-detects encrypted vs
              [--force]     plain mode from the file's magic header.
              [--dry-run]   Refuses to overwrite a non-empty $DAIMON_HOME
                            unless --force. With --dry-run, verifies the
                            backup file (decrypts, walks the tarball,
                            reports manifest) WITHOUT writing anything to
                            $DAIMON_HOME — useful for checking old backups
                            without committing to a restore. Offline.

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
