# Architecture

This document complements [SPEC.md](../SPEC.md) with a reader-oriented view of the system: what the components do, how data flows between them, and what invariants hold at each boundary. The SPEC is the authoritative specification; this is the map.

## 1. Process model

A daimon is a single long-running local process (`daimond`) plus any number of short-lived clients (`daimon` CLI, Python/TypeScript SDK programs). Clients speak JSON-RPC 2.0 to the daemon over a Unix socket at `$DAIMON_HOME/daimon.sock` with file mode 0600. External network access to the daemon is not supported; federation (v0.3+) uses a separate TCP listener for the Noise IK transport, not the client socket.

```
                 ┌──────────────┐
                 │  daimon CLI  │    one-shot: dial socket, call verb, exit
                 └──────┬───────┘
     ┌─────────────────┼──────────────────┐
     │                 │                  │
┌────▼─────┐      ┌────▼─────┐       ┌────▼─────┐
│ SDK (Py) │      │ SDK (TS) │       │  other   │    long-lived programs
└────┬─────┘      └────┬─────┘       └────┬─────┘
     │                 │                  │
     └─────────────────┼──────────────────┘
                 ╔═════▼═══════╗
                 ║  daimond    ║    long-running, single-tenant
                 ║  (daemon)   ║    one principal per process
                 ╚═════╤═══════╝
                       │
             ┌─────────┼──────────┐
             │                    │
     HTTPS to providers    TCP + Noise IK to peer daimons (v0.3+)
```

Invariants:

- **One principal per daemon process.** Multi-principal is a different security model and is deferred (§11 of the SPEC).
- **The daemon unlocks exactly once per process.** After unlock, the identity private key, the memory encryption subkey, and the wallet mnemonic sit in process memory. They do not touch disk unencrypted and are not shared across processes.
- **Clients are stateless.** All durable state lives under `$DAIMON_HOME`; a client can crash and the daimon is unaffected.

## 2. On-disk layout

```
$DAIMON_HOME/
├── identity.keystore          Ed25519 keystore (Argon2id KEK + AES-GCM payload), mode 0600
├── wallet.keystore            BIP-39 mnemonic + per-chain wallet index, same envelope shape, mode 0600
├── memory.db                  SQLite — content / metadata / source columns AES-GCM-encrypted (§5.1)
├── activity.log               append-only JSONL — payload field AES-GCM-encrypted (§8.1)
├── capability.db              SQLite — issued / received tokens, receipts, token_calls counter
├── address_book.db            SQLite — DID → (transport pubkey, authorizations, trust state)
├── providers.json.encrypted   provider credentials (AES-GCM under identity KEK)
├── daimon.sock                Unix socket, mode 0600
├── daimon.log                 daemon stdout/stderr
└── config.toml                non-sensitive config (ports, provider endpoints, embedder choice)
```

The default resolution of `$DAIMON_HOME` follows `os.UserConfigDir()`: `$XDG_CONFIG_HOME/daimon` on Linux, `~/Library/Application Support/daimon` on macOS, `%AppData%/daimon` on Windows. The environment variable `DAIMON_HOME` overrides.

File permissions are enforced at write: the daemon fails startup if the keystore or the socket are readable by anyone but the owning UID. The permission check is deliberately conservative — a loosened permission is treated as a security event, not a warning.

## 3. Component boundaries

Each internal package has a narrow surface with one owner.

