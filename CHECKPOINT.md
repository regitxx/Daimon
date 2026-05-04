# Daimon — Project Checkpoint

> **Read this first at the start of every conversation.**
> Then read JOURNAL.md for full history. Then begin work.

**Last updated:** 2026-05-04
**Phase:** Day Zero — vision, SPEC v0.1, defaults resolved, Go skeleton in place. **Identity, memory, activity-log, RPC server, three provider adapters (Claude + OpenAI + Ollama-chat), the Ollama embedder, application-level row encryption (SPEC §5.1), the production lifecycle (`daimon init` / `unlock` / `identity get`), and now `daimon memory` (write / read / list / search / delete / export / import) + `daimon provider` (list / invoke, with optional SPEC §11 inject_context) — all landed.** Mediated mode is real; the cosine retrieval path is live when Ollama is running; the Provider interface is exercised against three wire formats (Anthropic Messages, OpenAI Responses, Ollama /api/chat); memory rows are AES-256-GCM-encrypted at rest under an HKDF-derived, identity-bound key with no CGO and no SQLCipher dependency. **Every primitive AND every wrapper SPEC v0.1 demands is now in tree.** A principal can run `daimon init`, `daimon unlock`, then `daimon memory write --kind fact "the cat sat on the mat"` and `daimon provider invoke openai "..."` from any terminal, with `--inject-context` opting into memory retrieval before the provider sees the prompt. Remaining v0.1 work is `daimon chat` (conversation-state management — opens streaming + multi-turn-history questions, deserves its own session), the demo video, and the NLnet application.

**Repository:** https://github.com/regitxx/Daimon.git
**Build status:** `make build` → `bin/daimond` (~15.2 MB) + `bin/daimon` (~4.8 MB). 162/162 test pass-lines green in ~10s (9 identity + 30 memory + 11 activity + 41 server + 12 provider + 10 claude adapter + 13 openai adapter + 17 ollama-chat adapter + 12 ollama embedder + 7 daimonhome), race-clean, vet-clean. The CLI surface itself is verified by an end-to-end manual smoke against a temp `$DAIMON_HOME` (init → unlock → identity get → memory write/read/list/search/delete/export/import → provider list → daemon kill → re-error), which is reproducible in a few seconds and runs at the end of any session that touches `cmd/daimon/`.

The production lifecycle now works end-to-end:

1. `daimon init` resolves `$DAIMON_HOME` (default `os.UserConfigDir()/daimon`), prompts for a password twice with confirmation, generates a fresh Ed25519 identity, and writes the encrypted keystore at mode 0600. Refuses to overwrite without `--force`.
2. `daimon unlock` resolves the same home, prompts for the password once, dials `$DAIMON_HOME/daimon.sock`. If the daemon isn't running, **auto-spawns `daimond serve` as a fully-detached process** (own session via Setsid, stdout/stderr to `$DAIMON_HOME/daimon.log`), polls the socket with bounded backoff up to 5s, then sends `daimon.identity.unlock {password}`. The daemon loads the keystore, opens the persistent memory store and activity log, and transitions from locked to unlocked via an atomic flag with release/acquire semantics. The unlock RPC is the **only** method the dispatcher allows pre-unlock; everything else returns `CodeIdentityLocked`. Idempotent: a second unlock against an already-unlocked daemon returns the same DID without re-deriving the key.
3. `daimon identity get` dials the existing socket and round-trips `daimon.identity.get` — the post-unlock smoke test that proves the gate also permits valid calls. Detects "daemon not running" (ENOENT/ECONNREFUSED) and "daemon locked" (CodeIdentityLocked) and prints actionable hints rather than raw errors.

The same `daimond` binary still runs the self-contained 8-step demonstration via `daimond demo` (ephemeral identity, temp socket, never touches `$DAIMON_HOME`) — the living spec from session 4 is preserved. The CLI and the daemon both route filesystem layout through the same `internal/daimonhome` resolver so they cannot disagree about where the keystore lives or where the socket should be opened (sun_path-overflow fallback to `$TMPDIR/daimon-$uid.sock` lives in that one helper).

