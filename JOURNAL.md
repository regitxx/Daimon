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

---

## 2026-05-03 — Day Zero, third session

**SPEC §11 resolved.** All seven open questions answered. Defaults locked for v0.1:

| Question | Decision |
|---|---|
| Embedding model | `nomic-embed-text` via local Ollama (graceful degrade if absent) |
| Context budget | 2000 tokens default per `context.get`, per-request override |
| Context retrieval policy | `0.7 × cosine + 0.3 × exp(−age_days/30)` — deterministic |
| Retention | No auto-expiration. Deletion is user-initiated. |
| Multi-principal | Deferred. One principal per `daimond` process. |
| Streaming | SSE on HTTPS transport. Unix socket sync-only. |
| CLI surface | `daimon init / unlock / memory / provider / chat` |

Added `daimon.memory.delete` to the RPC surface as a consequence of the retention decision.

**Go skeleton in place.** Project compiles, `daimond` runs and prints banner.

- `go.mod` — module `github.com/regitxx/Daimon`, Go 1.22 minimum
- `cmd/daimond/main.go` — version banner, no functionality yet
- `Makefile` — build, test, fmt, vet, run, clean targets
- Verified: Go 1.26.1 on darwin/arm64. `make build` produces `bin/daimond` ~2.5 MB.

**Decision on Go module path:** chose `github.com/regitxx/Daimon` (capital D) to exactly match the GitHub repo URL. If we later rename the GH repo to lowercase, we update the module path with it. Convention prefers lowercase but exact match avoids resolution surprises.

**Next session begins with:** identity primitive in `internal/identity` — DID generation (Ed25519, did:key), encrypted keystore using Argon2id-derived key. Passkey/WebAuthn-PRF integration is v0.1.x. This unlocks everything else (signing memory writes, signing activity log, did:web `.well-known/agent.json` later).

---

## 2026-05-03 — Day Zero, fourth session: identity primitive landed

**`internal/identity` package shipped.** Four files, ~450 lines of Go, 8 tests passing.

**Files:**
- `did.go` — did:key encoding/decoding. multicodec prefix (`0xed 0x01` for ed25519-pub) + multibase base58btc + 'z' prefix. Includes `MultibaseFragment` helper for DID document construction.
- `keystore.go` — Argon2id (64 MiB / 3 iters / 4 parallel / 32-byte key) → AES-256-GCM. JSON keystore format with versioning. File mode 0600.
- `identity.go` — public API: `Generate`, `LoadFromKeystore`, `SaveToKeystore`, `DID`, `PublicKey`, `Sign`, `Verify`, `DIDDocument` (Ed25519VerificationKey2020 suite).
- `identity_test.go` — covers generate, sign/verify (with tampered-message rejection), did:key roundtrip, malformed did rejection, keystore roundtrip with 0600 perm check, wrong-password rejection, corrupted-ciphertext rejection, DID document JSON shape.

**Dependencies added:**
- `golang.org/x/crypto/argon2` — for Argon2id KDF
- `github.com/mr-tron/base58` — for did:key multibase encoding

**Wired into `cmd/daimond/main.go`:** running the binary now generates an ephemeral Ed25519 identity, prints its `did:key`, signs a test message, and verifies. End-to-end demonstration that the primitive compiles, links, and works.