| Package | Owns | Contract |
|---|---|---|
| `internal/identity` | the Ed25519 keypair, the keystore envelope, `did:key` serialization | `Open(path, password) → Identity` yields a struct with the raw private key, signer, and multibase-encoded `did:key`. |
| `internal/secretbox` | AES-256-GCM AEAD primitives and envelope shape | `NewAEAD(key []byte)`, `SealAAD(key, plaintext, aad)`, `OpenAAD(key, envelope, aad)`. One module, used by every at-rest site. |
| `internal/memory` | SQLite schema, per-row encryption, retrieval (substring + cosine) | writes are signed by the identity; search returns `Memory[]` ordered by score. |
| `internal/activity` | append-only JSONL, BLAKE3 chain, per-entry AEAD on payload | `Append(kind, payload)` writes one row; `Verify()` walks the chain and surfaces the first mismatch. |
| `internal/wallet` | mnemonic generation, BIP-32 derivation, address formatting | derivation is deterministic from mnemonic + path; private keys are zeroed after each sign. |
| `internal/payment` | EIP-712 hashing, EIP-3009 authorisation, x402 client with pre-signing ceiling | returns typed errors for ceiling breach (`-32006`), unsupported scheme (`-32007`), wrong password on `show-mnemonic` (`-32008`). |
| `internal/server` | the JSON-RPC verb surface, unlock gate, handler wiring | `Serve(opts)` binds the socket and routes verbs. All state-modifying verbs are locked behind the unlock gate. |
| `internal/did/web` | parse / fetch / verify `did:web` documents, sign `DaimonEndpoint` payloads | small, pure. |
| `internal/transport` | Ed25519↔X25519 conversion, Noise IK handshake, TCP framing | `Dial(remoteDID, addr)` returns an authenticated bidirectional channel; the identity Ed25519 key is reused. |
| `internal/addressbook` | persistent peer registry, TOFU pinning, per-verb grants | `Grant(did, verb)` and `Pin(did, pubkey)` are the mutations; `Lookup(did, verb)` is the authorization probe. |
| `internal/capability` | Biscuit v3 token issue / attenuate / verify + SQLite state | `calls_used` counter is the only stateful piece; the token itself is self-describing. |
| `internal/reputation` | Ed25519-signed `Receipt`, canonical JSON | signed over all fields except `Signature`, in declaration order. |
| `internal/provider/*` | per-provider adapters, streaming | `Invoke(ctx, req) → Response` or `Stream(ctx, req) → chan Delta`. |

The dependency graph is deliberately shallow: no cycles, no central "core" package that everything imports. The RPC server composes the components; the components do not know about each other.

## 4. Representative data flows

### 4.1 Unlock

```
daimon unlock                             # CLI
  → read password from the controlling terminal (no echo)
  → JSON-RPC: daimon.identity.unlock({password})
  → daimond:
      • Argon2id(password, keystore.kdf_params) → KEK
      • AES-256-GCM decrypt keystore.ciphertext under KEK → Ed25519 seed
      • HKDF-SHA256(seed, info="daimon-memory-encryption-v1")  → memory subkey
      • HKDF-SHA256(seed, info="daimon-activity-encryption-v1") → activity subkey
      • Argon2id + AES decrypt wallet.keystore (same password path) → mnemonic
      • store the three subkeys and the mnemonic in process memory
      • append activity row kind="identity.unlocked"
  → response: {did, mnemonic?}        mnemonic present only on the first unlock
```

The three subkeys above derive deterministically from the identity seed, which is itself only in memory. Nothing in this flow writes any key material to disk.

### 4.2 Memory write with retrieval

```
daimon memory write --kind fact "I run Linux on an M1 MacBook via Asahi."
  → daimon.memory.write({kind, content, metadata?, source?})
  → daimond:
      • sign (id || content || metadata)  with the identity private key
      • encrypt content, metadata, source under the memory subkey
        with AAD = "daimon-memory-row-v1" || row_id || field_name
      • optionally compute an embedding via the configured embedder
        (default: nomic-embed-text over the local Ollama HTTP endpoint)
      • INSERT INTO memories
      • append activity row kind="memory.write", payload encrypted under activity subkey
  → response: {id}

# Later: chat with retrieval.
daimon chat --once "What OS am I on?"
  → daimon.provider.invoke({model, messages, inject_context:true})
  → daimond:
      • daimon.context.get(query=last user message)
          - compute query embedding
          - rank memories by 0.7·cosine + 0.3·exp(-age_days/30)
          - return top-k under the token budget
      • splice retrieved memories into the system prompt
      • forward to the chosen provider
      • stream or return the response
      • append activity row kind="provider.invoke" with injected_memory_ids
```

### 4.3 x402 payment

