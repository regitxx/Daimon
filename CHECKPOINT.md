# Daimon — Project Checkpoint

> **Read this first at conversation start.** Full chronological detail is in [JOURNAL.md](./JOURNAL.md).

**Last updated:** 2026-05-21
**Phase:** Day Zero — v0.1 GA shipped on PyPI + npm; v0.2 pre-release across SDKs + binaries; v0.2.0 GA gated on phase 40.4 (live Base Sepolia settlement). v0.3 phases 30–38 shipped + **SPEC §16 v0.3 federation formal specification written**.

---

## What you can install + run right now

| Channel | Command | Version |
|---|---|---|
| Binary one-liner | `curl -fsSL https://raw.githubusercontent.com/regitxx/Daimon/main/install.sh \| sh` | v0.2.0-dev.3 (binary) |
| PyPI (pre-release) | `pip install --pre daimon-protocol` | 0.2.0.dev2 |
| npm (pre-release) | `npm install @daimon-protocol/sdk@dev` | 0.2.0-dev.2 |
| PyPI (stable) | `pip install daimon-protocol` | 0.1.0 |
| npm (stable) | `npm install @daimon-protocol/sdk` | 0.1.0 |
| Source | `git clone https://github.com/regitxx/Daimon.git && cd Daimon && make build` | `git describe` |

End-to-end walkthrough: [QUICKSTART.md](./QUICKSTART.md) (zero → paid x402 resource in ~30 minutes).

---

## What's in tree right now

**v0.1 surface (GA, 2026-05-12):**
- Identity (did:key Ed25519 + Argon2id+AES-256-GCM keystore)
- Memory (SQLite + application-level AES-256-GCM rows under identity-bound HKDF subkey + cosine retrieval via Ollama embedder)
- Activity log (BLAKE3 hash-chained, Ed25519-signed, walkable via `daimon activity verify`)
- Four streaming provider adapters: Claude, OpenAI, Ollama, LM Studio
- Conversational chat REPL with multi-turn history + `inject_context` retrieval
- Python + TypeScript SDKs at full RPC parity, both with native streaming

**v0.2 surface (pre-release, 2026-05-18+):**
- BIP-39/BIP-32 HD wallet (24-word mnemonic, MetaMask-compatible)
- x402 payment client (EIP-3009 `transferWithAuthorization`, ceiling-before-signing, typed `-32006`/`-32007` error codes)
- Full seed lifecycle: `show-mnemonic` (password-gated re-display, typed `-32008`), `recover` (offline import with password parity cross-check), `derive` (read-only address prediction), `rotate-password` (change at-rest password without nuking state)
- **State migration**: `daimon backup --to file.dbk` + `daimon restore file.dbk` — whole-daimon snapshot in a single encrypted file (default) or plain `.dbk`; double-protected (backup passphrase + inner keystore passwords); offline-only; preserves DID + mnemonic + audit chain across machines
- Audit chain extends to `wallet.created` + `payment.signed` + `payment.settled` + `payment.failed` rows
- `daimon doctor` Wallet section surfaces the silent-disabled wallet-RPCs failure mode with actionable remediation
- Chain registry: Base + Base-Sepolia (USDC), per SPEC §15.3

**Distribution + infra:**
- `install.sh` one-liner with SHA-256 verification against published `checksums.txt`
- GitHub Releases pipeline (`.github/workflows/release.yml`) cross-compiles darwin/linux × amd64/arm64 on any `v*` tag
- Binary version via ldflags injection (`-X main.version=...`); SDK versions via `gen_version.py` (Python) + `gen-version.mjs` (TS), both CI-drift-checked
- 10 CI shards (Go race+vet, Python 3.10/3.11/3.12/3.13, Node 18/20/22, x402 cross-language smoke, install.sh on ubuntu + macOS)

**Tests:** 519 Go race+vet + 84 pytest + 85 vitest = **688 tests, all green on every push**. Plus 8 Go benchmarks runnable via `make bench` (not in CI; see [docs/perf.md](./docs/perf.md) for measured baselines).

**CLI peer + federation surface (phases 37–38):** `daimon federation config` + `daimon peer listen/dial/close/list/echo/invoke/pay-required` + `daimon peer address-book list/add/pin/block/unblock/remove`. Full --json escape-hatch; bash/zsh/fish completion updated. `daimon peer listen` starts the inbound Noise IK TCP listener after unlock.

**Repo:** https://github.com/regitxx/Daimon.git (public). Apache 2.0.

