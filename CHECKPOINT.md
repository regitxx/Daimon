# Daimon — Project Checkpoint

> **Read this first at the start of every conversation.**
> Then read JOURNAL.md for full history. Then begin work.

**Last updated:** 2026-05-04
**Phase:** Day Zero — vision, SPEC v0.1, defaults resolved, Go skeleton in place. **Identity, memory, activity-log, RPC server, four provider adapters (Claude + OpenAI + Ollama-chat + LM Studio — Claude, OpenAI, AND Ollama now stream token-by-token; LM Studio falls back transparently), the Ollama embedder, application-level row encryption (SPEC §5.1), the production lifecycle (`daimon init` / `unlock` / `identity get`), `daimon memory` (write / read / list / search / delete / export / import), `daimon provider` (list / invoke, with optional SPEC §11 inject_context), `daimon chat` (conversational REPL with multi-turn history persisted as JSONL across CLI invocations, `--inject-context` default ON, provider switchable mid-session, **`--stream` default ON for REPL / OFF for `--once` — token-by-token rendering via `daimon.provider.stream` server-pushed JSON-RPC notifications**), `demo/script.md` + `demo/README.md` (the demo's asciicast scaffolding), the LM Studio adapter, token-by-token streaming end-to-end through Ollama and Anthropic's Messages API, **and now SSE streaming through OpenAI's Responses API** — all landed.** The protocol's most concrete promise — switch Claude → OpenAI → local-LLM mid-task with memory intact — now spans two local runtimes (Ollama + LM Studio) AND streams from both major hosted providers (Anthropic + OpenAI); the conversational chat shell renders Claude, OpenAI, AND Ollama tokens as they arrive, and the `provider.Streamer` interface absorbs OpenAI in this session without touching `internal/server/`, `cmd/daimon/`, or SPEC — exactly as session 18's wire-shape contract predicted. Mediated mode is real; the cosine retrieval path is live when Ollama is running; the Provider interface is exercised against four wire formats (Anthropic Messages, OpenAI Responses, Ollama /api/chat, OpenAI Chat Completions) — three of which now also implement Streamer; memory rows are AES-256-GCM-encrypted at rest under an HKDF-derived, identity-bound key with no CGO and no SQLCipher dependency. **Every primitive AND every wrapper SPEC v0.1 demands is in tree, the demo script is committed, v0.1.x provider breadth has shipped with LM Studio, and v0.1.x UX has three of four streaming adapters live (Ollama + Claude + OpenAI); LM Studio streaming is the last adapter in the v0.1.x queue.** v0.1 polish work (asciicast recording held for huckgod's shell with real Anthropic + OpenAI keys; NLnet application) remains **deferred** by huckgod's call.

