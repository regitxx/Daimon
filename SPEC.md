# Daimon Protocol Specification

**Version**: v0.3 (Draft)
**Status**: v0.1.0 GA shipped to PyPI + npm 2026-05-12. v0.2.0-dev.2 pre-release shipped 2026-05-19 (wallet + x402 surface). v0.2 GA gated on live Base Sepolia settlement (phase 40.4). v0.3 federation arc (phases 30–38) shipped 2026-05-21 — see §16.
**Date**: 2026-05-21 (v0.3 draft; v0.2 additions 2026-05-19; originally 2026-05-03 for v0.1)

---

## 1. Scope

### 1.1 v0.1 — local sovereign agent

v0.1 specifies the **local sovereign agent**: a single daimon running on a user's machine, holding their identity and memory, routing requests to LLM providers.

The intent of v0.1 is to deliver the smallest possible Daimon that provides *standalone value to a single user* without depending on any network effect. If v0.1 is useful when only one person uses it, the rest of the protocol can be built incrementally.

### 1.2 v0.2 — agent owns its own money

v0.2 adds two primitives that complete the "your agent acts on your behalf" promise:

- A **wallet** the daimon holds and signs with (one HD seed, N derived keypairs per chain — see §14)
- **x402 payments** — the daimon parses HTTP 402 responses, signs EIP-3009 `transferWithAuthorization` messages against the matching wallet, retries with `PAYMENT-SIGNATURE`, audits every step (§15)

The two compose: any client that uses `daimon.payment.pay` (or `daimon.provider.invoke` once 40.5b lands) can buy paid resources without ever handling private keys or signing logic itself.

### 1.3 v0.3 — agent-to-agent federation

v0.3 adds **federation**: the ability for daimons to find, authenticate, and communicate with each other across machines. The four shipped primitives:

1. **Transport**: Noise IK mutual-auth encrypted channels over TCP. The same Ed25519 keypair used for identity and audit-chain signing authenticates the transport — one key pair for everything (§16.2).
2. **Channel lifecycle**: `daimon.peer.dial`, `daimon.peer.close`, `daimon.peer.list`, `daimon.peer.listen` — open and manage peer connections (§16.3).
3. **Address book**: persistent, encrypted record of known peers with per-verb authorization control and TOFU pubkey pinning (§16.4).
4. **Served verbs**: the first three A2A protocol verbs — `peer.echo` (connectivity test), `peer.ask` (cross-daimon LLM invocation with address-book gate), `peer.pay.required` (x402 price discovery) — plus the generic `daimon.peer.invoke` passthrough (§16.5).

Single-player utility from v0.1 + v0.2 is fully preserved. The federation surface is opt-in: a daimon with no `peer listen` running cannot be dialed in; a daimon that never calls `peer.dial` never opens any outbound channels. v0.3 adds capability without breaking any v0.1/v0.2 invariant.

### Out of scope for v0.1 and v0.2 (updated for v0.3)

- ~~Federation (agent-to-agent communication across machines)~~ → **shipped in v0.3 (§16)**
- ~~Public DID resolution and verification by third parties~~ → **did:key transport shipped in v0.3; did:web resolution deferred to v0.3.x**
- ~~The daimon as a payment RECIPIENT~~ → **peer.pay.required price discovery shipped; on-chain settlement deferred to v0.3.x/phase 40.4**
- Reputation → v0.4
- Sub-agent delegation / Biscuit tokens → v0.4
- NAT traversal / mobile daimons → v0.3.x (requires relay; v0.3 assumes publicly addressable endpoints)
- Group encrypted channels (MLS) → v0.3.x
- Public daimon directory / DHT discovery → v0.4

These are deferred deliberately. They require network primitives that have no value until v0.1 + v0.2 are solid.

---

## 2. Terminology

| Term | Meaning |
|---|---|
| **Daimon** (capitalized) | The protocol. |
| **daimon** (lowercase) | An instance of the protocol running for a specific principal. |
| **daimon-core** | The reference implementation — the daemon process. |
| **Principal** | The human (or organization) the daimon represents. Exactly one principal per daimon in v0.1. |
| **Provider** | An external LLM or agent service (Claude, OpenAI, Gemini, Ollama, etc.). |
| **Client** | Any program that talks to daimon-core via the Daimon Protocol (CLI, IDE plugin, MCP host, web UI). |
| **Memory** | A signed, encrypted store of facts, preferences, observations, and task state belonging to the principal. |
| **Activity log** | An append-only, hash-chained record of every meaningful action the daimon has taken. |
| **Peer** | A remote daimon running for a different principal, reachable over the network. v0.3+. |
| **Channel** | An authenticated, encrypted TCP connection between two daimons established via Noise IK. Identified by a UUID `channel_id`. v0.3+. |
| **Address book** | A local, encrypted registry of peer DIDs with their trust status and per-verb authorization. v0.3+. |
| **TOFU** | Trust On First Use — the first connection to a DID pins its Ed25519 public key; subsequent connections verify against that pin. v0.3+. |

---

## 3. Architecture

```
┌──────────────────┐
│   Client         │  CLI, IDE plugin, MCP host, web UI, etc.
└────────┬─────────┘
         │
         │  Daimon Protocol (JSON-RPC 2.0)
         │  over Unix socket or localhost mTLS
         ▼
┌─────────────────────────────────┐
│   daimon-core                   │
│   Local daemon, single-tenant   │
│                                 │
│   ┌────────────────────────┐    │
│   │ Identity               │    │  did:key (+ optional did:ion)
│   ├────────────────────────┤    │
│   │ Memory                 │    │  Encrypted SQLite + vector index
│   ├────────────────────────┤    │
│   │ Activity log           │    │  Hash-chained, signed
│   ├────────────────────────┤    │
│   │ Provider adapters      │    │  Claude / OpenAI / Ollama (v0.1)
│   └────────────────────────┘    │
└────────┬────────────────────────┘
         │
         │  HTTPS to provider APIs
         ▼
┌──────────────────┐
│  LLM Provider    │
└──────────────────┘
```

The daimon-core is a long-running local process. It listens on a Unix socket (Linux/macOS) or localhost over mTLS (Windows or when explicit network access is enabled). All persistent state lives on the local filesystem, encrypted at rest with a key derived from a passkey or password unlock.

---

## 4. Identity

### 4.1 DID generation

Every daimon has a primary DID. v0.1 supports two DID methods:

- **did:key** *(default, required)*: generated from an Ed25519 keypair locally. Self-sovereign. No external dependency. Cannot be rotated — a new did:key is a new identity.
- **did:ion** *(optional)*: anchored to Bitcoin via Sidetree. Survives key rotation. Resolution requires ION node access.

A daimon MUST generate a `did:key` at creation. A daimon MAY anchor a `did:ion` for long-term identity stability.

### 4.2 Key storage

Private keys MUST be stored encrypted at rest. The encryption key MUST be derived from one of:

- (Preferred) a hardware-bound passkey (FIDO2/WebAuthn) using the PRF extension to derive a stable secret, OR
- A user password processed through Argon2id (memory ≥ 64 MiB, iterations ≥ 3, parallelism ≥ 4).

Plain-text key storage is non-compliant.

### 4.3 Key rotation

v0.1 supports identity key rotation only for `did:ion`. `did:key` rotation is out of scope (a new did:key is a new identity; principals MAY anchor multiple did:keys under a single did:ion).

---

## 5. Memory

### 5.1 Storage layer

- SQLite database at `$DAIMON_HOME/memory.db`. Pure-Go driver (modernc.org/sqlite); no CGO, single-binary distribution preserved.
- **At-rest confidentiality (v0.1):** application-level row encryption. The `content`, `metadata`, and `source` columns are stored as `version(1B) || nonce(12B) || AES-256-GCM(plaintext, AAD)`, with `AAD = "daimon-memory-row-v1" || 0x00 || row_id || 0x00 || field_name`. The AAD binds each ciphertext to its row and column so a stolen ciphertext cannot be silently moved onto another row or into another field. The `id`, `created_at`, `updated_at`, `kind`, `embedding`, and `signature` columns remain in clear: the signature is over plaintext (id ‖ content ‖ metadata), embeddings are a one-way function of plaintext content, and ids/timestamps/kinds are needed for indexing without unlock.
- **Encryption key derivation:** the 32-byte AES key is derived from the principal's Ed25519 seed via HKDF-SHA256 with info label `"daimon-memory-encryption-v1"`. The same identity always produces the same key (deterministic), so a memory store written in one process can be reopened in the next without storing the key on disk. The key never crosses the daimon's process boundary in plaintext: it is rederived in memory at unlock and held in the same trust scope (§9.2) as the unlocked private key.
- **SQLCipher (page-level encryption) is a v0.2+ option** if the threat model ever requires hiding row count, timestamps, kinds, indexes, WAL contents, or query patterns. The seam is `memory.Open`; switching engines does not change the public API. The v0.1 choice is deliberate: pure-Go single-binary distribution is preserved, and the v0.1 threat model (disk theft / backup exfiltration on top of OS-layer FDE) is covered by encrypting the user-supplied content material.
- Vector index: O(n) cosine in Go for v0.1 single-user scale. The `sqlite-vec` extension slots in via the same `Open` seam when scale demands it.

