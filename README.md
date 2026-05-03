# Daimon

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

**Phase**: Day Zero — vision crystallized, spec drafting.

There is no production code yet. The protocol is being designed in the open.

- [`SPEC.md`](./SPEC.md) — the protocol document
- [`CHECKPOINT.md`](./CHECKPOINT.md) — current state, decisions, next actions
- [`JOURNAL.md`](./JOURNAL.md) — chronological build log

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

- DID identity (`did:key` default, optional `did:ion` anchor)
- Encrypted persistent memory (SQLCipher + vector index)
- Hash-chained signed activity log
- Provider adapters: Claude, OpenAI, Ollama
- **Single-player killer feature**: switch providers without losing context, memory, or identity

## Roadmap

| Phase | Months | Ships |
|---|---|---|
| v0.1 | 0–2 | daimon-core daemon, CLI, Python+TS SDKs, 3 provider adapters |
| v0.2 | 2–4 | x402 payment integration, agent wallet |
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
