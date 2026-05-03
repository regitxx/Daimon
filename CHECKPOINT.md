# Daimon — Project Checkpoint

> **Read this first at the start of every conversation.**
> Then read JOURNAL.md for full history. Then begin work.

**Last updated:** 2026-05-03
**Phase:** Day Zero complete — vision, name, and SPEC v0.1 shipped. Implementation next.

**Repository:** https://github.com/regitxx/Daimon.git

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
4. Resolve open questions in SPEC §11 — lock v0.1 defaults
5. Skeleton Go project for daimon-core (`cmd/daimond/main.go` + `internal/`)
6. First primitive: identity (`internal/identity` — DID generation, keystore, Argon2id-based encryption)
7. Second primitive: memory (`internal/memory` — SQLCipher init, schema, basic write/read)
8. Third primitive: activity log (`internal/activity` — append + verify)
9. RPC server (`internal/server` — JSON-RPC 2.0 over Unix socket)
10. First provider adapter (Claude)
11. CLI (`cmd/daimon` — wraps RPC for terminal use)
12. End-to-end demo: switch providers mid-task, memory persists
13. Apply to NLnet NGI Zero (deadline: rolling, every 2 months — application drafted in parallel with code work)

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