### 5.2 Memory schema (v0.1)

```sql
CREATE TABLE memories (
  id          TEXT PRIMARY KEY,        -- ULID
  created_at  INTEGER NOT NULL,        -- UNIX timestamp ms
  updated_at  INTEGER NOT NULL,
  kind        TEXT NOT NULL,           -- 'fact' | 'preference' | 'task' | 'observation'
  content     TEXT NOT NULL,           -- the memory text
  metadata    TEXT,                    -- JSON-encoded structured data
  embedding   BLOB,                    -- vector (model-dependent dimension)
  source      TEXT,                    -- which provider/client wrote this
  signature   BLOB NOT NULL            -- Ed25519 signature over (id || content || metadata)
);

CREATE INDEX idx_memories_kind     ON memories(kind);
CREATE INDEX idx_memories_created  ON memories(created_at);
-- vector index added by sqlite-vec extension
```

All memory writes MUST be signed by the daimon's identity key. Signatures MUST be verifiable by any holder of the public key.

### 5.3 Embedding model

v0.1 default: `nomic-embed-text` (768 dimensions) running locally via Ollama.

Rationale: keeps memory creation entirely local. No third-party API call required. The embedding model can be upgraded; the schema MUST tolerate variable embedding dimensions per row.

### 5.4 Export and import

Memory MUST be exportable as a single signed JSON document:

```json
{
  "format": "daimon-export-v1",
  "did": "did:key:z6Mk...",
  "exported_at": 1714780800000,
  "memories": [...],
  "activity": [...],
  "signature": "..."
}
```

Any conformant daimon MUST be able to import this format from any other daimon controlled by the same principal. Signature verification on import is REQUIRED.

---

## 6. The Daimon Protocol (RPC surface)

**Transport**: JSON-RPC 2.0 over either:

- Unix socket at `$DAIMON_HOME/daimon.sock` (Linux, macOS) — auth by filesystem permissions (mode 0600, owner-only)
- HTTPS at `localhost:7777` with mutual TLS — client cert provisioned via passkey unlock

External (non-localhost) access is NOT supported in v0.1. Federation arrives in v0.3.

### 6.1 Methods

#### Identity

```
daimon.identity.get() → {
  did: string,
  public_key: string (multibase),
  did_methods: string[]
}
```

#### Memory

```
daimon.memory.write({
  kind: "fact" | "preference" | "task" | "observation",
  content: string,
  metadata?: object,
  source?: string
}) → { id: string }

daimon.memory.read({ id: string }) → Memory

daimon.memory.search({
  query: string,
  limit?: number,        // default 10
  kind?: string
}) → Memory[]

daimon.memory.export() → ExportDocument

daimon.memory.import({
  document: ExportDocument,
  verify_signature: boolean  // default true
}) → { imported: number, skipped: number }

daimon.memory.delete({ id: string }) → { deleted: boolean }
```

#### Context

```
daimon.context.get({
  query: string,
  max_tokens?: number,   // default 2000
  kinds?: string[]
}) → {
  context: string,       // formatted for LLM prompt injection
  memory_ids: string[],  // memories that contributed
  token_estimate: number
}
```

The daimon decides what is relevant for a given query. Implementations MAY use semantic similarity, recency weighting, or learned policies. v0.1 default: cosine similarity + recency boost.

A successful standalone `daimon.context.get` call appends a `context.previewed` entry to the activity log with `{query, matched}` payload (SPEC §8.2), mirroring the self-audit shape of `activity.queried` / `activity.verified` — every meaningful action against the principal's data is itself logged. The inject-context-on-invoke path deliberately does NOT write this row: the `provider.invoke` entry already records `injected_memory_ids` for the same retrieval, and an additional `context.previewed` alongside it would double-log a single principal action. Empty match (`matched=0`) still appends; failure (search-layer error) does NOT, by the same reasoning that gates `activity.verified`'s success-only-append rule (§6.1).

#### Activity log

```
daimon.activity.append({
  kind: string,
  payload: object
}) → { id: string, hash: string }

daimon.activity.query({
  since?: number,
  kind?: string,
  limit?: number
}) → ActivityEntry[]

daimon.activity.verify() → {
  verified: number,         // count of entries that passed all three checks
  ok: boolean               // true iff verified == total entries on disk
}
```

`daimon.activity.verify` walks the log from genesis to the current head, asserting three properties for every entry: (1) `prev_hash` matches the previous entry's `hash`, (2) the stored `hash` recomputes from the canonical *plaintext* form (the encrypted payload is decrypted first, since the chain commits to plaintext per §8.1), and (3) `signature` verifies under the bound identity's public key. On success the daimon appends a new `activity.verified` entry to the log itself with `{verified: N}` payload, mirroring the `activity.queried` self-audit shape from §8.2 — every meaningful action against the log is itself logged. On failure (any of the three checks rejects an entry, or AEAD authentication fails on a tampered payload) the call returns a typed RPC error and **does NOT** append an `activity.verified` entry: extending a corrupt chain would compound the problem.

#### Provider routing

```
daimon.provider.list() → Provider[]

daimon.provider.invoke({
  provider: string,         // "claude", "openai", "ollama"
  request: ProviderRequest, // normalized: model, messages, tools, etc.
  inject_context?: {        // optional: ask daimon to enrich the prompt
    query: string,
    max_tokens?: number
  }
}) → {
  response: ProviderResponse,
  injected_memory_ids?: string[]   // present iff inject_context ran AND matched ≥1 memory
}

daimon.provider.stream({   // identical params to invoke
  provider: string,
  request: ProviderRequest,
  inject_context?: { query: string, max_tokens?: number }
}) → {
  response: ProviderResponse,           // final accumulated response, on the request id
  injected_memory_ids?: string[]
}
```

`daimon.provider.stream` is parallel to `invoke` for adapters that can render output incrementally. While the call is in flight, the daimon emits zero or more JSON-RPC 2.0 server-pushed **notifications** on the same connection:

```
{"jsonrpc": "2.0", "method": "daimon.provider.stream.delta",
 "params": {"content": "..."}}
```

Notifications carry no `id` field per JSON-RPC 2.0 — they precede the terminal response, which arrives on the original request's `id` and carries the same envelope as `daimon.provider.invoke` (the fully accumulated `ProviderResponse` under `response`, optional `injected_memory_ids`). Adapters that do not implement streaming return `CodeNotFound` with `"provider does not support streaming"`; clients SHOULD fall back to `daimon.provider.invoke` transparently. Streaming is opt-in per call: `invoke` remains the default unary contract.

The response envelope's optional `injected_memory_ids` field surfaces the IDs of memories the daimon folded into the prompt via `inject_context`. The field is OMITTED entirely (not present as an empty array) when `inject_context` was not supplied OR when retrieval ran but matched no memories — clients MUST treat absence and empty-array as equivalent for UX purposes (e.g. printing `matched=0`). The activity log entry for the call carries the same IDs in its payload (SPEC §8.1) — the response field is a convenience for clients that want to render "matched=N" without re-querying the audit trail.

Provider credentials never leave daimon-core. Clients invoke providers *through* the daimon, not around it.

#### Wallet (v0.2)

```
daimon.wallet.list() → Wallet[]

daimon.wallet.create({
  chain: string                  // e.g. "evm:base", "evm:base-sepolia"
}) → Wallet

daimon.wallet.address({
  chain: string
}) → { address: string }         // EIP-55 checksummed for EVM

daimon.wallet.sign({
  chain: string,
  digest_hex: string             // 32-byte digest, 0x-prefix optional
}) → { signature_hex: string }   // 65 bytes [r || s || v] for EVM

daimon.wallet.derive({
  chain: string,
  index?: number                 // BIP-44 HD index; default 0
}) → { chain, path, address, pubkey }
                                 // read-only: derives without persisting
                                 // (no audit row, no wallet-list mutation)

daimon.wallet.show_mnemonic({
  password: string               // keystore password, re-verified against on-disk file
}) → { mnemonic: string[] }      // the 12- or 24-word BIP-39 phrase
```