---

## What we are building

**Daimon** — a protocol giving every human one sovereign agent for life. Portable, encrypted, owned by them. Holds their memory, identity, reputation, and money. Plugs into any AI model or service through an open protocol.

Not a chatbot. Not a wrapper. A substrate. The email-standard moment for agents.

In Socratic philosophy, the *daimon* (δαίμων) was your inner guiding voice — uniquely yours. The double meaning with Unix daemon is intentional: your daimon literally runs as a daemon on your machine.

## Why this and not something else

- **No incumbent can build it.** Anthropic, OpenAI, Google profit from lock-in. Cross-provider portable identity cannibalizes their business. The hole is permanent.
- **Composes, doesn't compete.** Sits on top of MCP, A2A, x402, DIDs. Not in the protocol war — above it.
- **Single-player utility from day one.** Even with zero other users: persistent memory across providers, no re-explaining yourself, switch Claude → GPT → local Llama without losing your agent.
- **Network value emerges naturally.** Once you have portable identity + memory + reputation, agent commerce, sub-agent delegation, agent labor markets become trivial.

## How we work together

- **Claude (me)**: leads spec, code, research, docs, architecture. Tireless implementation engine.
- **huckgod**: human persistence layer. Strategic decisions, outreach, conversations with potential users, pushing commits, signing things, real-world continuity.
- **Repo as memory**: this file + JOURNAL.md are how state survives across our conversations. I read both at conversation start.

## Decisions locked in

| Decision | Choice | Date |
|---|---|---|
| Project name | Daimon | 2026-05-03 |
| License | Apache 2.0 | 2026-05-03 |
| Core daemon language | Go | 2026-05-03 (provisional) |
| SDK languages | TypeScript + Python | 2026-05-03 (provisional) |
| Project root | /Users/huckgod/Developer/network | 2026-05-03 |
| Funding model | Foundation/grants (NLnet, Sovereign Tech Fund). Not VC. Not commercial. | 2026-05-03 |
| Spirit | Built out of love, not for money | 2026-05-03 |

## Roadmap

| Phase | Months | Ships |
|---|---|---|
| v0.1 | 0–2 | daimon-core daemon, CLI, Python+TS SDK, 3 provider adapters (Claude, GPT, Ollama), spec v0.1, demo video |
| v0.2 | 2–4 | x402 payment integration, agent wallet |
| v0.3 | 4–6 | A2A discovery layer, three seed nodes |
| v0.4 | 6–9 | Biscuit-token capability delegation, reputation primitive |
| v0.5 | 9–12 | First labor-market wedge: post-task / agent-bid / escrow / reputation |
| v1.0 | 12+ | Linux Foundation handoff conversation, foundation governance |

## v0.1 — what daimon-core does (and only this)

1. **Identity**: did:key (ephemeral) + did:ion (anchored). Keypair in OS keychain. Passkey-authenticated.
2. **Memory**: encrypted, structured long-term store (SQLite + vector index). Versioned, exportable, signed.
3. **Protocol endpoint**: tiny JSON-RPC API any LLM client can call. MCP-compatible.
4. **Provider adapters**: thin shims for Claude, OpenAI, Gemini, Ollama. Calls route through the daimon.
5. **Activity log**: every action signed and logged. Seed of reputation. Exportable. Yours.

## Next concrete actions (in order)