---

## Roadmap

| Phase | Window | Ships | Status |
|---|---|---|---|
| v0.1 | months 0–2 | daimon-core + CLI + Python/TS SDKs + 4 streaming providers + chat REPL | ✅ **GA 2026-05-12** |
| v0.2 | months 2–4 | wallet + x402 payments + full seed lifecycle | ✅ **pre-release** — GA gated on 40.4 |
| v0.3 | months 4–6 | A2A discovery, federation across machines, Noise IK encrypted channels, did:key transport, daimon as payment recipient | **phases 30–38 shipped 2026-05-21** (discovery, TCP+Noise transport, address book, peer.echo, peer.ask, peer.pay.required, SDK wrappers, CLI commands, peer.listen RPC). **SPEC §16 written 2026-05-21.** Design doc: [`design/v0.3-federation.md`](./design/v0.3-federation.md) |
| v0.4 | months 6–9 | Biscuit-token capability delegation, reputation primitive | not started |
| v0.5 | months 9–12 | First labor-market wedge: post-task / agent-bid / escrow | not started |
| v1.0 | months 12+ | Foundation handoff conversation, governance | aspirational |

---

## What's blocked + why

- **Phase 40.4** (live Base Sepolia settlement against a real x402-protected endpoint with a real facilitator) — needs test USDC + a real protected resource. Cryptographic surface is self-tested end-to-end via the x402-smoke CI shard, but the on-chain settle step needs externals. **Gates v0.2.0 GA.**
- **Phase 40.5b** (`provider.invoke` auto-pay on HTTP 402) — speculative; no LLM provider returns 402 today. Implementing now would land untested code in the path most users hit. **Deferred to v0.2.x or v0.3.**

---

## What we are building

**Daimon** — a protocol giving every human one sovereign agent for life. Portable, encrypted, owned by them. Holds their memory, identity, reputation, and money. Plugs into any AI model or service through an open protocol.

Not a chatbot. Not a wrapper. A substrate. The email-standard moment for agents.

In Socratic philosophy, the *daimon* (δαίμων) was your inner guiding voice — uniquely yours. The double meaning with Unix daemon is intentional: your daimon literally runs as a daemon on your machine.

### Why this and not something else

- **No incumbent can build it.** Anthropic, OpenAI, Google profit from lock-in. Cross-provider portable identity cannibalizes their business. The hole is permanent.
- **Composes, doesn't compete.** Sits on top of MCP, A2A, x402, DIDs. Not in the protocol war — above it.
- **Single-player utility from day one.** Even with zero other users: persistent memory across providers, no re-explaining yourself, switch Claude → GPT → local Llama without losing your agent.
- **Network value emerges naturally.** Once portable identity + memory + reputation exist, agent commerce, sub-agent delegation, and agent labor markets become trivial.

---

## How we work together

- **Claude (me)**: leads spec, code, research, docs, architecture. Implementation engine.
- **huckgod**: human persistence layer. Strategic decisions, outreach, account-level operations (publishing, repo visibility, signing), real-world continuity.
- **Repo as memory**: this file + JOURNAL.md are how state survives across conversations. Both are read at conversation start.

## Decisions locked in

| Decision | Choice | When |
|---|---|---|
| Project name | Daimon | 2026-05-03 |
| License | Apache 2.0 | 2026-05-03 |
| Core daemon language | Go | 2026-05-03 |
| SDK languages | TypeScript + Python | 2026-05-03 |
| PyPI distribution name | `daimon-protocol` (bare `daimon` taken) | 2026-05-12 |
| npm scope | `@daimon-protocol/sdk` (`daimon` org taken) | 2026-05-12 |
| Import name | `daimon` (stable regardless of dist name) | 2026-05-12 |
| Funding model | Foundation/grants. Not VC. Not commercial. | 2026-05-03 |
| Spirit | Built out of love, not for money | 2026-05-03 |
| Author attribution | Johannes Christian Koeleman (@regitxx), sole | 2026-05-12 |

## Constraints from huckgod (do not deviate)

- Repo commits attributed to Johannes Christian Koeleman <reziiix101@gmail.com> only. **NO Co-Authored-By trailers.** Pushes pre-authorized.
- NLnet NGI Zero application + asciicast demo recording are **off the table**. Do NOT surface as roadmap items, next-session leans, or punt-list items.

---

## Repository pointers