The wallet keystore is auto-created by the unlock callback the first time `daimon unlock` runs. On that first unlock, the `daimon.identity.unlock` response carries a `mnemonic` field with the 24 BIP-39 words the daimon will use to derive all wallets — clients MUST surface this exactly once to the principal for backup. On subsequent unlocks the field is omitted. See §14 for the full wallet primitive.

`daimon.wallet.show_mnemonic` is the password-gated re-display: callers supply the keystore password and the daemon re-runs the full Argon2id + AES-GCM-decrypt against the on-disk keystore (NOT against the in-memory unlocked state). Wrong password surfaces as `CodeWrongPassword = -32008`, distinct from `CodeIdentityLocked = -32001` — the daemon IS unlocked, the password attestation is a separate check, and SDKs / CLIs MUST NOT rewrite the error as "run daimon unlock first" when they see `-32008`.

The symmetric import path — bringing an external 12- or 24-word BIP-39 phrase INTO a fresh daimon — is an offline operation that writes the keystore directly on disk, not an RPC verb. See §14.6.

`daimon.wallet.derive` is the read-only "what address would I get?" verb: it computes the address that would be produced for `(chain, index)` without persisting anything or writing an audit row. Useful for verifying a recovered seed produces the expected address before calling `create` (compare derive's output at index 0 against an externally-known address like the user's MetaMask) and for pre-computing what address a future `create` at index N will yield.

#### Payment (v0.2)

```
daimon.payment.pay({
  url: string,
  method?: string,               // default "GET"
  headers?: { string: string },
  body_base64?: string,          // mutually exclusive
  body_text?: string,            // with body_base64
  ceiling_smallest_unit?: string,// decimal; refuse to sign over this
  validity_seconds?: number      // EIP-3009 validBefore window; default 300
}) → {
  status_code: number,
  response_headers: { string: string }, // allowlist: Content-Type,
                                         // Content-Length, Payment-Response
  response_body_base64: string,
  payment_response?: PaymentResponse    // parsed PAYMENT-RESPONSE header
}
```

`daimon.payment.pay` wraps the full x402 v2 retry handshake (§15). The daimon parses the 402's `PAYMENT-REQUIRED` header, picks a compatible `PaymentRequirements` row using its wallet keystore + chain registry, builds an EIP-3009 `transferWithAuthorization` against the matching wallet, enforces `ceiling_smallest_unit` BEFORE signing (an over-ceiling 402 NEVER produces a signature on the wire), retries the request with `PAYMENT-SIGNATURE`, and decodes the result. Two new typed RPC error codes surface for SDK consumers:

- `CodePaymentCeiling = -32006` — resource demanded more than the local ceiling
- `CodePaymentUnsupported = -32007` — no wallet matches the resource's payment requirements

### 6.2 Two integration modes

A client can use Daimon in either mode:

**Mediated mode** *(recommended)*: client calls `daimon.provider.invoke`. Daimon enriches with context, calls the provider, logs activity. Provider credentials live only in daimon. No provider sees the full picture.

**Direct mode**: client calls `daimon.context.get`, builds its own prompt, calls the provider directly using its own credentials, then calls `daimon.activity.append` and `daimon.memory.write` to record. Simpler to integrate with existing tools; weaker privacy guarantee.

Both modes MUST be supported by conformant implementations.

---

## 7. Provider adapters

### 7.1 Adapter interface

Each provider adapter MUST implement (Go signature, normative):

```go
type ProviderAdapter interface {
    Name() string
    Invoke(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
    Models() []ModelInfo
}
```

`ProviderRequest` carries a normalized form (model, messages, tools, response format) plus the daimon's injected context. The adapter translates to the provider's native API.

### 7.2 Adapters in v0.1

- **claude** — Anthropic Messages API
- **openai** — OpenAI Responses API (with Chat Completions fallback)
- **ollama** — local Ollama server, `/api/chat` endpoint (locally-pulled model list discovered via `/api/tags` at registration)

Provider credentials are stored at `$DAIMON_HOME/providers.json.encrypted`, encrypted with the same root key as identity.

---

## 8. Activity log

Append-only, hash-chained record of every meaningful action.

### 8.1 Entry format

```json
{
  "id": "01HXYZ...",          // ULID
  "ts": 1714780800000,
  "kind": "memory.write" | "provider.invoke" | "memory.export" | ...,
  "payload": "AaaBBBccc...",  // see "At-rest confidentiality" below
  "prev_hash": "blake3:...",  // hash of previous entry
  "hash": "blake3:...",       // hash of this entry's canonical form
  "signature": "ed25519:..."  // signature by daimon identity key
}
```

The chain forms a verifiable audit trail. v0.1 stores locally only. v0.4+ may publish hashes to an external anchor (Bitcoin, Ethereum, Filecoin) for tamper evidence.

**At-rest confidentiality (v0.1):** the `payload` field is encrypted at the application level. On disk it is the base64 encoding of `version(1B) || nonce(12B) || AES-256-GCM(plaintext_payload, AAD)`, with `AAD = "daimon-activity-payload-v1" || 0x00 || entry_id || 0x00 || "payload"`. The AAD binds each ciphertext to its entry id so a stolen ciphertext cannot be silently moved onto another entry. The `id`, `ts`, `kind`, `prev_hash`, `hash`, and `signature` fields remain in clear: `kind` and `ts` are needed for `daimon.activity.query` filtering without unlock, and `prev_hash` / `hash` / `signature` are needed for chain continuity recovery on Open and for tamper-evident verification.

**Hash chain semantics under encryption.** The per-entry `hash` and `signature` commit to the canonical *plaintext* form of the entry — i.e., `hash = BLAKE3(canonical_json(plaintext_entry))` where `plaintext_entry.payload` is the JSON of the original payload object, not the AEAD envelope. Verification on the encrypted log decrypts each entry's payload before recomputing the hash, so chain integrity holds across the encryption boundary. An attacker who tampers with the ciphertext fails AEAD authentication before the chain check runs (`ErrInvalidCiphertext`); an attacker who tampers with `prev_hash` or `hash` themselves still triggers `ErrChainBroken` / `ErrHashMismatch`.

**Encryption key derivation.** The 32-byte AES key is derived from the principal's Ed25519 seed via HKDF-SHA256 with info label `"daimon-activity-encryption-v1"`. The label is distinct from the memory store's `"daimon-memory-encryption-v1"` so the two stores have independent subkeys despite sharing the same root identity. The key never crosses the daimon's process boundary in plaintext: it is rederived in memory at unlock and held in the same trust scope (§9.2) as the unlocked private key. The threat model matches the memory store's (§5.1): disk theft / backup exfiltration on top of OS-layer FDE.

### 8.2 Logged kinds (v0.1 + v0.2)

| Kind | When | Since |
|---|---|---|
| `daimon.created` | Genesis row, written by `daimon init` immediately after keystore generation. Payload `{version, did}`. The chain root is always this kind; entry index 0 has `prev_hash = ZeroHash`. `daimon unlock` never mutates log shape — it just opens the existing log. | v0.1 |
| `memory.write` | Each memory write | v0.1 |
| `memory.export` | Each export | v0.1 |
| `memory.import` | Each import | v0.1 |
| `provider.invoke` | Each provider call (mediated mode) | v0.1 |
| `activity.queried` | Each query against the log itself | v0.1 |
| `activity.verified` | Each successful chain verification (count of entries verified) | v0.1 |
| `context.previewed` | Each standalone `daimon.context.get` call (`{query, matched}`); the inject-context-on-invoke path is recorded under `provider.invoke` with `injected_memory_ids` instead, to avoid double-logging a single action | v0.1 |
| `key.rotated` | did:ion key rotations | v0.1 |
| `wallet.created` | Each `daimon.wallet.create` call. Payload `{id, chain, address, pubkey}`. The path field is implementation detail of HD derivation and intentionally omitted from the audit row. | v0.2 |
| `payment.signed` | The daimon signed an EIP-3009 authorisation for an outbound x402 payment. Payload `{url, scheme, network, amount, asset, pay_to, from, valid_after, valid_before}` — the structural shape of the signed authorisation, not the signature bytes themselves (those ride in the PAYMENT-SIGNATURE header on the retry). | v0.2 |
| `payment.settled` | The retried request returned a 2xx response AND any PAYMENT-RESPONSE header indicated success. Payload includes the prior `payment.signed`'s structural fields plus `{status_code, transaction, payer}` from the settlement frame. | v0.2 |
| `payment.failed` | A payment did NOT settle. Reasons include: ceiling exceeded BEFORE signing (no `payment.signed` row precedes this one), retry transport failure, server-side rejection (non-2xx on retry), or `PAYMENT-RESPONSE.success == false`. Payload carries `{url, reason}` plus the structural fields when available. | v0.2 |

