# Daimon Protocol Specification

**Version**: v0.2 (Draft)
**Status**: v0.1.0 GA shipped to PyPI + npm 2026-05-12. v0.2.0-dev.1 pre-release shipped 2026-05-19 (wallet + x402 surface, with `show_mnemonic` re-display + `wallet recover` import on the seed-lifecycle side). v0.2 GA cuts once live Base Sepolia settlement is verified.
**Date**: 2026-05-19 (originally 2026-05-03 for v0.1)

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

### Out of scope for v0.1 and v0.2

- Federation (agent-to-agent communication across machines) → v0.3
- Reputation → v0.4
- Sub-agent delegation → v0.5
- Public DID resolution and verification by third parties → v0.3
- The daimon as a payment RECIPIENT (accepts x402 payments from other agents) → v0.3 federation territory; v0.2 is payer-only

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

daimon.wallet.show_mnemonic({
  password: string               // keystore password, re-verified against on-disk file
}) → { mnemonic: string[] }      // the 12- or 24-word BIP-39 phrase
```

The wallet keystore is auto-created by the unlock callback the first time `daimon unlock` runs. On that first unlock, the `daimon.identity.unlock` response carries a `mnemonic` field with the 24 BIP-39 words the daimon will use to derive all wallets — clients MUST surface this exactly once to the principal for backup. On subsequent unlocks the field is omitted. See §14 for the full wallet primitive.

`daimon.wallet.show_mnemonic` is the password-gated re-display: callers supply the keystore password and the daemon re-runs the full Argon2id + AES-GCM-decrypt against the on-disk keystore (NOT against the in-memory unlocked state). Wrong password surfaces as `CodeWrongPassword = -32008`, distinct from `CodeIdentityLocked = -32001` — the daemon IS unlocked, the password attestation is a separate check, and SDKs / CLIs MUST NOT rewrite the error as "run daimon unlock first" when they see `-32008`.

The symmetric import path — bringing an external 12- or 24-word BIP-39 phrase INTO a fresh daimon — is an offline operation that writes the keystore directly on disk, not an RPC verb. See §14.6.

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

## 12. What v0.1 + v0.2 explicitly do NOT solve

- Discovery of other agents
- Agent-to-agent communication
- ~~Payments~~ → **Outbound x402 payments shipped in v0.2 (§15).** The daimon as a payment recipient (accepts x402 from other agents) is still v0.3 federation territory.
- Reputation
- Capability delegation to sub-agents
- Public verifiability of memory or activity by third parties
- Cloud-hosted daimons (a daimon you don't run yourself)
- Custodial wallet mode (third-party holds keys on the principal's behalf) — v0.2's wallet primitive is strictly non-custodial; custodial integration could land in v0.2.x if there's a concrete user need (see design/v0.2-wallet.md §8.1)
- Non-EVM payment chains — v0.2 supports EVM (Base, Base Sepolia) only; SVM (Solana) and Stellar are reserved for v0.2.x once their facilitator ecosystems mature

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

**Next milestone for v0.2**: live Base Sepolia settlement against a real x402-protected endpoint with a real facilitator (phase 40.4 in the project's session log). Until then, v0.2.0-dev.1 is published on PyPI under `--pre` and npm under `@dev` for early adopters who want to experiment against local mock servers. v0.2.0 GA cuts once 40.4 verifies live settlement end-to-end.
