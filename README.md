# Daimon

[![CI](https://github.com/regitxx/Daimon/actions/workflows/ci.yml/badge.svg)](https://github.com/regitxx/Daimon/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](./LICENSE)

A protocol and reference implementation for a single, lifelong personal agent that holds its principal's identity, memory, reputation, and money. The agent runs as a local daemon. Its data is encrypted at rest under keys derived from a password the principal alone knows. Any LLM provider becomes interchangeable underneath: switching from Claude to OpenAI to a local Ollama model preserves the relationship.

The name comes from the Socratic *daimon* (δαίμων) — the inner guiding voice. The double reading as a Unix daemon is deliberate.

## Problem

A person who uses an AI accrues value in two directions: context they have given the model, and context the model has learned about them. Today that value is pinned to whichever provider they happen to be using, and switching models restarts the relationship from zero. The commercial incentives of the providers run directly against portability: Anthropic, OpenAI, and Google each profit when the memory lives with them. The result is that the layer which should be most personal — one's own agent — is the layer least owned by the person it represents. Daimon separates the person-owned layer from the provider: one daimon per principal, running on their machine, holding identity (Ed25519 under `did:key`), memory (SQLite with per-row AES-256-GCM), a tamper-evident activity log (BLAKE3-chained and Ed25519-signed), an HD wallet (BIP-39 / BIP-32) that signs x402 payments against a pre-signing ceiling, and from v0.3 an opt-in federation surface over Noise IK. The protocol is Apache 2.0, extraplatform, and designed so no implementer, including the reference one, can hold user data hostage.

## Architecture

```mermaid
flowchart TB
    subgraph client["Client processes"]
        CLI["daimon CLI"]
        SDKPY["Python SDK<br/>daimon-protocol"]
        SDKTS["TypeScript SDK<br/>@daimon-protocol/sdk"]
    end

    subgraph daemon["daimond — local daemon"]
        RPC["JSON-RPC 2.0<br/>Unix socket, mode 0600"]
        ID["Identity<br/>Ed25519 did:key<br/>Argon2id keystore"]
        MEM["Memory<br/>SQLite + AES-256-GCM<br/>per-row AAD binding"]
        LOG["Activity log<br/>BLAKE3 hash chain<br/>Ed25519 signed"]
        WAL["Wallet<br/>BIP-39 + BIP-32<br/>EVM secp256k1"]
        PAY["Payments<br/>x402 / EIP-3009<br/>pre-sign ceiling"]
        AB["Address book<br/>TOFU pinning<br/>per-verb grants"]
        CAP["Capability tokens<br/>Biscuit v3<br/>Datalog checks"]
        REP["Reputation receipts<br/>Ed25519 signed"]
    end

    subgraph net["Network"]
        PROV["LLM providers<br/>Claude · OpenAI · Ollama · LM Studio"]
        PEER["Peer daimons<br/>Noise IK over TCP"]
        CHAIN["EVM chains<br/>Base · Base Sepolia"]
    end

    CLI --> RPC
    SDKPY --> RPC
    SDKTS --> RPC
    RPC --> ID
    RPC --> MEM
    RPC --> LOG
    RPC --> WAL
    RPC --> PAY
    RPC --> AB
    RPC --> CAP
    RPC --> REP
    WAL -. signs .-> PAY
    PAY -. EIP-3009 .-> CHAIN
    RPC -. provider.invoke .-> PROV
    AB -. peer.ask .-> PEER
    CAP -. gates .-> AB
    REP -. returned by .-> PEER
```

Three things to hold in mind while reading the rest:

1. The daemon is single-tenant. One daimon, one principal. Multi-principal is explicitly deferred — it is a different security model, not a feature.
2. Every piece of persistent state is encrypted at rest under a key derived from the user's password via Argon2id. The daemon never writes the key to disk; it holds it in memory only while unlocked.
3. The activity log is append-only, hash-chained with BLAKE3, and signed with the identity Ed25519 key. Every action the daimon takes writes a row. The chain is walkable end-to-end by `daimon activity verify`; tampering with a row or a hash is detected deterministically.

For the deeper architectural picture, read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). For the threat model, cryptographic choices, and known limitations, read [docs/SECURITY.md](docs/SECURITY.md). The formal protocol specification is [SPEC.md](SPEC.md) (17 sections, versioned from v0.1 through v0.4).

## Security and design decisions

The short version of the threat model, with the full form in [docs/SECURITY.md](docs/SECURITY.md).

| Layer | Primitive | Why |
|---|---|---|
| Identity signatures | Ed25519 | Small keys, fast verification, no ambient configuration. Pairs with `did:key` for a self-contained DID. |
| Password → KEK | Argon2id, ≥64 MiB memory, ≥3 iterations, ≥4 parallelism | Memory-hard, resistant to GPU and ASIC attack, parameters above OWASP's 2024 floor. |
| Key material at rest | AES-256-GCM under `internal/secretbox` | AEAD with per-value random nonce. Authenticated — a tampered keystore fails to open. |
| Memory row encryption | AES-256-GCM + per-row AAD = `"daimon-memory-row-v1" \|\| row_id \|\| field_name` | Prevents silently moving a ciphertext onto a different row or into a different column. |
| Activity payload encryption | AES-256-GCM + per-entry AAD = `"daimon-activity-payload-v1" \|\| entry_id \|\| "payload"` | Same construction as memory, bound to the entry id so a ciphertext cannot be reparented. |
| Subkey derivation | HKDF-SHA256 with domain-separated info labels | Independent subkeys for memory, activity, and future stores derive from one Ed25519 seed with no on-disk key material. |
| Content hashing | BLAKE3 | Fast on commodity hardware, drop-in replacement for SHA-2, extensively analyzed. |
| Wallet seed | BIP-39 (24 words, 256 bits of entropy) + BIP-32 HD derivation along `m/44'/60'/0'/0/i` | Interoperable with MetaMask, Rabby, and every standard EVM wallet. Users can reconstruct their wallet from the seed phrase alone, on paper. |
| Payment signatures | EIP-712 typed data for EIP-3009 `transferWithAuthorization` | Standard x402 facilitator path. The per-call ceiling is enforced *before* signing, so an over-budget authorisation never produces a signature on the wire. |
| Federation channel | Noise IK over TCP | Mutual authentication with known responder, forward secrecy. The same Ed25519 identity key authenticates the channel (converted to X25519). |
| Capability delegation | Biscuit v3 tokens with Datalog checks | Offline attenuation: a delegator can tighten a token further without contacting the issuer. Verification uses ambient facts (time, model, call count). |

Verification you can run yourself:

```sh
# Walk the local activity log end-to-end.
daimon activity verify
# Output: "verified N entries — chain ok" (or a precise failure point).

# Confirm the wallet derivation matches the BIP-39 canonical test vector.
go test ./internal/wallet -run TestDerive_EVMAddressMatchesPublishedVector
# Expects m/44'/60'/0'/0/0 of "abandon abandon ... about" → 0x9858EfFD232B4033E47d90003D41EC34EcaEda94.

# Confirm the EIP-712 domain and transferWithAuthorization typehashes.
go test ./internal/payment -run TestTypehash
```

The two pinned EIP-712 typehashes (`0x8b73c3c6…` for the domain, `0x7c7c6cdb…` for `transferWithAuthorization`) and the BIP-39 derivation vector are part of the protocol's published surface and do not change without a major version bump. They are also restated in [docs/SECURITY.md](docs/SECURITY.md) as cryptographic anchors.

## Install and quickstart

The quickstart below is lifted from [QUICKSTART.md](QUICKSTART.md) and every command has been run in CI.

```sh
# Install (one-liner with SHA-256 verification against the published checksums.txt)
curl -fsSL https://raw.githubusercontent.com/regitxx/Daimon/main/install.sh | sh

daimon init           # choose a password; generates Ed25519 identity, provisions keystore
daimon unlock         # enter it; auto-spawns daimond on the Unix socket

# Write a few things to memory.
daimon memory write --kind fact       "I work on protocol design."
daimon memory write --kind preference "Prefer short, technical explanations."

# Chat with any provider you have an API key configured for.
# The daimon injects relevant memory into the prompt before calling the provider;
# the stderr line "[memory: 2 recalled]" shows what was injected.
daimon chat --once "What should I work on today?"

# Same memory, different provider, no re-explaining.
daimon chat --provider openai --once "What should I work on today?"
daimon chat --provider ollama --once "What should I work on today?"

# Build from source instead:
git clone https://github.com/regitxx/Daimon.git
cd Daimon
make build            # produces bin/daimon + bin/daimond

# Run the full suite locally (mirrors CI minus the install-script shard).
make ci-local
```

A local Ollama server gives the daimon a free, offline provider: `ollama pull llama3.2`, then `daimon chat --provider ollama` runs entirely on-device.

## SDKs

Both SDKs are at full RPC parity with the daemon and support native streaming.

```sh
# Python (import name stays `daimon`)
pip install daimon-protocol               # v0.1.0 (GA)
pip install --pre daimon-protocol         # v0.2.0.dev2 (wallet + payments)

# TypeScript
npm install @daimon-protocol/sdk          # v0.1.0 (GA)
npm install @daimon-protocol/sdk@dev      # v0.2.0-dev.2 (wallet + payments)
```

Minimal usage is in [sdk/python/README.md](sdk/python/README.md) and [sdk/typescript/README.md](sdk/typescript/README.md). Cross-language reference code for streaming and x402 payments is in [examples/streaming](examples/streaming) and [examples/x402-smoke](examples/x402-smoke). The x402 smoke harness is run end-to-end on every CI push and asserts byte-for-byte wire parity between the two SDKs against a real-network mock facilitator that cryptographically verifies the EIP-3009 signatures.

## Testing

All three suites are green as of 2026-11-07 on a local run.

| Suite | Count | How to run |
|---|---|---|
| Go (daemon + CLI + internal packages) | 589 top-level test functions across 112 files | `go test -race -count=1 ./...` |
| Python SDK | 96 | `cd sdk/python && pytest -q` |
| TypeScript SDK | 96 | `cd sdk/typescript && npm test` |
| **Total** | **781** | `make ci-local` |

The CI matrix on every push and pull request runs: Go vet + race + build, Python 3.10 / 3.11 / 3.12 / 3.13, Node 18 / 20 / 22, the x402 cross-language live smoke, the v0.4 cross-daimon capability + reputation smoke, and `install.sh` end-to-end on both Ubuntu and macOS against the published release. Failures block merge.

Measured performance baselines for the CPU-bound paths (Argon2id, AEAD, BIP-32 derivation, EIP-712 hashing) live in [docs/perf.md](docs/perf.md) and can be reproduced with `make bench`.

## Status

What is in tree, grounded in the SPEC sections it conforms to.

| Component | SPEC | State |
|---|---|---|
| Ed25519 `did:key` identity, Argon2id + AES-GCM keystore | §4, §9.3 | shipped v0.1.0 (GA) |
| Memory: SQLite, per-row AES-GCM, HKDF subkey, AAD binding | §5, §9.3 | shipped v0.1.0 (GA) |
| Activity log: BLAKE3 chain, Ed25519 signatures, AEAD payloads | §8 | shipped v0.1.0 (GA) |
| Provider adapters: Claude, OpenAI, Ollama, LM Studio (+ streaming) | §7 | shipped v0.1.0 (GA) |
| Conversational chat REPL with retrieval over memory | §6, §11 | shipped v0.1.0 (GA) |
| Python + TypeScript SDKs at full RPC parity | §6, §13 | shipped v0.1.0 (GA) |
| HD wallet: BIP-39 (24 words), BIP-32 EVM secp256k1 derivation | §14 | shipped v0.2 (pre-release) |
| x402 payments: EIP-3009, ceiling-before-signing, typed errors | §15 | shipped v0.2 (pre-release) |
| Seed lifecycle: `show-mnemonic` / `recover` / `derive` / `rotate-password` | §14.6 | shipped v0.2 (pre-release) |
| Backup / restore (whole-daimon encrypted snapshot) | §14.7 | shipped v0.2 (pre-release) |
| `did:web` resolver and `DaimonEndpoint` signing | §16.1 | shipped v0.3 (pre-release) |
| Noise IK over TCP, Ed25519 → X25519 transport authentication | §16.2 | shipped v0.3 (pre-release) |
| Address book with TOFU pinning and per-verb authorization | §16.4 | shipped v0.3 (pre-release) |
| Peer verbs: `peer.echo`, `peer.ask`, `peer.pay.required` | §16.5 | shipped v0.3 (pre-release) |
| Biscuit v3 capability tokens: issue / attenuate / verify | §17.2–§17.4 | shipped v0.4 (pre-release) |
| Reputation receipts (Ed25519-signed, returned by `peer.ask`) | §17.5 | shipped v0.4 (pre-release) |
| v0.2.0 GA: live Base Sepolia settlement against a real facilitator | §15 phase 40.4 | in progress, external resources gate |
| `did:ion` anchoring for key rotation | §4.1 | specified, not implemented |
| SLIP-10 / Ed25519 wallet chains (Solana, Stellar) | §14.1 | not started |
| NAT traversal / relay for non-publicly-addressable daimons | §16 (deferred) | not started |
| MLS group channels (multi-party sessions) | §9.3 (future) | not started |
| Public daimon directory / DHT discovery | §12 | not started |
| Labor-market wedge: post-task / agent-bid / escrow | §1 (v0.5) | not started |

## Roadmap and open research questions

The next six concrete milestones follow from the SPEC's open phases and the gaps in the current test surface. Three are implementation; three are research.

1. **v0.2.0 GA.** Run a live Base Sepolia settlement against a real x402-protected endpoint with a working facilitator. The cryptographic surface is self-tested via the CI smoke shard; the remaining gap is external infrastructure. Unblocks the stable tag for the wallet and payments surface and moves `daimon-protocol` 0.2.0 from pre-release to GA on both PyPI and npm.

2. **`did:ion` identity method.** `did:key` is self-contained but cannot rotate — a new key is a new identity. `did:ion` (Sidetree over Bitcoin) gives key rotation under a stable DID without reintroducing a vendor. Implementation gap. UX question: what is the right flow for a principal who rotates, and how does the address-book TOFU pin (§16.4) react when the DID stays stable but the transport pubkey has changed. This is partially a human-factors question, not only a cryptographic one.

3. **Relay for mobile daimons.** v0.3 assumes both ends of a channel bind a listener. A daimon running on a phone cannot. The natural fit is a Noise-IK-authenticated store-and-forward relay, but the design question is whether the relay operator can be reduced to a passive mix (seeing only encrypted payloads and unlinkable identifiers) without introducing unacceptable latency.

4. **Private preference prediction.** v0.1 records what the principal *states* about themselves (`memory write --kind preference "…"`). The next capability is predicting preferences they have not stated, from observed behavior across providers, without any single provider seeing enough to reconstruct the model. The open questions are: (a) what statistical model fits a few-shot personal preference learner that trains on the daimon side rather than the provider side, (b) how to export learned preferences to a new provider as context without leaking the training set, (c) whether differential privacy adds anything meaningful at the single-user scale where there is no population to hide in.

5. **Verifiable memory integrity across time.** Each memory row is signed; each activity entry is hash-chained. A third party cannot currently verify non-tamper without the principal's cooperation. Open question: what minimal public anchor (periodic hash commitment to an EVM contract? a Sigsum-style transparency log?) preserves the principal's privacy while giving an external verifier deterministic proof over a stated time window. The privacy-functional trade-off is the interesting part.

6. **Capability composition algebra.** Biscuit v3 tokens attenuate offline — a holder can add further restrictions without contacting the issuer, and the verifier runs a Datalog authorizer against ambient facts. The open question is what *algebra* of rights composes cleanly when a daimon chains delegations across three or four hops, with heterogeneous Datalog checks at each layer. The current implementation handles the single-hop case robustly; the multi-hop case is specified but under-tested.

## Repository layout

```
.
├── SPEC.md                   formal protocol spec (17 sections)
├── docs/
│   ├── ARCHITECTURE.md       data flows, component boundaries
│   ├── SECURITY.md           threat model, cryptographic anchors, limitations
│   ├── perf.md               measured benchmarks
│   └── systemd.md            running daimond under systemd
├── cmd/
│   ├── daimond/              the daemon process
│   ├── daimon/               CLI client
│   └── x402-mock-server/     test facilitator for the smoke suite
├── internal/
│   ├── identity/             Ed25519 did:key, keystore
│   ├── secretbox/            AES-256-GCM AEAD and envelope
│   ├── memory/               SQLite-backed encrypted store + retrieval
│   ├── activity/             BLAKE3-chained signed log
│   ├── provider/             Claude, OpenAI, Ollama, LM Studio adapters
│   ├── wallet/               BIP-39 / BIP-32 HD wallet
│   ├── payment/              EIP-712 / EIP-3009 payment client
│   ├── server/               JSON-RPC 2.0 verb surface
│   ├── did/web/              did:web resolver and endpoint signing
│   ├── transport/            Noise IK over TCP
│   ├── addressbook/          peer DID registry with TOFU pinning
│   ├── capability/           Biscuit v3 capability tokens
│   └── reputation/           Ed25519-signed receipts
├── sdk/
│   ├── python/               daimon-protocol on PyPI
│   └── typescript/           @daimon-protocol/sdk on npm
├── examples/
│   ├── streaming/            cross-language streaming reference
│   └── x402-smoke/           cross-language x402 payment reference
├── design/                   per-version design proposals (v0.2, v0.3, v0.4)
├── install.sh                one-liner installer with SHA-256 verification
├── Makefile                  build / test / ci-local / bench targets
├── QUICKSTART.md             zero-to-paid-x402 walkthrough
└── CHECKPOINT.md             current project state
```

## License

Apache 2.0. See [LICENSE](LICENSE).

## Author

Johannes Christian Koeleman ([@regitxx](https://github.com/regitxx)). Spec, reference implementation, SDKs, and all documentation in this repository. See the commit log for a precise accounting.