**Lifecycle invariant.** A daimon provisioned via `daimon init` produces a one-entry chain immediately: the genesis `daimon.created` row is appended before init returns. Every subsequent meaningful action (memory write/export/import, provider invocation, audit query/verify, context preview) extends the chain. `daimon activity verify` against a freshly-init'd daimon reports `verified 1 entries — chain ok`. The `--force` re-init flag removes the prior `activity.log` and `memory.db` (both are signed/encrypted under the discarded identity and unreadable by the new one), so the post-`--force` invariant matches the post-fresh-init invariant: exactly one entry, the new genesis. Adopters who use `daimon-core` programmatically without going through the `daimon init` CLI are responsible for writing the genesis row themselves; `daimon activity verify` does not require it (an empty-prefix chain still verifies), but the chain root carries no semantic meaning without it.

---

## 9. Security model

### 9.1 Threats in scope (v0.1)

- **Provider lock-in / surveillance**: by routing through daimon-core in mediated mode, no single provider sees the principal's full context. Memory stays local.
- **Local data theft of files at rest**: keys, memory, activity log, and provider credentials are encrypted at rest with passkey-derived keys. An attacker with filesystem access without the passkey gets ciphertext.
- **Tampering with memory or the log**: every write is signed; the log is hash-chained.

### 9.2 Threats out of scope (v0.1)

- A compromised provider returning malicious responses (partial mitigation: log review)
- Side-channel attacks on the local machine
- A compromised daimon-core process itself (TPM-backed remote attestation arrives v0.2+)
- Cross-daimon trust (no federation in v0.1)
- Memory inference attacks against the embedding index
- **Transient password exposure over the unlock IPC channel.** `daimon unlock` sends the keystore password over the Unix socket to `daimond serve`. The socket is mode 0600 so only the principal's UID can connect, but the password is briefly readable via `/proc/<daimond>/fd` or by a `ptrace`/`strace` on the daemon — i.e. by anything that can already compromise the daimon-core process, which §9.2 already places out of scope. Mitigation (CLI reads password from the controlling terminal and forwards an unlock token instead) is deferred to v0.1.x.

### 9.3 Cryptographic primitives

| Purpose | Primitive |
|---|---|
| Identity signatures | Ed25519 |
| Memory & activity hashing | BLAKE3 |
| At-rest encryption (identity keystore, v0.1) | AES-256-GCM via `internal/secretbox.NewAEAD`; ciphertext + Argon2id parameters wrapped in a JSON envelope |
| At-rest encryption (provider credentials, v0.1) | AES-256-GCM via `internal/secretbox.NewAEAD`; same JSON envelope shape as the identity keystore |
| At-rest encryption (memory rows, v0.1) | AES-256-GCM via `internal/secretbox.SealAAD` / `OpenAAD` (per-row random nonce, AAD-bound to row id + field; bytes-packed `version‖nonce‖ct` envelope) |
| At-rest encryption (activity payloads, v0.1) | AES-256-GCM via `internal/secretbox.SealAAD` / `OpenAAD` (per-entry random nonce, AAD-bound to entry id; bytes-packed envelope, base64-into-JSON-string on disk) |
| At-rest encryption (memory pages, v0.2+ option) | SQLCipher (deferred; see §5.1) |
| Key derivation (password → master) | Argon2id (≥64 MiB, ≥3 iters, ≥4 parallel) |
| Key derivation (master → subkeys) | HKDF-SHA256 with domain-separated info labels |
| Key derivation (passkey) | WebAuthn PRF extension |
| Future: agent-to-agent channels | Noise XX (v0.3) |
| Future: group sessions | MLS RFC 9420 (v0.3+) |

All four at-rest encryption sites share one AEAD primitive — `internal/secretbox.NewAEAD` — and the two AAD-bound sites (memory rows, activity payloads) additionally share one bytes-packed envelope (`SealAAD` / `OpenAAD`). The keystore and credentials carry their AES-GCM ciphertext inside an Argon2id-parameterised JSON envelope that the package does not model. Domain separation between the four sites is enforced at the AAD layer (memory/activity) and at the HKDF info-label layer (memory: `daimon-memory-encryption-v1`; activity: `daimon-activity-encryption-v1`); the keystore and credentials each derive their AEAD key from the user password directly via Argon2id, with independent salts.

---

## 10. File layout

```
$DAIMON_HOME/  (default: $XDG_CONFIG_HOME/daimon, or
                          ~/Library/Application Support/daimon on macOS,
                          %AppData%/daimon on Windows — whatever os.UserConfigDir()
                          returns; override via the DAIMON_HOME environment
                          variable)
├── identity.keystore         # Encrypted Ed25519 keystore (mode 0600)
├── memory.db                 # SQLite — content/metadata/source columns AES-256-GCM-encrypted (§5.1)
├── activity.log              # Append-only signed log — payload field AES-256-GCM-encrypted (§8.1)
├── providers.json.encrypted  # Provider credentials
├── daimon.sock               # Unix socket (mode 0600). Falls back to
│                             # $TMPDIR/daimon-$uid.sock when the resolved
│                             # path exceeds the AF_UNIX sun_path cap (104
│                             # bytes on darwin, 108 on linux).
├── daimon.log                # Daemon log file (where `daimon unlock` writes
│                             # the auto-spawned daemon's stdout/stderr)
└── config.toml               # Non-sensitive config
```

---

## 11. v0.1 defaults (resolved)

The following defaults are locked in for v0.1. Each was an open question; each now has a chosen answer with a brief rationale.

| Question | Decision | Rationale |
|---|---|---|
| Embedding model | `nomic-embed-text` (768 dim) via local Ollama | Keeps memory creation 100% local. No third-party API call. |
| Fallback if Ollama absent | Semantic search disabled; key-value memory still functions | A daimon must run on machines without local model infra. |
| Memory injection budget | 2000 tokens default per `context.get`, configurable per request | Reasonable default for most chat use. Adaptive logic is post-v0.1. |
| `context.get` policy | `score = 0.7 × cosine_sim + 0.3 × exp(−age_days/30)` | Simple, deterministic, predictable. Model-driven retrieval is post-v0.1. |
| Memory retention | No automatic expiration. Deletion is user-initiated via `daimon.memory.delete` | Daimon never deletes the principal's data without their action. |
| Multi-principal | Deferred. One principal per daimond process. Multiple principals = multiple processes with separate `$DAIMON_HOME`. | Keeps v0.1 simple. Multi-tenancy is a different security model. |
| Streaming | HTTPS transport supports Server-Sent Events. Unix socket synchronous-only in v0.1. | Streaming matters for chat UIs; SSE is sufficient. |
| CLI surface | `daimon init`, `daimon unlock`, `daimon memory`, `daimon provider`, `daimon chat`. Subcommand details in v0.1.1. | Establishes ergonomic shape; specifics iterate during implementation. |

---

## 12. What v0.1–v0.3 explicitly do NOT solve

