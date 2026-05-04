# Daimon Protocol Specification

**Version**: v0.1 (Draft)
**Status**: Day Zero. Subject to substantial revision before v0.1 freeze.
**Date**: 2026-05-03

---

## 1. Scope of v0.1

v0.1 specifies the **local sovereign agent**: a single daimon running on a user's machine, holding their identity and memory, routing requests to LLM providers.

The intent of v0.1 is to deliver the smallest possible Daimon that provides *standalone value to a single user* without depending on any network effect. If v0.1 is useful when only one person uses it, the rest of the protocol can be built incrementally.

### Out of scope for v0.1

- Federation (agent-to-agent communication across machines) → v0.3
- Payments → v0.2
- Reputation → v0.4
- Sub-agent delegation → v0.5
- Public DID resolution and verification by third parties → v0.3

These are deferred deliberately. They require network primitives that have no value until v0.1 is solid.

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
```

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
}) → ProviderResponse
```

Provider credentials never leave daimon-core. Clients invoke providers *through* the daimon, not around it.

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
  "payload": { /* kind-specific */ },
  "prev_hash": "blake3:...",  // hash of previous entry
  "hash": "blake3:...",       // hash of this entry's canonical form
  "signature": "ed25519:..."  // signature by daimon identity key
}
```

The chain forms a verifiable audit trail. v0.1 stores locally only. v0.4+ may publish hashes to an external anchor (Bitcoin, Ethereum, Filecoin) for tamper evidence.

### 8.2 Logged kinds (v0.1)

| Kind | When |
|---|---|
| `daimon.created` | First boot |
| `memory.write` | Each memory write |
| `memory.export` | Each export |
| `memory.import` | Each import |
| `provider.invoke` | Each provider call (mediated mode) |
| `activity.queried` | Each query against the log itself |
| `key.rotated` | did:ion key rotations |

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
| At-rest encryption (memory rows, v0.1) | AES-256-GCM, application-level row encryption (per-row random nonce, AAD-bound to row id + field) |
| At-rest encryption (memory pages, v0.2+ option) | SQLCipher (deferred; see §5.1) |
| Key derivation (password → master) | Argon2id (≥64 MiB, ≥3 iters, ≥4 parallel) |
| Key derivation (master → subkeys) | HKDF-SHA256 with domain-separated info labels |
| Key derivation (passkey) | WebAuthn PRF extension |
| Future: agent-to-agent channels | Noise XX (v0.3) |
| Future: group sessions | MLS RFC 9420 (v0.3+) |

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
├── activity.log              # Append-only signed log
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

## 12. What v0.1 explicitly does NOT solve

- Discovery of other agents
- Agent-to-agent communication
- Payments
- Reputation
- Capability delegation to sub-agents
- Public verifiability of memory or activity by third parties
- Cloud-hosted daimons (a daimon you don't run yourself)

These are by design. v0.1 must stand on its own as useful for one user before any network effects are introduced.

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

**Next milestone**: working `daimond` binary that initializes a daimon-core, generates an identity, accepts RPC, and writes/reads memory. Provider adapters next.