**Sample DID from a demo run:** `did:key:z6MkgHPbnonFyfAaEqu3qbjPYb8NkENPmcfBxfMLvsv2FKkA` (the `z6Mk` prefix is canonical for Ed25519-based did:key — it's the multibase + multicodec encoding of the public key).

**Performance**: Argon2id at the SPEC §4.2 minimum parameters (64 MiB / 3 iters / 4 parallel) runs in 50–70ms per derivation on M-series Apple Silicon. Acceptable.

**Test runtime**: 1.17s for the full identity package suite. Fast enough.

**Decisions made this session:**
- DID document uses `Ed25519VerificationKey2020` suite (not the older 2018 form). Aligns with current W3C specs.
- Argon2id parameters match SPEC §4.2 minimums exactly. Hardcoded for v0.1; configurable later.
- Keystore is JSON with base64-encoded fields. Debuggable, portable across architectures. Format versioned.
- No passkey/WebAuthn integration in v0.1 — password-only. Passkey support is v0.1.x (requires a UI layer).
- Public API surface kept narrow. No exported field on `Identity` (private key is locked inside).

**Next session begins with:** memory primitive in `internal/memory` — SQLCipher-encrypted SQLite with `sqlite-vec` extension for vector search. Schema from SPEC §5.2. Memory writes signed by the identity (ties memory to identity at the cryptographic level). Test plan: write/read roundtrip, signature verification on read, semantic search behavior, export/import roundtrip with signature verification.

---

## 2026-05-03 — Day Zero, fifth session: memory primitive landed

**`internal/memory` package shipped.** Six files, ~1240 lines of Go (~810 implementation + ~430 tests). 14 memory tests + 8 identity tests, 22/22 pass in 0.27s.

**Files:**
- `memory.go` — `Memory` struct (matches SPEC §5.2), `Kind` enum, `SigningInput()` canonical form, `[]float32` ↔ little-endian bytes codec, metadata canonicalization.
- `embedder.go` — `Embedder` interface (`Embed`, `Dimensions`, `Name`) and `NullEmbedder`. The Ollama-backed embedder lands with provider work.
- `store.go` — `Open` / `Close`, `Write`, `Read`, `Delete`, `List`. SQLite schema applied at open. Every read verifies the row signature; tampered rows return `ErrSignatureFailed`.
- `search.go` — `Search` with two paths: cosine similarity over stored embeddings (when an embedder produces vectors) and substring fallback (NullEmbedder or query-embed failure). O(n) cosine in Go is fine for v0.1 scale; sqlite-vec slots in later without API change.
- `export.go` — `Export` / `Import` per SPEC §5.4. Document-level signature over canonical JSON of the doc with signature field nulled. Per-memory signatures verified independently against the DID embedded in the export. Default `ImportOptions` is safe (verify + idempotent skip on duplicate ID).
- `memory_test.go` — round-trip, tamper detection on read, missing-id, delete idempotency, kind/limit listing, substring search, cosine ranking with a stub embedder, export/import round-trip, tampered-document rejection, tampered-memory rejection (re-signs doc to prove per-memory check is what catches it), `SkipVerification` escape hatch, unknown-format rejection, and SigningInput determinism.

**Dependencies added:**
- `modernc.org/sqlite v1.50.0` — pure-Go SQLite, CGO-free
- `github.com/oklog/ulid/v2 v2.1.1` — ULIDs

**Wired into `cmd/daimond/main.go`:** the demo now runs all four steps end-to-end — generate identity, open memory store, write 3 signed memories, export, import into a *different* identity's fresh store. All cross-identity signatures verify. Binary grew 2.7 MB → 9.5 MB (modernc.org/sqlite is large but CGO-free).

**Decisions made this session:**

- **Pragmatism over strict spec on encryption.** SPEC §5.1 calls for SQLCipher; this session ships plain SQLite. The Open path is the only seam SQLCipher needs — schema, write/read, signing, export are all encryption-agnostic. Recording the deviation here so the spec and implementation are honest with each other; SQLCipher slots in next iteration.
- **No sqlite-vec yet.** v0.1 cosine search runs in Go over decoded embeddings. For the thousands-of-memories scale a single principal will hit, this is sub-10ms and avoids a CGO dependency. sqlite-vec arrives when the principal's memory size or query latency demands it.
- **Domain-separated signing input.** SPEC §5.2 says `(id || content || metadata)`. We sign `"daimon-memory-v1\x00" || id || "\x00" || content || "\x00" || metadata`. The version tag prevents future signature-domain collisions; the null separators eliminate ambiguity when fields are empty. Documented inline in `memory.go`.
- **Metadata canonicalization is "Go's `json.Marshal` of `map[string]any`"** — sorted keys, no whitespace, deterministic for primitive-bearing objects. Sufficient for Go-to-Go interop. Cross-language SDKs (TS, Python) will need RFC 8785 JCS or equivalent. Tracked.
- **Document-level export signature.** Sign the canonical JSON of the export document with its `signature` field nulled (using `omitempty`). Same canonical form on the verifier side. Stable across Go encoders; same caveat as metadata canonicalization for cross-language interop.
- **Cross-identity import is allowed by default.** SPEC §5.4 frames import as same-principal, but v0.1's verification is "do the signatures verify against the DID embedded in the document?" — which permits importing another principal's memories if the receiver chooses to. Policy lives above this layer. The demo exercises this path.
- **Default `ImportOptions{}` is safe.** Field names inverted (`SkipVerification`, `FailOnDuplicate`) so the zero value verifies signatures and silently skips duplicate IDs. Idempotent re-imports work out of the box.
- **Recency-weighted retrieval lives above this layer.** SPEC §11's `0.7·cosine + 0.3·exp(-age_days/30)` is the `daimon.context.get` policy. `memory.Search` exposes raw cosine — the context primitive will compose them.

**What we explicitly punted (in priority order for next session):**
1. SQLCipher at-rest encryption (CGO). Keystore key derivation already exists; pipe it through.
2. Activity log primitive (`internal/activity`) — append-only BLAKE3 hash chain.
3. Real Ollama embedder (`internal/memory/ollama.go`?) — drops into the `Embedder` interface seam.
4. RFC 8785 JCS canonicalization once cross-language SDKs land.

**Next session begins with:** SQLCipher integration *or* activity-log primitive — huckgod's call. SQLCipher is the spec-faithful path; activity log is the next net-new primitive. Both are blockers for the RPC server (which exposes signed reads, signed activity, and assumes encrypted storage by default).

---

## 2026-05-03 — Day Zero, sixth session: activity log primitive landed

**`internal/activity` package shipped.** Three files, ~800 lines of Go (~425 implementation + ~375 tests). 11 activity tests pass in 0.93s; full suite (identity + memory + activity) is 33 tests in ~2.8s.

**Files:**
- `activity.go` — `Entry` struct (matches SPEC §8.1), `Kind` enum (the seven v0.1 kinds from §8.2), `HashPrefix`/`HashSize`/`ZeroHash()`, canonical-bytes derivation. Hash: `"blake3:" + hex(BLAKE3-256(canonicalJSON))`. Signature: Ed25519 over the raw 32-byte hash digest (compact: signing 32 bytes is much cheaper than the full canonical bytes, and the hash already commits to the entry).
- `log.go` — `Log` struct, `Open`/`Close`/`Append`/`Query`/`Verify`/`LastHash`/`Path`. Storage is JSON Lines at SPEC §10's `activity.log` path with mode 0600. Each `Append` writes one line and `fsync`s before returning. A `sync.Mutex` serializes appends; `Query` and `Verify` open the file separately for read so they don't block writers.
- `log_test.go` — 11 tests: empty open + 0600 perm, genesis + chain link, clean-chain `Verify`, tampered-payload detection (rewrites a single entry, expects `ErrHashMismatch`), broken-chain detection (splices out the middle entry, expects `ErrChainBroken`), bad-signature detection (chain signed by id1 verified against id2, expects `ErrSignatureFailed`), kind/limit/since filters, persist-across-reopen with chain continuity, append-after-close errors, empty-kind rejection, and 8 goroutines × 25 appends concurrently with full chain verification at the end.

**Dependencies added:**
- `lukechampine.com/blake3 v1.4.1` — pure-Go BLAKE3
- (transitive: `github.com/klauspost/cpuid/v2`)

**Wired into `cmd/daimond/main.go`:** the demo now runs five steps end-to-end:
1. Generate identity
2. Open memory store + activity log
3. Append `daimon.created` genesis entry; for each of three memory writes, append a `memory.write` activity entry
4. Export memory document, append `memory.export` entry, re-import into a fresh-identity store and verify cross-identity signatures
5. Verify the activity-log chain end-to-end (5 entries, last_hash printed)

Binary: 9.5 MB → 9.8 MB (BLAKE3 + cpuid).

**Decisions made this session:**

- **Sign the hash, not the canonical bytes.** SPEC §8.1 specifies a `signature` field separate from `hash`; it doesn't dictate what gets signed. Signing the 32-byte BLAKE3 digest (instead of re-feeding the full canonical bytes through Ed25519) is faster and equivalent — the hash already commits to all data. Documented inline in `activity.go`.
- **Genesis prev_hash is `blake3:` + 32 zero bytes hex.** A sentinel that no real entry can collide with (BLAKE3 of any input cannot be all-zero in practice). Encoded explicitly via `ZeroHash()`.
- **JSON Lines, fsync per append.** SPEC §10 says `activity.log` (a file, not a database). JSONL is the natural format: one entry per line, append-only, trivially scannable, easy to inspect with `cat`/`jq`. fsync on every append costs ~1ms but guarantees durability — acceptable for v0.1 throughput.
- **Open does NOT verify the chain.** O(n) startup cost would be punishing for daimons with long histories. `Verify` is a separate explicit call. `Open` only walks the file once to recover `lastHash` so new appends chain correctly.
- **Hash prefix `"blake3:"` for hash agility.** Future migration to a different hash algorithm doesn't break the entry schema; verifiers reading old entries can dispatch on the prefix.
- **Concurrent Append is safe but ordered.** `sync.Mutex` serializes appends so the chain stays consistent under contention. The test exercises 200 concurrent appends and verifies the resulting chain end-to-end without error.
- **`Query` does not verify integrity.** Query is the hot path (UI listings, debug tools). Verification belongs in the explicit `Verify` call. Callers who need both call them in sequence.
- **Demo couples memory writes to activity entries at the orchestrator level**, not inside `memory.Store`. Keeping the packages independent means the RPC handler (next milestone) can decide the integration policy — mediated mode logs everything, direct mode lets the client choose.

**What we explicitly punted (in priority order for next session):**
1. SQLCipher at-rest encryption for the memory store. The keystore key derivation already exists; pipe it through `memory.Open`.
2. RPC server (`internal/server`) — JSON-RPC 2.0 over Unix socket. Three primitives are now in place; the protocol surface from SPEC §6.1 can be wired to them mechanically.
3. Real Ollama embedder for the `Embedder` interface seam in `internal/memory`.
4. Activity-log indexing for huge histories — irrelevant until daimons have run for months.

**Next session begins with:** RPC server *or* SQLCipher — huckgod's call. The RPC server is what makes daimon-core a *daemon* in the Unix sense (clients can talk to it); SQLCipher closes the spec deviation. RPC unblocks the first provider adapter (Claude) and the CLI; SQLCipher doesn't unblock anything new but makes the project spec-faithful.

---

## 2026-05-03 — Day Zero, seventh session: RPC server landed

**`internal/server` package shipped.** Four files, ~1,610 lines of Go (~960 implementation + ~645 tests). 18 server tests pass in ~1.5s; full suite (identity + memory + activity + server) is 51 tests in ~3s, race-clean.

This is the milestone that makes daimon-core a *daemon* in the Unix sense — clients can now talk to it over the wire.

**Files:**
- `jsonrpc.go` — JSON-RPC 2.0 framing primitives. `Request` (with `IsNotification()`), `Response`, `RPCError`, the standard codes (-32700/-32600/-32601/-32602/-32603), and Daimon-specific application codes (-32001 IdentityLocked, -32002 NotFound, -32003 SignatureFailed, -32004 InvalidKind, -32005 NotImplemented). Helpers for parse-error / invalid-request / success / error response construction.
- `server.go` — `Server` struct, `New` / `Listen` / `Serve(ctx) / `Close`. Per-connection serial dispatch via goroutine-per-conn; a server-level cancellable context drains in-flight handlers on Close. Stale-socket detection that probes with `net.Dial` before unlinking — refuses to clobber a socket another process is actively listening on. Socket file is `chmod 0600` immediately after bind.
- `handlers.go` — wires SPEC §6.1 to the primitives. Methods registered: `daimon.identity.get`, `daimon.memory.{write,read,search,delete,export,import}`, `daimon.context.get`, `daimon.activity.{append,query}`. Mediated-mode auto-logging: write/export/import/query each emit the corresponding activity entry. `mapMemoryError` and `mapActivityError` translate package-level sentinel errors to RPC error codes.
- `server_test.go` — fixture builds a fresh identity + memory + activity log + server on a temp socket. 18 tests covering: socket mode 0600, idempotent Close, all 10 method roundtrips (including export→import across two principals), `context.get` recency-weighted formula and kinds filter, parse-error framing, unknown-method dispatch, JSON-RPC version rejection, notification (no `id`) producing no response, persistent multi-request connections, and 80 concurrent writes across 8 clients with full activity-chain Verify at the end.

**Method surface (this build):**

| Method | Wired to |
|---|---|
| `daimon.identity.get` | `identity.Identity` |
| `daimon.memory.write` | `memory.Store.Write` (+ activity log `memory.write`) |
| `daimon.memory.read` | `memory.Store.Read` |
| `daimon.memory.search` | `memory.Store.Search` |
| `daimon.memory.delete` | `memory.Store.Delete` |
| `daimon.memory.export` | `memory.Store.Export` (+ activity log `memory.export`) |
| `daimon.memory.import` | `memory.Store.Import` (+ activity log `memory.import`) |
| `daimon.context.get` | `memory.Store.Search` + SPEC §11 recency formula |
| `daimon.activity.append` | `activity.Log.Append` |
| `daimon.activity.query` | `activity.Log.Query` (+ activity log `activity.queried`) |
| `daimon.provider.list` | not registered — CodeMethodNotFound |
| `daimon.provider.invoke` | not registered — CodeMethodNotFound |

**Wired into `cmd/daimond/main.go`:** new step `[6/6]` starts the RPC server on a temp Unix socket, makes one `daimon.identity.get` self-call, prints the round-trip result, and shuts the server down. Demo now exercises the full stack end-to-end. Binary: 9.8 MB → ~10.4 MB.

**Decisions made this session:**

- **JSON-RPC framing via `json.Decoder`/`json.Encoder` on the raw socket.** The spec doesn't mandate a wire framing; LSP-style Content-Length headers are overkill for a localhost socket, and newline-delimited would break if a future client sent pretty-printed JSON. Streaming JSON values is robust and obvious.
- **Per-connection serial dispatch.** A single connection processes one request at a time; concurrency comes from many connections. Parallel-on-one-connection (interleaved request IDs) is uncommon in JSON-RPC implementations and adds locking on the writer that we don't need yet.
- **Notification detection via `json.RawMessage` on the `id` field.** Distinguishing absent ("notification") from present-but-null requires looking at whether the JSON contained the key. Using `RawMessage` for `id` (instead of `any`) gives us that signal cleanly: `len(req.ID) == 0` ⇔ notification.
- **No batch support yet.** SPEC §6.1 doesn't require batches and no v0.1 client needs them. Single-request handling is half the code; batching is a clean future extension.
- **Mediated-mode auto-logging is the daimon's policy, not the client's.** SPEC §8 says "every meaningful action the daimon has taken" — *the daimon* decides. The handler appends to the activity log automatically on write/export/import/query. Client cooperation is not required for the audit trail.
- **Activity-append failure is logged, not propagated.** If a memory.write succeeds but the subsequent activity append fails, we log the error and still return success on the RPC. The alternative (failing the call) would induce client retries → duplicate memory entries → worse audit gap. Documented inline; transactional integrity arrives if/when SPEC §8 demands it.
- **`memory.delete` is NOT auto-logged.** SPEC §8.2's enumerated kinds for v0.1 don't include a `memory.delete` kind, and inventing kinds is a spec change. Worth adding in §8.2 next pass — deletion is plainly a meaningful action — but not in this session.
- **`activity.queried` is logged after the read.** SPEC §8.2 lists it as a kind. We append after returning the query result so the queried entry isn't visible in the response that triggered it; future queries see it. Yes, this means every introspection grows the log by one — that's the spec's intent.
- **Provider methods deliberately return `CodeMethodNotFound`, not a placeholder.** Honest signal that the method isn't in this build. They land with the provider-adapter primitive. We reserve `CodeNotImplemented` (-32005) for methods that exist in the dispatch table but are stubbed.
- **Stale-socket recovery probes with `net.Dial` before unlinking.** A Unix socket file left behind from a crashed process is unusable until removed; we detect that case (Dial → ECONNREFUSED) and clean up. We refuse to clobber a socket another process is actively listening on (Dial succeeds), and we refuse to delete a non-socket file at the same path.
- **`context.get` implements the SPEC §11 formula directly.** `score = 0.7·cosine + 0.3·exp(−age_days/30)`. Pulls 50 candidates from `memory.Search`, re-ranks with the recency boost, formats top-K under the token budget into a `[N] (kind) content` block. Token estimation is `chars/4` — coarse, but the per-model tokenizer arrives with the provider adapters that own it.
- **Output `Memory` is the internal struct.** Embedding bytes are included (base64 in JSON). For typical clients the embedding bloat doesn't matter, and clients that want to re-verify signatures need the full row. If a real client complains we add an opt-out flag.
- **macOS `sun_path` 104-byte limit.** `t.TempDir()` plus a long test name overflows it. Tests use `os.MkdirTemp("", "dmn")` for the socket directory specifically, leaving the heavier per-test files (DB, activity log) under `t.TempDir()`. The demo in `main.go` does the same.

**What we explicitly punted (in priority order for next session):**
1. SQLCipher at-rest encryption for the memory store. Closes the spec deviation; the keystore key derivation already exists, just pipe it through `memory.Open`.
2. Real Ollama embedder for the `Embedder` interface seam — unblocks cosine search in the demo and makes `context.get` non-trivial (currently uses substring fallback because demo runs with `NullEmbedder`).
3. First provider adapter (Claude) + the `daimon.provider.{list,invoke}` RPC methods. This is what makes mediated mode real — provider creds in the daimon, clients call through it.
4. JSON-RPC batch requests — straightforward addition once a real client demands them.
5. HTTPS + mutual TLS transport (SPEC §6 alternative for Windows / network mode).
6. SSE streaming on the HTTPS transport (SPEC §11; Unix socket stays sync-only in v0.1).

**Next session begins with:** SQLCipher *or* Ollama embedder *or* Claude adapter — huckgod's call. SQLCipher closes the spec gap. The Ollama embedder makes the existing demo more interesting (cosine instead of substring). The Claude adapter is the first real value flow — switch from synthetic demo to "a daimon in front of a real LLM." All three are self-contained and could go in any order; Claude is the one that produces the most user-visible "this is real" moment.

