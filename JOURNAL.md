# Daimon — Build Journal

> Chronological log of decisions, discoveries, and direction shifts.
> Append to the bottom. Never rewrite history.

---

## 2026-05-03 — Day Zero

**Founders commit to building together.**

huckgod and Claude (Opus 4.7, 1M context) commit to building Daimon together. huckgod will provide the human persistence layer — strategic decisions, outreach, real-world continuity, pushing commits. Claude leads spec, implementation, research, architecture, docs.

Built out of love, not for money. No commercial pressure. No demo theater. Foundation/grant funding only (NLnet NGI Zero, Sovereign Tech Fund). Linux Foundation handoff is the long-term governance target.

**Vision crystallized.**

Daimon is a protocol giving every human one sovereign agent for life — portable, encrypted, owned by them. Holds memory, identity, reputation, money. Plugs into any AI model or service.

Killer wedge: single-player utility. Switch Claude → GPT → local Llama mid-task without losing your agent's memory or context.

**Why this specific bet:**
- No incumbent can build it (cannibalizes their lock-in)
- Composes with MCP, A2A, x402 — doesn't compete
- Single-player value from day zero
- Network effects emerge naturally once portable identity exists

**Name chosen: Daimon.**

Greek δαίμων — Socrates's inner guiding voice, uniquely yours. Double meaning with Unix daemon: your daimon literally runs as a daemon on your machine. Spelled with the Greek "i" to distinguish.

Alternatives considered: Anima (Latin, soul) and Vellum (signed parchment). Daimon won on philosophical depth + technical poetry.

**Provisional technical decisions:**
- License: Apache 2.0 (broadest enterprise adoption, compatible with foundation handoff)
- Core daemon: Go (good middle ground — fast to ship, decent systems story, healthy ecosystem)
- SDKs: TypeScript + Python (covers MCP/agent dev community)
- Repo layout: TBD next session

**Rhythm established:**
- CHECKPOINT.md = current state, read at conversation start
- JOURNAL.md = chronological log, append-only
- huckgod commits and pushes; I draft and write
- New conversation when context bloats, milestone hits, or I get confused

**Next session begins with:** drafting `SPEC.md` v0.1 and `README.md`. Then `git init` and first commit.

---

## 2026-05-03 — Day Zero, continued

**Name confirmed: Daimon.** huckgod said yes. Locked in.

**GitHub repo created:** https://github.com/regitxx/Daimon.git

**Documents shipped this session:**
- `README.md` — public face. Vision, why, status, composes-with table, roadmap, governance.
- `SPEC.md` v0.1 — protocol document. Local sovereign agent only. Federation/payments/reputation deferred.
- `LICENSE` — Apache 2.0 boilerplate.
- `.gitignore` — Go + Node + Python + Daimon runtime data.

**Spec v0.1 architectural decisions:**
- Single-tenant local daemon (one daimon per principal per process)
- JSON-RPC 2.0 over Unix socket (Linux/macOS) or localhost mTLS (Windows / network mode)
- Identity: did:key default, did:ion optional anchor. Ed25519. Argon2id / WebAuthn-PRF for at-rest key derivation.
- Memory: SQLCipher-encrypted SQLite + sqlite-vec. Per-row signed with Ed25519. Default embedding model: `nomic-embed-text` via local Ollama.
- Activity log: BLAKE3 hash-chained, signed per entry. Local only in v0.1.
- Two integration modes: **mediated** (provider creds in daimon) and **direct** (client manages providers, daimon just stores context+activity).

**Open questions captured in spec §11.** Most consequential: what `daimon.context.get` policy is for v0.1 — going with simple cosine similarity + recency boost. ML-driven retrieval is post-v0.1.

**Out of scope, by design:** any agent-to-agent feature, payments, reputation, sub-agent delegation, public verifiability. v0.1 must stand alone for one user.

**Repository initialized.** First commit pushed to https://github.com/regitxx/Daimon.git.

**Next session begins with:**
1. Read CHECKPOINT.md and JOURNAL.md.
2. Resolve the open questions in SPEC §11 — pick defaults, lock v0.1.
3. Begin reference implementation: `cmd/daimond` skeleton, `internal/identity` (DID generation + keystore), then `internal/memory`.