```
daimon payment pay --ceiling-usd 0.10 https://example.com/paid
  → daimon.payment.pay({url, ceiling_smallest_unit, chain?})
  → daimond:
      • GET url  →  402 Payment Required + payment manifest (scheme, network, asset, amount, pay_to, valid_after, valid_before)
      • if amount > ceiling: return -32006 CodePaymentCeiling — no signature is ever produced
      • if scheme != "exact" or network not in registry: return -32007 CodePaymentUnsupported
      • re-derive the signing key from mnemonic + wallet path
      • build EIP-712 TypedData using the pinned domain hash + transferWithAuthorization typehash
      • sign; immediately zero the derived private key
      • append activity row kind="payment.signed" with structural fields (no signature bytes)
      • retry: GET url with PAYMENT-SIGNATURE header
      • on 2xx (+ PAYMENT-RESPONSE.success): append activity row kind="payment.settled"
      • on any other outcome: append kind="payment.failed"
  → response: either the paid resource bytes, or a typed error
```

Two invariants make this flow safe for the principal:

- **Ceiling is enforced before signing.** An over-budget authorisation never produces a signature. The activity log can therefore never contain a `payment.signed` row whose amount exceeds the per-call ceiling.
- **Private keys are zeroed after each sign.** Derivation is pure (`mnemonic + path`), so the daemon holds the private key only for the handful of microseconds between derivation and the Go runtime's zero-after-use.

### 4.4 Cross-daimon `peer.ask`

```
daimon-A:  peer.dial(did_B, tcp://...)
  → Noise IK handshake over TCP
  → address book on A records (did_B, transport_pubkey_B, direction=outbound)
  → address book on B records (did_A, transport_pubkey_A, direction=inbound)

daimon-A:  peer.ask(channel_id, model, messages, capability_token?)
  → daimon-B receives the request on the Noise channel
  → authorization check (in order):
      1. is did_A blocked? → reject (-32010 CodePeerBlocked)
      2. is capability_token present and valid for this verb/model? → allow
      3. is did_A pinned with grant=peer.ask?                      → allow
      4. otherwise                                                 → reject (-32015 CodeCapabilityRequired)
  → if capability token present: verify Biscuit against ambient facts
       (time, peer_ask_model, calls_used — incremented atomically from capability.db)
  → provider.invoke on daimon-B's provider stack
  → optionally sign and attach a reputation.Receipt (if request_receipt=true)
  → activity rows on both sides: kind="peer.ask.sent" / kind="peer.ask.served"
```

The capability-gate ordering (blocked → token → pinned → reject) is a protocol-level invariant, not an implementation detail. A blocked DID cannot smuggle a capability token through; a valid token substitutes for the address-book pin, which is how the single-token-handoff pattern works.

## 5. Encryption at rest, in one picture

```
            password
               │
               ▼
          Argon2id            parameters: memory ≥ 64 MiB, iterations ≥ 3, parallelism ≥ 4
        (identity salt)       stored in keystore JSON envelope
               │
         KEK (32 bytes)
               │
               ▼
     AES-256-GCM decrypt
 of keystore.ciphertext
               │
               ▼
     Ed25519 seed (32 B)         ─────► signing / did:key / transport key (via Ed25519→X25519)
               │
               ▼
     HKDF-SHA256 (seed, info=…)
          │         │
          │         └────────────► "daimon-activity-encryption-v1"  → activity payload AEAD subkey
          │
          └──────────────────────► "daimon-memory-encryption-v1"    → memory row AEAD subkey
```

The wallet mnemonic is stored under its own keystore envelope with the same password path (Argon2id → AES-GCM). The ciphertexts in `memory.db` and `activity.log` additionally carry per-row/per-entry AAD (§9.3 of the SPEC and docs/SECURITY.md) so a stolen ciphertext cannot be moved onto another row or entry without failing authentication.

Four derivation facts are worth stating explicitly:

