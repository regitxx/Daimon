# Daimon

[![CI](https://github.com/regitxx/Daimon/actions/workflows/ci.yml/badge.svg)](https://github.com/regitxx/Daimon/actions/workflows/ci.yml)

> One sovereign agent. For life. Owned by you.

**Daimon is a protocol giving every human one personal AI agent that holds their memory, identity, reputation, and money — portable across any AI provider, encrypted, owned entirely by them.**

In Socratic philosophy, your *daimon* (δαίμων) was your inner guiding voice — uniquely yours. The double meaning is intentional: at the technical layer, your daimon literally runs as a daemon on your machine.

This is not a chatbot. This is not a wrapper around Claude or GPT. This is the substrate.

---

## Why

Today, your "AI relationship" belongs to OpenAI or Anthropic. Switch providers and you lose everything: your context, your memory, your history, your accumulated identity. You start over.

The current generation of agent protocols — MCP, A2A, AGNTCY, x402 — solve real problems. None of them solves *this* one: a personal, portable, user-owned identity and memory layer that no provider controls.

Anthropic, OpenAI, and Google cannot build this. It cannibalizes their lock-in. The gap is permanent. We are filling it.

## Status

**Phase**: Day Zero — v0.1.0 GA shipped on both registries; v0.2.0-dev.3 pre-release with **pre-built binaries** on [GitHub Releases](https://github.com/regitxx/Daimon/releases/latest) (`curl | sh` one-liner installer in the next section, or `pip install --pre daimon-protocol` / `npm install @daimon-protocol/sdk@dev` for the SDKs).

The v0.1 surface (identity / memory / activity log / four streaming provider adapters / conversational chat REPL) is feature-complete and published as `daimon-protocol 0.1.0` on PyPI and `@daimon-protocol/sdk 0.1.0` on npm. The v0.2 surface (BIP-39/BIP-32 HD wallet + x402 payments + full seed lifecycle — show-mnemonic, recover, derive) is in tree, CI-protected, and published as a pre-release on both registries + as platform binaries on GitHub Releases. 356 Go test pass-lines + 65 pytest cases + 65 vitest cases run on every commit, plus a 9th CI shard that runs both SDKs end-to-end against a real-network mock x402 server (cryptographically verifies EIP-3009 signature recovery + asserts `wallet.derive` parity between both SDKs).

- **[`QUICKSTART.md`](./QUICKSTART.md)** — zero-to-paid-x402-resource in ~30 minutes, the whole v0.1 + v0.2 surface end-to-end
- [`SPEC.md`](./SPEC.md) — the protocol document (v0.1 + v0.2)
- [`CHECKPOINT.md`](./CHECKPOINT.md) — current state, decisions, next actions
- [`JOURNAL.md`](./JOURNAL.md) — chronological build log

## Try federation in 30 seconds (no setup beyond install)

A public daimon is running on Hetzner Falkenstein, accepting Noise IK
connections from anywhere on the internet. After installing daimon
locally (one-liner below in [Try v0.1](#try-v01--memory--provider-routing)),
prove the protocol works end-to-end:

```sh
# Dial the public daimon
daimon peer dial \
  --did did:key:z6Mkh7bW4iGukKYrgbtjki99sk2ZAyiP6mzcFSrS3DZus1Td \
  --endpoint tcp://178.105.195.194:9999

# Copy the channel_id it prints, then:
daimon peer echo <channel_id> "hello from anywhere"
```

If you see your message echoed back with the public daimon's DID as
sender, you just verified: a sovereign agent on your laptop talked to
a sovereign agent in Falkenstein, identified each other by cryptographic
keys (did:key), and exchanged a message over an end-to-end encrypted
channel (Noise IK over TCP). No central server. No account. No login.
Pure peer-to-peer.

That's the email-standard moment for agents. The same channel pattern
also carries `peer.ask` (delegate a question to another agent's
provider), `peer.pay.required` (x402 price discovery), and v0.4's
capability-token + signed-receipt flows.

> The public daimon is run by [@regitxx](https://github.com/regitxx) as
> infrastructure for the project. It accepts `peer.echo` from anyone
> (no address-book pin required) and audits every request to its own
> activity log. Don't send secrets through it — Noise IK protects the
> wire, but the audit row sees method names + caller pubkeys.

## Try v0.1 — memory + provider routing

```sh
# Install (one-line, no Go required, SHA-256 verified):
curl -fsSL https://raw.githubusercontent.com/regitxx/Daimon/main/install.sh | sh

daimon init              # once — choose a password
daimon unlock            # auto-spawns daimond
daimon memory write --kind fact "the sky is blue"
daimon memory list       # search needs Ollama for embeddings
```

Or build from source: `git clone https://github.com/regitxx/Daimon.git && cd Daimon && make build`.
Full walkthrough including v0.2 wallet + x402 payments: [QUICKSTART.md](./QUICKSTART.md).

Or use one of the SDKs:

- [`sdk/python`](./sdk/python) — `pip install daimon-protocol`, then `from daimon import Client`
- [`sdk/typescript`](./sdk/typescript) — `npm install @daimon-protocol/sdk`, then `import { Client } from "@daimon-protocol/sdk"`
- [`examples/streaming`](./examples/streaming) — cross-language streaming reference: both SDKs round-trip token deltas through the same daemon, audit chain verified three ways

## Try v0.2 — wallet + x402 payments (pre-release)

The v0.2 surface adds a wallet the daimon holds + signs with, and end-to-end x402 payment handling. Default `pip install daimon-protocol` and `npm install @daimon-protocol/sdk` still resolve to v0.1.0 stable; opt into the v0.2 surface via the pre-release channels:

```sh
# Pin the SDK pre-release
pip install --pre daimon-protocol         # or @dev on npm:
npm install @daimon-protocol/sdk@dev

# With a `daimon` binary on PATH (one-liner install above):
daimon unlock                       # auto-creates wallet keystore + surfaces 24-word mnemonic ONCE
daimon wallet create --chain evm:base
daimon payment pay --ceiling-usd 0.10 https://example.com/paid

# Forgot to write the mnemonic down? Re-display it (password-gated):
daimon wallet show-mnemonic
# Already have a 24-word backup elsewhere? Use THAT seed instead (offline, before first unlock):
daimon wallet recover
```

The seed lifecycle is fully under user control. `show-mnemonic` re-runs the full Argon2id KDF + AES-GCM-decrypt against the on-disk keystore (NOT the in-memory unlocked state), so the operation is a genuine "prove you know the password right now" attestation — wrong password surfaces a typed `-32008 CodeWrongPassword` distinct from "daemon is locked." `recover` is offline-only and refuses to overwrite a non-empty keystore, so an existing wallet can never be silently orphaned. The same canonical 12-word BIP-39 test vector (`abandon ... about` → `0x9858EfFD…EcaEda94`) that every external derivation tool (iancoleman.io/bip39, MetaMask, Phantom) produces is pinned in `internal/wallet`'s tests as an interop fixture.

[`examples/x402-smoke`](./examples/x402-smoke) — cross-language x402 reference: both SDKs pay a mock x402 server through one daimon; mock server cryptographically verifies the EIP-3009 signature recovers to the wallet's address (same property a real facilitator checks before settling on-chain). Run end-to-end in 30 seconds with `./examples/x402-smoke/run.sh`.

v0.2.0 GA cuts once live Base Sepolia settlement against a real x402-protected endpoint is verified (phase 40.4). Cryptographic surface is already self-tested by the CI smoke shard.

## What composes with what

Daimon does not compete with the existing protocol stack. It sits *above* it as the user-owned layer.

| Existing standard | Role in Daimon |
|---|---|
| MCP (Model Context Protocol) | Tool calls flow through daimon-core |
| W3C DID + Verifiable Credentials | Identity primitives (did:key, did:ion) |
| x402 | v0.2: agent-native payments |
| A2A | v0.3: agent-to-agent communication |
| Biscuit tokens | v0.4: capability delegation |
| MLS / Noise | v0.3: encrypted agent channels |

## v0.1 scope — local sovereign agent

A single daimon running on your machine. Holds your identity and memory. Routes requests to any LLM provider. Logs every action you can verify.

- DID identity (`did:key`, with `did:ion` anchor reserved for v0.1.x)
- Encrypted persistent memory — application-level AES-256-GCM rows under an identity-bound HKDF key (no SQLCipher, no CGO)
- Hash-chained, Ed25519-signed activity log walkable end-to-end via `daimon activity verify`
- Provider adapters: Claude, OpenAI, Ollama, LM Studio — all four also stream token-by-token
- First-party SDKs in Python and TypeScript at full RPC parity, both with native streaming surfaces
- **Single-player killer feature**: switch providers without losing context, memory, or identity

## v0.2 scope — the daimon holds its own money

Everything in v0.1, plus the wallet + payments primitive that makes "your agent acts on your behalf" real:

- **BIP-39/BIP-32 HD wallet** (24-word mnemonic, MetaMask-compatible). One seed per principal, N derived keypairs per chain. v0.2 ships EVM chains only (Base + Base-Sepolia in the chain registry); SLIP-10 / Ed25519 chains (Solana, Stellar) deferred to v0.2.x.
- **x402 payment client** — daimon parses HTTP 402 responses, signs EIP-3009 `transferWithAuthorization` against the matching wallet, retries with `PAYMENT-SIGNATURE`. Ceiling enforced **before** signing (over-budget 402s never produce a signature on the wire). Typed RPC error codes (`-32006 CodePaymentCeiling`, `-32007 CodePaymentUnsupported`) propagate to both SDKs.
- **Full seed lifecycle**: `daimon wallet show-mnemonic` (password-gated re-display, distinct typed `-32008 CodeWrongPassword`); `daimon wallet recover` (offline import from a 12- or 24-word phrase, refuses if a keystore already exists, cross-checks against identity keystore password); `daimon wallet derive` (read-only "what address would I get?" verification, no persistence); `daimon rotate-password` (change the at-rest password on both keystores in lockstep, preserves DID + mnemonic + audit chain). Recovery's success block displays the derived index-0 EVM address so users catch typos BEFORE any state change.
- **Audit chain extends naturally**: same Ed25519-signed hash chain as v0.1's memory rows now also carries `wallet.created` + `payment.signed` + `payment.settled` + `payment.failed` kinds.
- **SDK parity** in both Python and TypeScript: `client.wallet.{list, create, address, sign, derive, show_mnemonic}` + `client.payment.pay({url, ceilingSmallestUnit, …})`, byte-for-byte mirroring the daemon's wire shape.
- **`daimon doctor` Wallet section** surfaces the running daemon's wallet RPC surface state, with actionable remediation on the silent password-mismatch failure mode.

GA blocked on phase 40.4: live Base Sepolia settlement against a real x402-protected endpoint with a real facilitator (the cryptographic surface is self-tested end-to-end via the CI x402-smoke shard, but the on-chain settle step needs an external endpoint).

## Roadmap

| Phase | Months | Ships |
|---|---|---|
| v0.1 | 0–2 | daimon-core daemon, CLI, Python+TS SDKs, 4 provider adapters ✅ **shipped GA 2026-05-12** |
| v0.2 | 2–4 | x402 payment integration, agent wallet, full seed lifecycle ✅ **pre-release on PyPI `--pre` / npm `@dev` 2026-05-18, pre-built binaries on GitHub Releases 2026-05-20** (`v0.2.0-dev.3`); GA blocked on live Base Sepolia settlement |
| v0.3 | 4–6 | A2A discovery, federation, encrypted channels |
| v0.4 | 6–9 | Biscuit-token capability delegation, reputation primitive |
| v0.5 | 9–12 | First labor-market wedge: post-task / agent-bid / escrow |
| v1.0 | 12+ | Foundation handoff conversation |

## License

[Apache 2.0](./LICENSE).

## Governance

No VC. No commercial pressure. Foundation- and grant-funded (NLnet NGI Zero, Sovereign Tech Fund). Long-term target: Linux Foundation handoff once adoption justifies it.

The protocol is the public good. Anyone can implement it. No party owns it.

## Author

Created by **Johannes Christian Koeleman** ([@regitxx](https://github.com/regitxx)) — 2026.