- [SPEC.md](./SPEC.md) — protocol document (v0.1 + v0.2)
- [QUICKSTART.md](./QUICKSTART.md) — zero-to-paid-x402-resource end-to-end walkthrough
- [JOURNAL.md](./JOURNAL.md) — chronological per-session decision log (full detail)
- [CONTRIBUTING.md](./CONTRIBUTING.md) — how to propose changes
- [SECURITY.md](./SECURITY.md) — responsible disclosure (GitHub Private Vulnerability Reporting)
- [PUBLISH.md](./PUBLISH.md) — release ritual (SDK publishes + binary distribution + dev cycling pattern)
- [docs/perf.md](./docs/perf.md) — measured performance baselines (Argon2id, AEAD, BIP-32, EIP-712)
- [design/v0.3-federation.md](./design/v0.3-federation.md) — v0.3 architectural proposal (DRAFT, awaiting review)
- [sdk/python/README.md](./sdk/python/README.md) + [sdk/typescript/README.md](./sdk/typescript/README.md) — per-language reference
- [examples/streaming](./examples/streaming) + [examples/x402-smoke](./examples/x402-smoke) — runnable cross-language demos

---

## Session history (compressed)

Detailed chronological entries live in JOURNAL.md. One-liner summaries here for fast orientation:

| Sessions | Date range | What shipped |
|---|---|---|
| 1–30 | 2026-05-03 → mid-May | v0.1 core: identity, memory, activity log, 4 provider adapters (with streaming), chat REPL, doctor, full CLI lifecycle |
| 31–37 | mid-May | Python + TS SDKs at full v0.1 RPC parity; CI matrix |
| 38–39 | 2026-05-12 | v0.1.0-dev.0 pipeline smoke → **v0.1.0 GA on PyPI + npm**; live Claude streaming three-way verified |
| 40–43 | 2026-05-12 → 18 | v0.2 wallet + x402 arc (six phases). BIP-39/BIP-32 wallet, payment client, RPC + CLI, SDK wrappers, x402-smoke CI shard, **v0.2.0-dev.0 pre-release** |
| 44–48 | 2026-05-19 | SPEC v0.2 formalisation (§14 wallet, §15 payments, §14.6 seed lifecycle); `show-mnemonic` + `recover` landing |
| 49–51 | 2026-05-19 | v0.2.0-dev.1 cut; Python `__version__` codegen + CI drift check; CI Actions v6 (Node 24 ready) |
| 52–55 | 2026-05-19 | Wallet UX hardening: `derive` verb, password parity cross-check, doctor wallet diagnostic, cross-language derive parity smoke. v0.2.0-dev.2 cut. **QUICKSTART.md.** |
| 56–58 | 2026-05-20 | Pre-built binary distribution: `.github/workflows/release.yml`, ldflags version injection. **v0.2.0-dev.3 binary release.** |
| 59 | 2026-05-20 | `install.sh` one-liner + release.yml pre-release auto-detection |
| 60 | 2026-05-20 | **Repo went public** (huckgod via `gh repo edit`); README v0.2 scope section |
| 61 | 2026-05-20 | CONTRIBUTING.md + SECURITY.md + `install-script` CI shard (shard 9 → 10) |
| 62 | 2026-05-20 | `daimon rotate-password` — change at-rest password without destroying state |
| 63 | 2026-05-20 | **CHECKPOINT pruned** 195 → 150 lines; **[`design/v0.3-federation.md`](./design/v0.3-federation.md) DRAFT** posted for review |
| 64 | 2026-05-20 | `daimon backup` + `daimon restore` — whole-daimon migration in one command |
| 65 | 2026-05-20 | `install.sh` respects `GH_TOKEN`/`GITHUB_TOKEN` to avoid GitHub API rate limits in CI |
| 66 | 2026-05-20 | `.github/ISSUE_TEMPLATE/*` + `PULL_REQUEST_TEMPLATE.md` + `go mod tidy` (3 deps indirect → direct) |
| 67 | 2026-05-20 | `daimon completion bash/zsh/fish` — static shell completion scripts |
| 68 | 2026-05-20 | `make ci-local` + `make build-all` — pre-push verification mirroring the 10-shard CI matrix |
| 69 | 2026-05-20 | Performance baselines: 8 benchmarks (`internal/{identity,secretbox,wallet,payment}/bench_test.go`) + `make bench` target + [`docs/perf.md`](./docs/perf.md) |
| 70 | 2026-05-20 | **v0.3 phase 30 slice 1**: W3C did:web resolver (`internal/did/web/{parse,document,resolve}.go`, +19 tests) |
| 71 | 2026-05-20 | **v0.3 phase 30 slice 2**: DaimonEndpoint + Ed25519 sign/verify (`internal/did/web/endpoint.go`, +12 tests) |
| 72 | 2026-05-20 | **v0.3 phase 30 slice 3**: `daimon.federation.config` RPC verb (`internal/server/federation_handlers.go`, +2 tests) |
| 73 | 2026-05-20 | **v0.3 phase 31 slice 0**: Ed25519→X25519 private-key derivation (`internal/transport/keyconv.go`, +8 tests) |
| 74 | 2026-05-20 | **v0.3 phase 31 slice 1**: Noise IK handshake wrapper (`internal/transport/noise.go`, +12 tests, +github.com/flynn/noise dep) |
| 75 | 2026-05-20 | **v0.3 phase 32**: address book persistence + RPC verbs + audit integration (`internal/addressbook/`, `internal/server/address_book_handlers.go`, +31 tests) |
| 76 | 2026-05-21 | **v0.3 phase 33 slice 0**: TCP+Noise transport + Ed25519 public→X25519 conversion (`internal/transport/tcp.go`, +11 transport tests) |
| 77 | 2026-05-21 | **v0.3 phase 33 slice 1**: peer.echo full stack — dial/close/list/invoke RPCs + two-daemon integration test (`internal/server/peer_channel_handlers{.go,_test.go}`, +18 server tests, 469 → 498 Go total) |
| 78 | 2026-05-21 | **v0.3 phase 34**: peer.ask — cross-daimon provider.invoke with address-book authorization gate, auto-populate on dial, KindPeerInvokeServed audit kind (+8 tests, 498 → 506 Go total) |
| 79 | 2026-05-21 | **v0.3 phase 35**: peer.pay.required — x402 price discovery verb, KindPeerPaymentInvoiced audit kind, universally authorized (+7 tests, 506 → 513 Go total) |
| 80 | 2026-05-21 | **v0.3 phase 36**: SDK wrappers — `client.federation.*` + `client.peer.*` (dial/close/list/invoke/echo/pay_required + addressBook.*) in Python + TypeScript (+19 pytest, +20 vitest; 65 → 84 pytest, 65 → 85 vitest) |
| 81 | 2026-05-21 | **v0.3 phase 37**: CLI peer + federation commands — `daimon federation config`, `daimon peer dial/close/list/echo/invoke/pay-required`, `daimon peer address-book list/add/pin/block/unblock/remove`. bash/zsh/fish completion updated. 513 Go tests still green. |
| 82 | 2026-05-21 | **v0.3 phase 38**: `daimon.peer.listen` RPC + `daimon peer listen` CLI — starts inbound Noise IK TCP listener post-unlock. `KindPeerListenStarted` audit kind. +5 tests (federation_handlers_test.go). 518 Go total. |
| 83 | 2026-05-21 | **SPEC §16 v0.3 federation** — formal protocol specification for all phases 30–38: transport (Noise IK/TCP), channel lifecycle, address book + trust model, served verbs (peer.echo / peer.ask / peer.pay.required), federation config wire format, 13 activity kinds, 4 error codes, v0.3 constraints + GA criteria. SPEC grows 797 → 1,222 lines. Version header bumped to v0.3 (Draft). |
| 84 | 2026-05-21 | **SDK wire fix**: `address_book.add` sent `label`/`pubkey_multibase` but server expects `pet_name`/`transport_pubkey_multibase`; optional fields were silently dropped. Fixed in Python + TypeScript SDKs; `AddressBookEntry.label` → `pet_name` in TS interface. Tests updated to assert correct wire names + absence of old ones. |
| 85 | 2026-05-21 | **§16.10 GA gate 2**: `TestFederationSmoke_EndToEnd` — 9-step narrative integration test in `internal/server/federation_smoke_test.go`. Two real daemons (Noise IK TCP), covers: federation.config, peer.dial, peer.list, peer.echo, peer.ask (mock provider), peer.pay.required error path, dual-side audit, channel close + empty list check. +1 Go test (519 total). |
| 86 | 2026-05-21 | `daimon unlock --peer-addr tcp://...` — starts inbound peer listener immediately post-unlock (no separate `daimon peer listen` step needed). Non-fatal on failure. Usage string + fish completion updated. `design/v0.3-federation.md` status updated to IMPLEMENTED → points to SPEC.md §16. |