- ~~Discovery of other agents~~ → **v0.3 shipped `daimon.federation.config` + address book; DHT/public directory deferred to v0.4**
- ~~Agent-to-agent communication~~ → **shipped in v0.3 (§16): peer.echo, peer.ask, peer.pay.required over Noise IK channels**
- ~~Payments~~ → **Outbound x402 payments shipped in v0.2 (§15). Price discovery for inbound payments shipped in v0.3 (peer.pay.required). On-chain inbound settlement is phase 40.4.**
- Reputation → v0.4
- Capability delegation to sub-agents (Biscuit tokens) → v0.4
- Public verifiability of memory or activity by third parties → future
- Cloud-hosted daimons (a daimon you don't run yourself) → future
- Custodial wallet mode — v0.2's wallet primitive is strictly non-custodial
- Non-EVM payment chains — v0.2/v0.3 support EVM (Base, Base Sepolia) only; SVM and Stellar reserved for v0.2.x+
- NAT traversal / mobile endpoints — v0.3 assumes publicly addressable TCP endpoints; relay service is v0.3.x
- Group encrypted channels (multi-party) — v0.3 is 1:1 only; MLS group channels are v0.3.x

These are by design. v0.1 + v0.2 must stand on their own as useful for one user before any network effects are introduced.

---

## 13. Implementation note

Reference implementation language: **Go**.
SDKs: **TypeScript** + **Python**.
License: **Apache 2.0**.

Initial repo layout:

```
daimon/
├── README.md
├── SPEC.md            (this file)
├── LICENSE
├── CHECKPOINT.md      (project state)
├── JOURNAL.md         (build log)
├── cmd/
│   └── daimond/       (the daemon binary)
├── internal/
│   ├── identity/
│   ├── memory/
│   ├── activity/
│   ├── providers/
│   └── server/        (RPC server)
├── pkg/
│   └── daimon/        (public Go SDK)
├── sdk/
│   ├── typescript/
│   └── python/
└── examples/
```

---

## 14. Wallet (v0.2)

### 14.1 Scope

A wallet is a chain-bound keypair the daimon holds and uses to sign messages on the principal's behalf. v0.2 introduces:

- **One BIP-39 mnemonic per principal** (24 words, 256 bits of entropy)
- **N HD-derived keypairs** off that mnemonic via BIP-32 / BIP-44 paths
- **EVM chains only** (`evm:base`, `evm:base-sepolia`, etc.) — all use `m/44'/60'/0'/0/i` with secp256k1, so a single seed phrase imports cleanly into MetaMask / Phantom / Rabby / any standard EVM wallet

SLIP-10 derivation for non-secp256k1 curves (Solana Ed25519, Stellar Ed25519) is reserved for v0.2.x.

### 14.2 On-disk format

`$DAIMON_HOME/wallet.keystore` — JSON envelope, file mode 0600, structurally identical to `identity.keystore`:

```json
{
  "version": 1,
  "kdf": "argon2id",
  "kdf_params": {"memory_kib": 65536, "iterations": 3, "parallelism": 4, "salt": "<b64>"},
  "cipher": "aes-256-gcm",
  "nonce": "<b64>",
  "ciphertext": "<b64>"
}
```

The encrypted payload, after Argon2id KDF + AES-256-GCM decrypt, is a JSON object:

```json
{
  "mnemonic": "abandon abandon ... about",
  "wallets": [
    {
      "id": "01K...",
      "chain": "evm:base",
      "path": "m/44'/60'/0'/0/0",
      "address": "0x...",            // EIP-55 checksummed
      "pubkey": "02...",             // 33-byte compressed secp256k1, hex
      "created_at": 1779100000000
    },
    ...
  ]
}
```

The mnemonic is the source of truth: every `Wallet` row can be re-derived deterministically from `mnemonic + path`. The `wallets[]` index exists to preserve the "which chains has this principal created a wallet for" state across daemon restarts; on Open the daimon decrypts the plaintext into memory and signs subsequent messages by re-deriving the private key from `mnemonic + path` on each sign call, zeroing the derived key immediately after use.

### 14.3 Lifecycle

- **Auto-create on first unlock.** When `daimon unlock` runs and finds no `wallet.keystore` at `$DAIMON_HOME/`, the unlock callback generates a fresh 24-word BIP-39 mnemonic, encrypts the empty `{mnemonic, wallets: []}` plaintext under the same Argon2id KEK as the identity keystore, and writes the file at mode 0600. The unlock RPC response carries a `mnemonic: string[]` field on that ONE call and never again. Clients MUST surface the mnemonic to the principal once for backup. The daemon does not retain a separate copy: losing the file AND the mnemonic is unrecoverable.

- **Subsequent unlocks** decrypt the existing keystore using the same password as the identity keystore. Wallet failures (missing file, corrupted, future-version format) are non-fatal: the daimon stays unlocked for everything else and surfaces `CodeInvalidRequest` ("wallet keystore not loaded") on every `daimon.wallet.*` RPC until repaired.

- **`daimon.wallet.create(chain)`** derives the next HD path for that chain's coin type (60 for EVM), persists the new wallet row, and writes a `wallet.created` audit row (§8.2). v0.2 enforces one wallet per chain label — `ErrChainAlreadyExists` for duplicates.

- **Signing** is per-call: the daimon re-derives the private key from `mnemonic + path`, signs the digest with ECDSA over secp256k1, returns the 65-byte `[r || s || v]` EVM signature with `v ∈ {27, 28}`, and zeros the derived key. The mnemonic itself stays in process memory between unlock and process exit (or `daimon.identity.lock`, post-v0.2).

### 14.4 EVM address derivation

For an EVM wallet derived at path `m/44'/60'/0'/0/i`:

1. Compute the secp256k1 private key from `BIP32(BIP39_seed(mnemonic, ""), path)`.
2. Derive the uncompressed public key: 65 bytes, leading `0x04` plus 32-byte X plus 32-byte Y.
3. `keccak256(uncompressed[1:])` — the 32-byte legacy-Keccak hash of the 64-byte XY body.
4. The Ethereum address is `hash[12:]` — the last 20 bytes, hex-encoded with `0x` prefix.
5. EIP-55 checksumming: re-hash the lowercase hex address; uppercase each character at position `i` where the `i`-th nibble of the recomputed hash is `≥ 8`.

The same address is valid on every EVM chain (Base, Mainnet, Polygon, Arbitrum, ...) — the `chain` label in the wallet record is the principal's declaration of intent, not a cryptographic property.

### 14.5 Test vector

The canonical BIP-39 12-word `abandon ... about` vector at `m/44'/60'/0'/0/0` derives to:

```
0x9858EfFD232B4033E47d90003D41EC34EcaEda94
```

The daimon's reference implementation anchors this in a test (`internal/wallet/wallet_test.go::TestDerive_EVMAddressMatchesPublishedVector`) so any future refactor that drifts the derivation pipeline trips CI immediately. Third-party Daimon-compatible implementations SHOULD include the same test.

### 14.6 Backup re-display and seed import

A non-custodial wallet is only as portable as the principal's ability to (a) re-verify the seed they're supposed to have written down and (b) bring an existing seed into a new daimon. Both operations are deliberately mediated through the principal's keystore password — neither the export nor the import shortcut around the cryptographic gate.

**Re-display (`daimon.wallet.show_mnemonic`).** Specified in §6.1. The reference implementation MUST re-run the full Argon2id KDF + AES-256-GCM authenticated-decrypt against the on-disk keystore on every call; it MUST NOT short-circuit by returning the in-memory mnemonic to anyone with socket access. The result is that "the daemon is unlocked" is not by itself sufficient to expose the seed — every re-display requires the password again. This matches the seed-reveal posture of MetaMask, Phantom, Trezor, Ledger Live, and every other reputable non-custodial wallet. Wrong password MUST surface as `CodeWrongPassword = -32008`, distinct from `CodeIdentityLocked = -32001`.

**Seed import (offline, no RPC).** The symmetric "I have a BIP-39 phrase from elsewhere, write a wallet keystore using THAT phrase" operation is performed offline by the CLI subcommand `daimon wallet recover`, not via an RPC verb. Rationale:

- A live seed swap on a running daimon would orphan every wallet derived from the previous mnemonic and leave the daemon's in-memory state desynchronised from the on-disk keystore — a half-state the cryptographic layer is not designed for.
- Anyone with the keystore file and the keystore password already has full control of the wallet; the offline tool requires both, so the security boundary is unchanged from the unlock path.

The reference implementation's `daimon wallet recover`:

1. MUST refuse if `$DAIMON_HOME/wallet.keystore` already exists. Overwriting an existing keystore would silently destroy any wallets derived from the previous seed; the principal must move the existing file out of the way explicitly first.
2. MUST validate the supplied phrase against BIP-39 (word count ∈ {12, 24}, all words in the BIP-39 English wordlist, checksum valid) BEFORE writing anything to disk.
3. MUST encrypt the keystore plaintext (`{mnemonic, wallets: []}`) under the same Argon2id + AES-256-GCM envelope a fresh keystore would use. The next `daimon unlock` then loads the file through the normal lifecycle path, opaque to the fact that the seed was imported rather than generated.

Third-party Daimon-compatible implementations SHOULD provide an equivalent offline import path. The on-disk format documented in §14.2 is canonical: any tool that writes a valid encrypted-keystore-file from a BIP-39 phrase + password is interoperable with the daimon daemon, regardless of which language or CLI surface it ships behind.

---

## 15. Payments / x402 (v0.2)

### 15.1 Scope

v0.2 ships **outbound x402 payments**: the daimon parses HTTP 402 responses, signs an EIP-3009 `transferWithAuthorization` against the matching wallet, retries the request with `PAYMENT-SIGNATURE`, and audits every step.

What v0.2 does NOT do:
- Accept x402 payments from other agents (the daimon as RECIPIENT) — v0.3 federation
- Talk to facilitators (`/verify`, `/settle`) — facilitator interaction is the resource server's concern; the daimon-as-payer just constructs valid PAYMENT-SIGNATURE headers
- Submit transactions on-chain itself — the resource server's facilitator does that

### 15.2 Wire format (x402 v2)

Reference: <https://docs.x402.org/core-concepts/http-402>. The daimon implements x402 v2 in full.

```
C ── GET /resource ────────────────────────────────────────────► S
C ◄── 402 Payment Required ────────────────────────────────────  S
       Header: Payment-Required: <base64(PaymentRequiredEnvelope)>

C ── GET /resource ────────────────────────────────────────────► S
       Header: Payment-Signature: <base64(PaymentPayload)>
C ◄── 200 OK ──────────────────────────────────────────────────  S
       Header: Payment-Response: <base64(PaymentResponse)>
       Body: <resource content>
```

`PaymentRequiredEnvelope` enumerates the server's accepted `(scheme, network, asset, amount)` tuples. The daimon picks the first row matching `scheme="exact" AND network in registry AND wallet for that chain exists AND asset == registered USDC contract`.

`PaymentPayload` for the EVM exact scheme:

```json
{
  "x402Version": 1,
  "scheme": "exact",
  "network": "base",
  "payload": {
    "signature": "0x<130 hex chars: r || s || v>",
    "authorization": {
      "from": "0x...",         // payer wallet address
      "to": "0x...",           // PaymentRequirements.PayTo
      "value": "100",          // PaymentRequirements.MaxAmountRequired (decimal)
      "validAfter": "0",
      "validBefore": "1779135503",  // unix seconds; default = now + 5 min
      "nonce": "0x<64 hex chars: 32 random bytes>"
    }
  }
}
```

### 15.3 EIP-712 + EIP-3009 hashing

The signature is over a 32-byte digest computed per EIP-712 §3:

```
digest = keccak256(0x1901 || domainSeparator || structHash)

domainSeparator = keccak256(abi.encode(
    keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
    keccak256(name),
    keccak256(version),
    chainId,
    verifyingContract
))

structHash = keccak256(abi.encode(
    keccak256("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"),
    from, to, value, validAfter, validBefore, nonce
))
```

All scalars are 32-byte right-padded big-endian per Solidity ABI; addresses are zero-padded on the left to 32 bytes; bytes32 is taken as-is.

The two type-string hashes are precomputed in the reference implementation:

```
EIP712Domain typehash:               0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f
TransferWithAuthorization typehash:  0x7c7c6cdb67a18743f49ec6fa9b35f50d52ed05cbed4cc592e13b44501c1a2267
```

Third-party implementations SHOULD verify their hashing pipeline produces these byte values for the canonical type strings.

### 15.4 Chain registry (v0.2)

The reference implementation hardcodes:

| x402 network | chain ID | USDC contract | EIP-712 name | EIP-712 version |
|---|---|---|---|---|
| `base` | 8453 | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` | `USD Coin` | `2` |
| `base-sepolia` | 84532 | `0x036CbD53842c5426634e7929541eC2318f3dCF7e` | `USDC` | `2` |

Adding chains (Polygon, Arbitrum, ...) or alternative tokens is a v0.2.x change; the wire format is unchanged, only the registry grows. Multi-token / arbitrary-asset support (where the resource server names any ERC-20 and the daimon fetches the EIP-712 domain via chain RPC) is reserved for v0.2.x.

### 15.5 Ceiling enforcement

The daimon-as-payer MUST refuse to sign authorisations whose value exceeds a per-payment ceiling. The ceiling is configured per-call via the `ceiling_smallest_unit` parameter (decimal string, smallest-unit of the asset — for USDC, `100000` ≡ $0.10). The check fires BEFORE signing — an over-ceiling 402 produces a `payment.failed` audit row with `reason="ceiling exceeded"` and ZERO `payment.signed` rows. A signed authorisation is a leak even if the SDK never transmits it (anyone holding the signature could replay it within `validBefore`); the ceiling is the principal's only defense against a malicious endpoint quoting enormous prices.

The reference implementation's typed RPC error for ceiling rejection is `CodePaymentCeiling = -32006`; third-party implementations SHOULD propagate this code.

### 15.6 Audit-row chain

A successful payment writes two consecutive rows to the activity log:

```
payment.signed   { url, scheme, network, amount, asset, pay_to, from, valid_after, valid_before }
                    ↓
payment.settled  { url, scheme, network, amount, asset, pay_to, payer, status_code, transaction }
```

A rejected payment writes ONE row (no `payment.signed`):

```
payment.failed   { url, reason, ... }
```

Both rows are chained + Ed25519-signed under the principal's identity key, walkable by `daimon.activity.verify`. Downstream auditors can reconstruct payment history without any privileged access.

---

**Next milestone for v0.2**: live Base Sepolia settlement against a real x402-protected endpoint with a real facilitator (phase 40.4 in the project's session log). Until then, v0.2.0-dev.2 is published on PyPI under `--pre` and npm under `@dev` for early adopters who want to experiment against local mock servers. v0.2.0 GA cuts once 40.4 verifies live settlement end-to-end.

---

## 16. Federation (v0.3)

v0.3 ships agent-to-agent federation: authenticated, encrypted channels between daimons with a progressively richer set of peer-served verbs. The entire federation surface is opt-in and backward-compatible — a daimon with no active listener is still a fully functional v0.1 + v0.2 daimon.

### 16.1 Design principles

**Cryptographic continuity.** A daimon's Ed25519 keypair (generated at `daimon init`, stored in the identity keystore) is the static key for BOTH the audit chain AND the Noise IK transport handshake. No separate "network identity" exists. The DID encodes the pubkey; the pubkey IS the transport credential. This is enforced in the reference implementation via `identity.MultibaseFragment(did)` → X25519 conversion for the Noise handshake, making the DID the single source of truth for all cryptographic claims.

**No new trust roots.** Discovery and authentication work entirely with user-owned primitives — manual (DID + endpoint sharing out-of-band), address book (explicit TOFU pinning), or did:web (user-controlled domain). No central directory, no third-party CA.

**Single-player utility preserved.** Every v0.3 verb fails gracefully or is simply absent on a daimon that hasn't been configured to listen. The federation surface is purely additive.

**Wire surface = extend the existing JSON-RPC.** All client-facing federation verbs (`daimon.peer.*`, `daimon.federation.*`) travel over the same JSON-RPC 2.0 Unix-socket protocol as v0.1 + v0.2 verbs. The daimon-to-daimon link uses the Noise-encrypted TCP channel internally; clients never see or speak Noise.

### 16.2 Transport: Noise IK over TCP

**Protocol**: [Noise IK](https://noiseprotocol.org/noise.html#interactive-handshake-patterns-fundamental) over plain TCP. Noise IK provides:
- Mutual authentication (both sides prove ownership of their static key)
- Forward secrecy (ephemeral session key generated per handshake)
- Initiator-authenticates-responder in round one (the initiator already knows the responder's DID → public key)

**Static keys**: the daimon's Ed25519 private key is converted to X25519 via the Bernstein key conversion (`Ed25519PrivateToX25519`). The peer's Ed25519 public key is converted similarly (`Ed25519PublicToX25519`). The conversion is deterministic and reversible at the public-key level — the same DID always produces the same X25519 key.

**Framing**: each JSON-RPC 2.0 message is sent as a length-prefixed frame over the Noise `*transport.Conn`. Frame format: `uint32 big-endian length || JSON payload`. The length field covers only the JSON payload bytes (not itself). Maximum frame size is 16 MiB; implementations MUST reject frames exceeding this limit.

**Handshake**:
1. Initiator (dialing daimon) derives the responder's X25519 pubkey from the peer's DID.
2. Initiator sends Noise IK `-> e, es, s, ss` (first handshake message).
3. Responder sends `<- e, ee, se` (second handshake message).
4. Both sides enter transport mode; subsequent frames are encrypted with ChaCha20-Poly1305.

**Listener**: started via `daimon.peer.listen`. The listener loop accepts inbound TCP connections, performs the Noise IK responder handshake, and dispatches inbound peer.* verb calls via `dispatchPeer`. The listener is idempotent — calling `daimon.peer.listen` while already listening returns the existing bound address.

**v0.3 constraints**:
- TCP only (no QUIC in v0.3; QUIC stream multiplexing is v0.3.x).
- IPv4 and IPv6 are both supported via `net.Listen("tcp", addr)`.
- NAT traversal is not provided. The listening daimon MUST be reachable at the advertised endpoint address. Daimons behind residential NAT or firewall that block inbound TCP cannot be dialed in.

### 16.3 Channel lifecycle

A channel is an established Noise IK TCP connection between two daimons, identified by a UUID `channel_id` assigned at dial time. The dialing daimon holds the channel in memory for the lifetime of the daemon process (or until `close` is called).

#### 16.3.1 `daimon.peer.listen` — start inbound listener

```
daimon.peer.listen({
  addr?: string          // TCP bind address, e.g. "0.0.0.0:9999" or "127.0.0.1:0"
                         // Scheme prefix "tcp://" is accepted and stripped.
                         // Default: "0.0.0.0:0" (OS-assigned ephemeral port).
}) → {
  endpoint: string       // Actual bound address, e.g. "tcp://0.0.0.0:54321"
}
```

Authorization: post-unlock only (the Noise static key is the daimon's identity private key). Idempotent: a second call while already listening returns the existing `endpoint` unchanged.

Audit row: `peer.listen.started { endpoint }`.

After this call, `daimon.federation.config` reflects the bound address in `public_endpoint`.

#### 16.3.2 `daimon.peer.dial` — open outbound channel

```
daimon.peer.dial({
  did: string,           // Remote daimon's DID. did:key only in v0.3.
  endpoint: string       // Remote TCP address, e.g. "tcp://host:port".
                         // Required: no automatic DID resolution in v0.3.
}) → {
  channel_id: string,    // UUID for subsequent peer.* calls
  peer_did: string,
  opened_at: string      // RFC3339
}
```

On dial, the reference implementation:
1. Extracts the peer's Ed25519 pubkey from the `did:key` DID fragment.
2. Converts to X25519 for the Noise IK initiator role.
3. Performs the Noise IK handshake over TCP.
4. Auto-populates the address book with the peer as `first_seen` if not already present; records a TOFU `transport_pubkey_multibase` observation.

Audit row: `peer.channel.opened { channel_id, peer_did, endpoint, tofu_warn? }`.

Error codes: `CodePeerUnreachable (-32010)` on network/handshake failure; `CodeInvalidParams (-32602)` for an unresolvable DID.

#### 16.3.3 `daimon.peer.close` — close a channel

```
daimon.peer.close({
  channel_id: string
}) → {}
```

Closes the underlying TCP connection and removes the channel from the in-memory map. Subsequent calls with the same `channel_id` return `CodeNotFound (-32601)`.

Audit row: `peer.channel.closed { channel_id, peer_did }`.

#### 16.3.4 `daimon.peer.list` — enumerate open channels

```
daimon.peer.list() → {
  channels: [
    { channel_id, peer_did, opened_at: RFC3339 }
  ]
}
```

Read-only. Returns an empty array if no channels are open.

#### 16.3.5 `daimon.peer.invoke` — invoke any peer verb

Low-level passthrough: serialize a JSON-RPC 2.0 request, send it over an open channel, and return the peer's raw JSON result.

```
daimon.peer.invoke({
  channel_id: string,
  method: string,        // e.g. "peer.echo", "peer.ask", "peer.pay.required"
  params?: object        // forwarded verbatim to the peer
}) → {
  result: any            // raw JSON result from the peer, preserved verbatim
}
```

If the peer returns a JSON-RPC error, `daimon.peer.invoke` propagates it as `CodeInternalError (-32603)` with the peer's error message embedded.

If the underlying TCP connection is broken during send or receive, the channel is automatically removed from the in-memory map and `CodePeerUnreachable (-32010)` is returned.

Audit row: `peer.invoke.sent { channel_id, peer_did, method }`.

### 16.4 Address book and trust model

The address book is a persistent, encrypted file at `$DAIMON_HOME/address_book.json.enc`, keyed under the identity's HKDF subkey (same pattern as `memory.db` row keys). It records every peer the daimon has talked to or the user has explicitly configured.

#### 16.4.1 Entry schema (wire format)

```json
{
  "did": "did:key:z6Mk...",
  "pet_name": "alice",           // optional human label
  "status": "first_seen",        // "first_seen" | "pinned" | "blocked"
  "approved_verbs": ["peer.ask", "peer.pay.required"],
  "transport_pubkey_multibase": "z6Mk...",  // TOFU-pinned X25519 pubkey (multibase)
  "first_seen": "2026-05-21T00:00:00Z",
  "last_seen": "2026-05-21T00:00:00Z"
}
```

#### 16.4.2 Status lifecycle

| Status | Meaning | Inbound request handling |
|---|---|---|
| `first_seen` | Auto-added on first dial; not yet explicitly trusted | Allowed only for universally-authorized verbs (peer.echo, peer.pay.required) |
| `pinned` | User has explicitly approved this peer's DID | Allowed for the verbs listed in `approved_verbs` |
| `blocked` | User has explicitly refused this peer's DID | All inbound requests rejected with `CodePeerUnauthorized (-32013)` |

#### 16.4.3 TOFU pubkey pinning

When a dial succeeds, the peer's X25519 pubkey (as a `z6Mk...` multibase string) is recorded in `transport_pubkey_multibase`. On subsequent dials to the same DID, if the observed pubkey differs, a TOFU warning is included in the `peer.channel.opened` audit row. The connection is NOT aborted (the Noise handshake already authenticated the actual key), but the mismatch is flagged for the user. Full enforcement (abort on TOFU mismatch) is v0.3.x.

#### 16.4.4 RPC verbs

**`daimon.peer.address_book.list`**
```
daimon.peer.address_book.list() → {
  entries: [addressBookEntryWire, ...]
}
```

**`daimon.peer.address_book.add`**
```
daimon.peer.address_book.add({
  did: string,                         // required
  pet_name?: string,
  transport_pubkey_multibase?: string  // optional TOFU seed
}) → addressBookEntryWire
```
Adds the peer at status `first_seen`. Returns `CodeAlreadyExists` if the DID is already present.

Audit row: `peer.address_book.added { did, pet_name? }`.

**`daimon.peer.address_book.pin`**
```
daimon.peer.address_book.pin({
  did: string,       // must already be in the address book
  verbs: string[]    // e.g. ["peer.ask", "peer.pay.required"]
}) → addressBookEntryWire
```
Promotes the entry to `pinned` and sets `approved_verbs`. The DID must already exist in the book; use `add` first if not.

Audit row: `peer.address_book.pinned { did, verbs }`.

**`daimon.peer.address_book.block`**
```
daimon.peer.address_book.block({
  did: string
}) → addressBookEntryWire
```
Sets status to `blocked`. All inbound requests from this DID will be rejected. The DID must already be in the book.

Audit row: `peer.address_book.blocked { did }`.

**`daimon.peer.address_book.unblock`**
```
daimon.peer.address_book.unblock({
  did: string
}) → addressBookEntryWire
```
Sets status back to `first_seen` (removes the block). The DID must already be in the book.

Audit row: `peer.address_book.unblocked { did }`.

**`daimon.peer.address_book.remove`**
```
daimon.peer.address_book.remove({
  did: string
}) → {}
```
Removes the entry entirely. Subsequent `add` calls for the same DID start fresh.

Audit row: `peer.address_book.removed { did }`.

### 16.5 Served peer verbs

Peer verbs are served on the **inbound** side: when a remote daimon dials in and sends a JSON-RPC 2.0 request over the Noise channel, the serving daimon's `dispatchPeer` router handles it.

Authorization model:
- **Universally authorized** (`peer.echo`, `peer.pay.required`): served to any peer that successfully completes the Noise IK handshake, regardless of address book status.
- **Address-book gated** (`peer.ask`): the calling peer's DID MUST be in the address book with status `pinned` AND the verb MUST appear in `approved_verbs`. A missing or `first_seen` peer, or a pinned peer without the verb, receives `CodePeerUnauthorized (-32013)`.
- **Blocked peers**: any inbound request from a `blocked` DID receives `CodePeerUnauthorized (-32013)` before reaching the verb handler.

The serving daimon resolves the calling peer's DID by extracting its Ed25519 pubkey from the Noise handshake static key via reverse X25519→Ed25519 lookup (not implemented in v0.3 — the calling peer's X25519 key is recorded in audit rows; full DID resolution from X25519 is v0.3.x; the authorization check uses a direct X25519 pubkey comparison against address book entries).

#### 16.5.1 `peer.echo`

Connectivity test verb. Reflects the caller's message back with the serving daimon's DID attached. Universally authorized.

Wire (sent by calling peer → serving daimon):
```json
{ "jsonrpc": "2.0", "method": "peer.echo", "params": { "message": "hello" }, "id": "..." }
```

Response:
```json
{ "jsonrpc": "2.0", "result": { "message": "hello", "from_did": "did:key:z6Mk..." }, "id": "..." }
```

Audit row on serving side: `peer.invoke.received { method: "peer.echo", peer_x25519: "..." }`.

#### 16.5.2 `peer.ask`

Cross-daimon LLM invocation. The calling peer asks the serving daimon to invoke one of its configured LLM providers. Authorization: address-book `pinned` + `peer.ask` in `approved_verbs`.

Wire (sent by calling peer):
```json
{
  "jsonrpc": "2.0",
  "method": "peer.ask",
  "params": {
    "provider": "claude",
    "request": {
      "messages": [{ "role": "user", "content": "Write a haiku." }],
      "model": "claude-opus-4-5",
      "max_tokens": 256
    }
  },
  "id": "..."
}
```

Response:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "response": {
      "content": "...",
      "model": "claude-opus-4-5",
      "stop_reason": "end_turn",
      "usage": { "input_tokens": 14, "output_tokens": 23 }
    }
  },
  "id": "..."
}
```

`inject_context` is NOT supported in `peer.ask` v0.3 — the calling peer builds its own retrieval context before asking.

Audit row on serving side: `peer.invoke.served { peer_did, peer_x25519, provider, model, input_tokens, output_tokens, stop_reason, duration_ms }`.

#### 16.5.3 `peer.pay.required`

Price discovery verb: returns the x402 `PaymentRequirements` for a named service. Universally authorized (price discovery must be available before payment can be set up — requiring authorization would be circular).

Wire (sent by calling peer):
```json
{
  "jsonrpc": "2.0",
  "method": "peer.pay.required",
  "params": { "service": "peer.ask" },
  "id": "..."
}
```

Response:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "requirements": [{
      "scheme": "exact",
      "network": "base-sepolia",
      "max_amount_required": "1000000",
      "resource": "peer.ask",
      "description": "1.00 USDC payment required to invoke peer.ask on this daimon (Base Sepolia testnet)",
      "pay_to": "0xABCD...",
      "max_timeout_seconds": 300,
      "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
    }]
  },
  "id": "..."
}
```

In v0.3: only `"peer.ask"` has payment requirements. All other services return `CodeInvalidParams`. The `pay_to` address is the serving daimon's Base Sepolia wallet address (derived from its BIP-32 HD wallet, chain `evm:base-sepolia`). If the serving daimon has no EVM wallet, returns `CodePeerProtocolUnsupported (-32012)`.

Audit row on serving side: `peer.payment.invoiced { service, peer_x25519, pay_to, amount, network, asset }`.

### 16.6 Federation config

`daimon.federation.config` lets a client introspect the local daimon's federation advertisement — what it can be called as, what it serves, where to reach it.

```
daimon.federation.config() → {
  did: string,                          // this daimon's did:key DID
  transport_pubkey_multibase: string,   // Ed25519 pubkey (z6Mk...), identical to DID fragment
  did_methods: string[],                // DID methods this daimon resolves; ["did:key"] in v0.3
  protocols: string[],                  // peer.* verbs served: ["peer.echo","peer.ask","peer.pay.required"]
  public_endpoint?: string,             // "tcp://host:port" if peer.listen is running; omitted otherwise
  federation_version: string            // "v0.3-draft" until GA
}
```

The `transport_pubkey_multibase` field is always identical to the fragment of `did` after `"did:key:"`. They encode the same key. Both are provided for clients that pre-fetch the pubkey for Noise handshake setup without needing to parse the DID themselves.

No audit row (read-only, non-mutating).

### 16.7 Activity log kinds (v0.3)

New activity kinds added in v0.3. All rows follow the same hash-chain + Ed25519-signature format as v0.1/v0.2 (§8).

| Kind | Written by | When | Payload fields |
|---|---|---|---|
| `peer.listen.started` | local daemon | `daimon.peer.listen` succeeds | `endpoint` |
| `peer.channel.opened` | dialing daemon | `daimon.peer.dial` succeeds | `channel_id`, `peer_did`, `endpoint`, `tofu_warn?` |
| `peer.channel.closed` | local daemon | `daimon.peer.close` | `channel_id`, `peer_did` |
| `peer.invoke.sent` | dialing daemon | `daimon.peer.invoke` returns result | `channel_id`, `peer_did`, `method` |
| `peer.invoke.received` | serving daemon | any inbound peer.* verb dispatch | `method`, `peer_x25519` |
| `peer.invoke.served` | serving daemon | `peer.ask` completes successfully | `peer_did`, `peer_x25519`, `provider`, `model`, `input_tokens`, `output_tokens`, `stop_reason`, `duration_ms` |
| `peer.payment.invoiced` | serving daemon | `peer.pay.required` call received | `service`, `peer_x25519`, `pay_to`, `amount`, `network`, `asset` |
| `peer.payment.received` | serving daemon | inbound x402 settlement confirmed | reserved for phase 40.4 |
| `peer.address_book.added` | local daemon | `address_book.add` | `did`, `pet_name?` |
| `peer.address_book.pinned` | local daemon | `address_book.pin` | `did`, `verbs` |
| `peer.address_book.blocked` | local daemon | `address_book.block` | `did` |
| `peer.address_book.unblocked` | local daemon | `address_book.unblock` | `did` |
| `peer.address_book.removed` | local daemon | `address_book.remove` | `did` |

Both the dialing and serving daimon independently write their own audit rows. The two audit chains collectively provide a full log of every cross-daimon interaction without either side depending on the other's audit data.

### 16.8 Typed error codes (v0.3)

New JSON-RPC error codes introduced in v0.3, extending the v0.1/v0.2 table in §6.2:

| Code | Constant | Meaning |
|---|---|---|
| `-32010` | `CodePeerUnreachable` | Could not establish a channel — DNS failure, TCP refused, Noise handshake failure, or broken connection during an active call. |
| `-32011` | `CodePeerAuthFailed` | Noise handshake completed but the peer's static X25519 key did not match the expected DID. Reserved; not yet returned in v0.3 (TOFU mismatch is a warning, not an abort). |
| `-32012` | `CodePeerProtocolUnsupported` | The peer (or the local daimon, for wallet-gated verbs) does not support the requested method or lacks the prerequisite configuration (e.g., no EVM wallet for `peer.pay.required`). |
| `-32013` | `CodePeerUnauthorized` | The address book entry for the peer DID does not authorize the requested verb, OR the peer is `blocked`. |

### 16.9 v0.3 constraints and non-goals

The following are explicitly deferred to v0.3.x or later:

- **NAT traversal**: the `peer.listen` endpoint must be publicly reachable. A relay service that lets NAT'd daimons receive inbound connections is v0.3.x.
- **did:web resolution**: the DID document + endpoint advertisement mechanism. v0.3 uses only `did:key` (offline, manual endpoint sharing). did:web resolution (fetch `https://domain/.well-known/did.json`, verify endpoint signature) is v0.3.x.
- **QUIC transport**: TCP is the v0.3 transport. QUIC (stream multiplexing, connection migration, 0-RTT) is v0.3.x.
- **Full TOFU enforcement**: a TOFU mismatch (pubkey changed since first sight) is recorded as a warning but does not abort the dial in v0.3. Hard abort is v0.3.x.
- **`peer.ask` pay-per-call gating**: the address book authorizes `peer.ask` per-DID with no economic throttle. x402 gating of `peer.ask` (where the calling peer pays per invocation) is v0.3.x.
- **On-chain inbound settlement**: `peer.pay.required` surfaces price requirements; settlement proof is phase 40.4 (shared with v0.2 GA).
- **Group channels / MLS**: v0.3 is 1:1 daimon-to-daimon only.
- **Multi-tenant daimons**: one principal per daemon process, same as v0.1.
- **Streaming peer.ask responses**: `peer.ask` collects the full LLM response before framing. Streaming is v0.3.x.

### 16.10 v0.3 GA criteria

v0.3.0 GA is gated on:

1. Phases 30–38 all green (shipped 2026-05-21 ✅).
2. Cross-daimon smoke in CI — two daemon processes on different ephemeral ports, `peer.dial` → `peer.echo` → `peer.ask` in the `x402-smoke` shard (planned).
3. Real-world dogfood: two daimons on separate machines communicating for one full week without manual intervention.
4. SPEC §16 review and sign-off (this section).

Current status: **phases 30–38 shipped, CI cross-daimon smoke planned, dogfood pending.**