1. ~~Draft `SPEC.md` v0.1~~ ✅ shipped 2026-05-03
2. ~~Draft `README.md`~~ ✅ shipped 2026-05-03
3. ~~Stand up `git init`, first commit~~ ✅ shipped 2026-05-03
4. ~~Resolve open questions in SPEC §11 — lock v0.1 defaults~~ ✅ shipped 2026-05-03
5. ~~Skeleton Go project (go.mod, cmd/daimond/main.go, Makefile)~~ ✅ shipped 2026-05-03
6. ~~First primitive: identity (Ed25519 keypair, did:key, Argon2id+AES-GCM keystore, DID document)~~ ✅ shipped 2026-05-03
7. ~~Second primitive: memory (`internal/memory` — schema per SPEC §5.2, signed write/read, cosine search, signed export/import)~~ ✅ shipped 2026-05-03
8. ~~Third primitive: activity log (`internal/activity` — append-only JSONL, BLAKE3 hash-chained, Ed25519-signed, full chain Verify)~~ ✅ shipped 2026-05-03
9. ~~RPC server (`internal/server` — JSON-RPC 2.0 over Unix socket; wires identity, memory, activity, context.get to the SPEC §6.1 method surface)~~ ✅ shipped 2026-05-03
10. ~~First provider adapter (Claude) + the `daimon.provider.{list,invoke}` RPC surface — what makes mediated mode real~~ ✅ shipped 2026-05-03
11. ~~Real Ollama embedder behind the existing `Embedder` interface — unblocks cosine search and makes `context.get` non-trivial.~~ ✅ shipped 2026-05-03
12. ~~Second provider adapter (OpenAI Responses API) — proves the Provider interface generalises against a second wire format.~~ ✅ shipped 2026-05-03
13. ~~At-rest encryption for the memory store — closes SPEC §5.1. Chose application-level row encryption (AES-256-GCM, per-row nonce, AAD-bound to row id + field) over CGO + SQLCipher to preserve pure-Go single-binary distribution. Key derived from the identity's Ed25519 seed via HKDF-SHA256.~~ ✅ shipped 2026-05-03
14. ~~Ollama chat adapter (third provider) — `/api/chat` against the same local server we already embed against. Probes `/api/tags` at construction; registration is gated on probe success; locally-pulled model list is harvested live. Closes the "switch Claude → GPT → local Llama mid-task" loop and rounds out the v0.1 provider trio.~~ ✅ shipped 2026-05-04
15. ~~CLI lifecycle MVP — `daimon init` / `daimon unlock` (auto-spawns `daimond serve` detached) / `daimon identity get`. Adds `daimon.identity.unlock` RPC + locked-state dispatcher gate (`CodeIdentityLocked`). New `internal/daimonhome` resolver shared by both binaries. `daimond` splits into `serve` and `demo` subcommands. Lifecycle proven end-to-end: keystore at mode 0600, daemon detached via Setsid, three DIDs round-trip across the three subcommands.~~ ✅ shipped 2026-05-04
16. ~~`daimon memory` + `daimon provider` subcommands — mechanical wrappers over existing RPC. Seven memory verbs (write/read/list/search/delete/export/import) + two provider verbs (list/invoke with optional `--inject-context` for SPEC §11 retrieval). Human-default output (tabwriter tables for list, labeled blocks for read, plain ID/content for write/invoke pipeable to shell), `--json` escape hatch on every subcommand. Stdin via `-` for content/prompt. Shared `daemonCall` helper humanises the locked / not-running error paths into actionable hints across all subcommands.~~ ✅ shipped 2026-05-04
17. `daimon chat` — wraps `daimon.provider.invoke` with conversation-state management. Opens streaming + multi-turn-history-persistence questions; deserves its own session. ← *next session candidate*
18. End-to-end demo video: switch Claude → OpenAI → Ollama mid-task, memory persists.
19. Apply to NLnet NGI Zero (rolling deadline every 2 months — drafted in parallel with code work)

## Working rhythm

- Start every conversation by reading CHECKPOINT.md and JOURNAL.md
- Every meaningful decision → append to JOURNAL.md with date
- Major phase shift → update CHECKPOINT.md
- Start a new conversation when:
  - Current conversation passes ~50K tokens of work (context fatigue)
  - We finish a major milestone (e.g., spec done → start implementation)
  - I start showing repetition or confusion
  - You want a fresh perspective on a hard problem
- Before ending a conversation: I update JOURNAL.md with what we did, you commit + push

## North star

This is a 5–10 year project. We measure success by spec quality, code quality, and named adopters — not by user count or revenue. The protocol wins or it doesn't. We build it because it should exist.