1. **Deterministic subkey derivation.** The memory and activity AEAD subkeys are fully determined by the Ed25519 seed. A fresh process re-derives the same subkeys from the same keystore + password with no on-disk key material.
2. **Domain separation.** The HKDF info labels are distinct per subkey. Compromise of one subkey does not weaken the others.
3. **No key escrow.** The daemon has no recovery path for a lost password. The design is explicit: losing the keystore file *and* the password is unrecoverable; losing the wallet keystore *and* the mnemonic is unrecoverable.
4. **Password rotation preserves state.** `daimon rotate-password` re-wraps both keystores under a new KEK without touching the Ed25519 seed or the mnemonic, so the identity, the memory chain, and the wallet addresses all survive.

## 6. Activity log semantics

The activity log is a single JSONL file at `$DAIMON_HOME/activity.log`, append-only, mode 0600. Each entry has the shape

```
{
  "id":        "01HXYZ...",                      ULID
  "ts":        1714780800000,
  "kind":      "memory.write" | "payment.signed" | ... ,
  "payload":   "<base64 of version || nonce || AES-GCM(payload, AAD)>",
  "prev_hash": "blake3:<hex>",
  "hash":      "blake3:<hex>",
  "signature": "ed25519:<hex>"
}
```

The hash commits to the canonical form of the *plaintext* entry (payload as an object, not as the AEAD envelope), so verification decrypts before rehashing and the chain integrity is preserved across the encryption boundary. Three distinct failure modes are detected deterministically:

- ciphertext tampering fails AEAD authentication before the hash check runs (`ErrInvalidCiphertext`);
- hash tampering fails the recomputed-hash check (`ErrHashMismatch`);
- link tampering fails the prev-hash check (`ErrChainBroken`).

`daimon activity verify` walks the whole log and reports either `verified N entries — chain ok` or the index of the first failure with the specific error. The set of `kind` values is enumerated in §8.2 of the SPEC and adds rows as new surfaces ship (wallet, payments, federation, capability, reputation).

## 7. Federation (v0.3) at a glance

Noise IK is the chosen handshake for three reasons:

1. **Mutual authentication with a known responder.** The dialer must know the responder's static pubkey up front, which fits the daimon trust model: you dial a DID you already know.
2. **Forward secrecy.** The handshake derives an ephemeral session key; a compromise of the long-term identity key does not retroactively decrypt past channels.
3. **Zero round-trip for the first message.** IK reaches 1-RTT with transport encryption active from the first application payload. Daimon dials are latency-sensitive; this matters.

The transport pubkey is the identity Ed25519 key converted to X25519 (`internal/transport/keyconv.go`). One keypair authenticates identity, audit-log signatures, and transport. The address book pins the X25519 pubkey on first use (TOFU) and verifies it on each re-dial; a pubkey rotation requires the principal to re-pin (which is intentional — rotation should be visible).

## 8. Capability tokens (v0.4) at a glance

A capability token in Daimon is a Biscuit v3 token that carries a set of right facts and (optionally) Datalog constraints. The constraints verified by the responder are:

- `time(now)` must satisfy the token's `check if time($t)` clauses, if present (`valid_until`).
- `peer_ask_model($model)` must match the token's model restriction, if present.
- `calls_used($n)` must stay below the token's `max_calls`, if present. The `calls_used` fact is produced by the responder from the `capability.db::token_calls` counter at verification time and incremented atomically before the invocation runs.

Attenuation is offline: a holder can append a block of further `check if` constraints without issuer contact. The resulting token still verifies against the original root pubkey, but under the tighter Datalog policy. This is what makes the single-token-handoff pattern work: Alice issues a capability to Bob; Bob attenuates it to Carol with a narrower scope; Carol invokes against Alice's daimon with the attenuated token; Alice's daimon verifies only the original root pubkey and the composed Datalog.

Revocation is explicit and is performed by the issuer: `daimon.capability.revoke({token_id})` writes an `issued_tokens.revoked_at` timestamp; `Verify` short-circuits to `-32014 CodeCapabilityDenied` on any revoked token. There is no CRL distribution — revocation is checked on the issuer side, which is the only side that knows the token's full history.

---

Pointers to the formal specification and the source are in the component table above. Anything not yet specified (relay, MLS group channels, `did:ion` anchoring, private preference learning) is enumerated in the roadmap section of the top-level README.