**Repository:** https://github.com/regitxx/Daimon.git
**Build status:** `make build` → `bin/daimond` (~15 MB) + `bin/daimon` (~4.6 MB). 214/214 test pass-lines green in ~10s (9 identity + 30 memory + 11 activity + 48 server + 12 provider + 17 claude adapter + 22 openai adapter + 25 ollama-chat adapter + 21 lmstudio adapter + 12 ollama embedder + 7 daimonhome), race-clean, vet-clean. The CLI surface itself is verified by an end-to-end manual smoke against a temp `$DAIMON_HOME` (init → unlock → identity get → memory write/read/list/search/delete/export/import → provider list → chat --once turn-1 → chat --once turn-2 with same name → chat --once with different --name → REPL via stdin heredoc → **chat --stream --once against live Ollama, deltas timed at ~8-9ms intervals proving real token-by-token rendering** → chat --stream against a non-streaming provider triggers the `[stream: ... falling back to invoke]` stderr note and retries against `daimon.provider.invoke` transparently → name validation → daemon kill → re-error), which is reproducible in a few seconds and runs at the end of any session that touches `cmd/daimon/`. The LM Studio probe-and-skip path is also smoke-verified against a shell where LM Studio is not running. **Live Claude AND OpenAI streaming round-trips are deferred to huckgod's shell** — the harness env redacts both `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`, same constraint that deferred the asciicast; the 7 `claude_test.go` SSE tests + 9 new `openai_test.go` SSE tests cover wire-shape correctness against httptest fixtures (Claude: happy path, ctx cancellation, malformed data, missing message_stop, HTTP 401, empty messages, Streamer; OpenAI: happy path with response.created/delta/completed shape, ctx cancellation, malformed data, missing terminal event, HTTP 401, mid-stream error event, response.incomplete with incomplete_details.reason normalisation, empty messages, Streamer).

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
17. ~~`daimon chat` — conversational REPL wrapping `daimon.provider.invoke` with multi-turn history persisted as JSONL at `$DAIMON_HOME/chat-sessions/<name>.jsonl`. `--inject-context` default ON (chat is conversational; opt-out via `--no-inject-context`); silent retrieval announcement on stderr per turn (`[inject_context: query="..."]`). History always loads from the named session — switching `--provider` mid-session preserves conversation across Claude/OpenAI/Ollama. `--once <prompt|->` for one-shot scripting; full REPL with `[<provider>/<model>]:` prefixed assistant turns and `/help` `/history` `/exit` slash commands. Persists user+assistant atomically only on RPC success — failed calls leave no orphan turns. Streaming punted to v0.1.x.~~ ✅ shipped 2026-05-04
18. ~~End-to-end demo: `demo/script.md` (six scenes, ~80s of typed action, real captured outputs from temp-`$DAIMON_HOME` smoke as expected-output blocks, narration line per beat for future voiceover, Ollama-only fallback documented per scene, recovery section for the five common first-take failures) + `demo/README.md` (how to play, how to re-record, scope of v0.1 vs v0.1.1). Recording itself held for huckgod's shell with real Anthropic + OpenAI keys; agent shell can't drive `asciinema rec` interactively and the harness env's API keys are redacted.~~ ✅ script + scaffolding shipped 2026-05-04; **asciicast recording = next session.**
19. Apply to NLnet NGI Zero — **deferred 2026-05-04** by huckgod's call. Resumes after a few v0.1.x building sessions; the demo asciicast and any LM Studio / streaming work that lands first will strengthen the application anyway.
20. ~~**LM Studio adapter** (post-v0.1, before v0.2). Fourth provider adapter at `internal/provider/lmstudio/`. Mirrors the `internal/provider/ollama/` shape: probes `http://localhost:1234/v1/models` at startup, registers if reachable, harvests loaded models live, sends `/v1/chat/completions` (the OpenAI Chat Completions wire format — NOT the Responses API the existing openai adapter uses, hence a separate package not a flag on openai).~~ ✅ shipped 2026-05-04. Default endpoint `http://localhost:1234`, env override `LMSTUDIO_HOST`. Bearer header always sent (default placeholder `lm-studio`, override via `LMSTUDIO_API_KEY`); probe sends bearer too so auth-required configs report as "wrong key" rather than "unavailable". Empty `data` on `/v1/models` is not an error (LM Studio up, no model loaded — registers with zero models). 21 test funcs covering probe success/failure, bearer plumbing, request-shape capture, all six `finish_reason` cases, empty-choices, malformed JSON, context cancellation, multi-turn ordering. Live round-trip pending huckgod's shell having LM Studio up locally.
21. **Live LM Studio round-trip.** ← *next session candidate when LM Studio is up locally.* Five-minute task: with LM Studio running and a model loaded, confirm `daimon provider list` shows the `lmstudio` row populated with the live model list, and `daimon chat --provider lmstudio --once "hi"` round-trips through `/v1/chat/completions`. Closes the live half of item 20. *Probed at the start of session 18; LM Studio still down on huckgod's shell, so streaming was tackled instead.*
22. ~~**Token-by-token streaming via `daimon.provider.stream` + `--stream` in the chat REPL.**~~ ✅ shipped 2026-05-04 (session 18). New `provider.Streamer` interface (channel-based, ctx-cancellation honoured) alongside the existing `Provider` — adapters opt in. Ollama implements it; Claude/OpenAI/LM Studio fall back via the CLI's `[stream: <provider> does not support streaming, falling back to invoke]` stderr note + transparent retry. Wire shape: server emits `daimon.provider.stream.delta` JSON-RPC notifications (no id field per JSON-RPC 2.0 §4.1) for each fragment, then the terminal `provider.Response` on the original request id. SPEC §6.1 carries the one-paragraph addition. CLI flag is tri-state (REPL default ON, `--once` default OFF). Activity-log payload includes `streamed:true` so audit history distinguishes the two call paths. Live smoke against `llama3.2:latest`: 35 separate deltas at ~8-9ms intervals — token-by-token rendering is real on huckgod's hardware. 198/198 tests race-clean (+15 over the 183 baseline: 8 ollama streaming + 7 server streaming).
23. v0.1.x building queue (in priority order): (a) **LM Studio `Stream` adapter** — half-day session adding `Stream()` to the lmstudio adapter via OpenAI Chat-Completions SSE (`data: {...}\n\n` chunks, terminal `data: [DONE]`); zero changes to server or CLI, just the adapter implementation + httptest fixtures. **Claude `Stream()` shipped 2026-05-04 (session 19)** via Anthropic Messages SSE: `message_start`/`content_block_delta`/`message_delta`/`message_stop` events, forward-compat ignores for ping/content_block_start/content_block_stop/future events; live Claude round-trip deferred to huckgod's shell (key redacted in harness). **OpenAI `Stream()` shipped 2026-05-04 (session 20)** via Responses API SSE: `response.created` (captures model), `response.output_text.delta` (emits text fragment from top-level `delta` field), terminal trio `response.completed`/`response.incomplete`/`response.failed` (each carries the full response object with status/usage/incomplete_details — same `normalizeStopReason` helper as the unary path so streaming and non-streaming UX are identical), mid-stream `error` event aborts with the carried message; forward-compat ignores for `response.in_progress`/`response.output_item.added|done`/`response.content_part.added|done`/`response.output_text.done` and any future tool/reasoning events. ctx cancellation via `http.NewRequestWithContext` + per-line `ctx.Err()` + select-guarded delta send (matches Claude shape exactly). 9 new tests in `openai_test.go` cover SSE wire correctness completely; live round-trip deferred to huckgod's shell (key redacted in harness). (b) server-side `injected_memory_ids` in `daimon.provider.invoke` response → chat REPL prints `[inject_context: query="..." matched=N]` with the count (~30 min, deferred from session 18 to keep streaming scope tight); (c) activity log encryption (SPEC §10) — same threat model as memory rows, `identity.DeriveSubkey` already generic enough, half-day; (d) internal `secretbox` factor — fold the four AES-GCM copies (identity keystore, provider credentials, memory rows, activity log when (c) lands) into one helper, half-day.

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
