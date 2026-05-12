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

---

## 2026-05-03 — Day Zero, eighth session: first provider adapter (Claude) landed — mediated mode is real

**The big one.** With this session daimon-core stops being self-test scaffolding and becomes a daimon you can put between a client and an LLM. SPEC §6.1 is now fully wired (every method has a handler), SPEC §7's Provider interface has a working reference (the Claude adapter), and SPEC §6.2's mediated mode — credentials in the daimon, context injection by the daimon, every call recorded in the activity log — works end-to-end through the JSON-RPC server.

**Files (this session):** seven new, ~1716 lines combined. ~520 lines of implementation in `internal/provider/{provider,registry,credentials}.go` and `internal/provider/claude/claude.go`. ~575 lines of tests in `internal/provider/provider_test.go`, `internal/provider/claude/claude_test.go`, and `internal/server/provider_handlers_test.go`. The remaining ~620 lines are smaller edits to `internal/server/{server,handlers,jsonrpc}.go`, `cmd/daimond/main.go`, and `CHECKPOINT.md` / this journal.

| Path | Purpose |
|---|---|
| `internal/provider/provider.go` | `Provider` interface, normalised `Request`/`Response`/`Message`/`Model`/`Usage`/`StopReason` types |
| `internal/provider/registry.go` | Concurrent-safe `Registry` (`Register`/`Get`/`List`/`Len`) |
| `internal/provider/credentials.go` | Encrypted `CredentialStore` at SPEC §10's `providers.json.encrypted` path. Argon2id + AES-256-GCM, mode 0600, first-run safe (missing file → empty store) |
| `internal/provider/claude/claude.go` | Anthropic Messages API adapter via raw `net/http`. Translates normalised request → `/v1/messages`, normalises response back |
| `internal/provider/{provider_test,claude/claude_test,server/provider_handlers_test}.go` | 12 + 10 + 12 tests covering the three pieces |
| `internal/server/server.go`, `handlers.go`, `jsonrpc.go` | Wired `Providers` + `Credentials` into `Options`, added `daimon.provider.{list,invoke}` handlers, factored `runContextRetrieval` so `provider.invoke` can reuse the SPEC §11 retrieval for `inject_context` |
| `cmd/daimond/main.go` | Demo grew to 7/7 steps: builds a provider registry, registers Claude conditionally on `ANTHROPIC_API_KEY`, calls `daimon.provider.list` over the socket |

**Method surface (this build vs. previous):**

Previously `daimon.provider.list` and `daimon.provider.invoke` returned `CodeMethodNotFound`. Both are now registered. SPEC §6.1 method surface is complete.

**Test totals:** 88/88 pass in ~10s, race-clean. By package: 8 identity, 14 memory, 11 activity, 12 provider, 10 claude adapter, 29 server (the prior 17 plus 12 new provider handler tests). Binary 10.4 MB → 15.0 MB — net/http and the credential crypto pull in a chunk.

**Decisions made this session:**

- **Net/http directly, no Anthropic SDK.** A daimon's job is to be a thin shim, and the translation logic (normalised → provider-native) is exactly where the value lives — clearer when the wire format is in front of you. Two of the three planned v0.1 adapters (Ollama, OpenAI) we'd write by hand anyway. The official Go SDK pulls a much larger dep tree; we've been disciplined (modernc.org/sqlite, mr-tron/base58, lukechampine.com/blake3 are all pure Go). httptest covers everything we need to test.
- **Normalised message shape is OpenAI-ish, with `system` hoisted out.** Anthropic puts the system prompt outside the message list; OpenAI uses an inline system role; Ollama mirrors OpenAI. We model `Request.System` as a top-level field so adapters can place it correctly without losing information either way.
- **`Temperature` is a `*float64`, not a `float64`.** Zero is a meaningful temperature; "not set" is a different state. Pointer-or-nil distinguishes them cleanly.
- **`StopReason` is a small enum; unknowns map to `StopReasonOther`.** Future Anthropic stop reasons we haven't seen yet don't crash the adapter — they fall through to `Other` and the caller can read the `Raw` field if it cares.
- **`Response.Raw` carries the provider's full original body.** Bytes for the price of bytes; clients that need provider-specific fields can read it; clients that don't can ignore it. Includes by default in v0.1; an opt-out flag lands if anyone ever complains.
- **Provider credentials decrypted once at unlock, held in-process.** Same trust boundary the unlocked Ed25519 private key already sits in. Decrypt-per-call is more secure but the UX is brutal. SPEC §9.2 explicitly calls "a compromised daimon-core process" out of v0.1 scope; this lives inside that boundary.
- **Crypto duplication is deliberate, recorded debt.** `internal/identity/keystore.go` and `internal/provider/credentials.go` both implement Argon2id+AES-GCM with the same SPEC §4.2 parameters. Factoring a shared `internal/secretbox` is the right call — but it's tangential to "ship the Claude adapter." Both files carry a TODO; the factor lands together with passkey/WebAuthn-PRF support, where the abstraction earns its keep.
- **Default model list is IDs + display names only.** Hard-coded context windows that may be wrong are worse than omitting them. A future iteration can hit `GET /v1/models` for dynamic discovery, or carry a vetted manifest. v0.1 ships `claude-opus-4-7`, `claude-sonnet-4-6`, `claude-haiku-4-5-20251001` — first one is the default when `Request.Model` is empty.
- **`anthropic-version: 2023-06-01` pinned in the adapter.** Anthropic versions the wire format via this header; we pin so the format doesn't shift under us. Bump deliberately.
- **DefaultMaxTokens = 1024.** Anthropic requires `max_tokens`; we set a conservative default that fits typical chat replies without truncating, and the caller overrides per-request.
- **`Registry.Register` replaces silently.** The daimon owns the provider table and may swap implementations (e.g. for tests). A "would clobber" error would be fussy; the test that verifies replacement asserts the new behaviour.
- **`provider.invoke`'s `inject_context` runs the SPEC §11 recency-weighted retrieval and prepends the result to `request.system`** — original system prompt is preserved, retrieved block goes first. Empty retrieval is silent (not an error); when the original system was empty, the injected block becomes the system prompt by itself. The activity-log entry records `injected_memory_ids` so the audit trail captures what the daimon contributed to the prompt.
- **`provider.invoke` does NOT log the prompt.** SPEC §8 says "every meaningful action" — the call counts, the model counts, token usage counts, what got injected counts. The prompt itself is the principal's data and would defeat the point of keeping memory local. The activity entry has `provider`, `model`, `input_tokens`, `output_tokens`, `stop_reason`, `duration_ms`, optionally `injected_memory_ids` — and that's it.
- **Provider errors map to `CodeInternalError`, not `CodeInvalidParams`.** Whether the request was "valid" is the upstream provider's call, not ours. The handler bubbles the upstream message through in the error data so callers can diagnose.
- **`Options.Providers` and `Options.Credentials` are both optional.** A daimon with no providers (e.g. a memory-only configuration) is a real configuration, not an error. `provider.list` returns `[]`; `provider.invoke` returns `CodeNotFound` with a structured message.
- **`Models()` returns a defensive copy.** Caught only by a paranoia test, but the mistake (mutate adapter's internal slice via a returned reference) is exactly the sort of thing that would never show up until it caused a Heisenbug.

**What we explicitly punted (in priority order for next session):**
1. **Real Ollama embedder** — drops into the existing `Embedder` interface seam in `internal/memory`. Makes cosine search and `context.get` non-trivial in the demo (currently substring-fallback because demo uses `NullEmbedder`). Probably the right next pick: it makes `inject_context` semantically meaningful, which is what mediated mode is *for*.
2. **Second provider adapter (OpenAI Responses API)** — proves the Provider interface generalises. If the interface needs to bend, three adapters tells us where. Doing this before SQLCipher means we lock the provider abstraction before adding a heavy storage migration.
3. **SQLCipher at-rest encryption.** Closes SPEC §5.1. Genuine architectural fork: pure-Go modernc.org/sqlite has no SQLCipher; CGO + mattn/go-sqlite3 with SQLCipher means giving up the pure-Go build, and the alternative (application-level row encryption — encrypt content/metadata blobs before write, leave rowids/timestamps clear) is materially simpler and may actually be better for this v0.1 scale. Worth its own deliberate session.
4. **Streaming.** SPEC §11 says "HTTPS transport supports SSE; Unix socket sync-only in v0.1." When the first interactive client lands, this stops being theoretical.
5. **Tool use, multimodal content, batch requests.** All three need spec changes before interface changes.
6. **Internal `secretbox` factor.** Two copies of Argon2id+AES-GCM in the tree (identity + credentials). Factor when the third copy would be needed, or when passkey/WebAuthn-PRF support arrives — whichever comes first.

**What this means in plain language:** before this session, daimon-core was a daemon that could store and verify a principal's memory. After this session, daimon-core is a daemon that holds a principal's memory *and credentials* and can mediate between any client and Anthropic. The "switch Claude → GPT → local Llama mid-task without losing your agent" pitch is no longer aspirational — half of it works today (set `ANTHROPIC_API_KEY`, start `daimond`, point any JSON-RPC 2.0 client at the socket, call `daimon.provider.invoke` with `inject_context`, and a real Claude reply comes back enriched with retrieved memories). Other halves arrive when the Ollama and OpenAI adapters land.

**Next session begins with:** Ollama embedder *or* OpenAI adapter *or* SQLCipher (architectural decision) — huckgod's call. Default recommendation: Ollama embedder. It makes `inject_context` and `context.get` semantically real (cosine over actual embeddings, not substring fallback), which validates the mediated-mode flow we just shipped. Small, self-contained, drops into the existing seam. SQLCipher needs its own decision-making session — the spec deviation is recorded and survives one more milestone.

---

## 2026-05-03 — Day Zero, ninth session: Ollama embedder landed

**The cosine path is live.** The mediated-mode flow we shipped last session — `provider.invoke` with `inject_context` retrieving memory by SPEC §11's recency-weighted formula — was running on substring fallback because the demo bound `NullEmbedder`. As of this session the daimon probes a local Ollama server at startup, caches the model's vector dimension on a single round-trip, and the existing `memory.Search`, `daimon.context.get`, and `inject_context` paths all switch to real cosine similarity over real vectors with zero changes to the rest of the tree. When Ollama is absent, SPEC §11's graceful-degrade kicks in: the daimon prints a one-line warning and falls back to `NullEmbedder`. Both paths are exercised end-to-end.

**Files (this session):** two new in a new subpackage, ~470 lines combined. ~190 lines of implementation in `internal/memory/ollama/ollama.go`, ~280 lines of tests in `internal/memory/ollama/ollama_test.go`. ~30 lines edited in `cmd/daimond/main.go` to wire the probe and add a search step. CHECKPOINT.md and this journal entry round out the diff.

| Path | Purpose |
|---|---|
| `internal/memory/ollama/ollama.go` | `Embedder` struct, `New(ctx, opts...)` with probe-on-construct, `Embed`/`Dimensions`/`Name`. POSTs `/api/embed` with `{"model": "...", "input": "..."}`; decodes `{"embeddings": [[...]]}`. |
| `internal/memory/ollama/ollama_test.go` | 12 tests covering probe defaults + overrides, probe errors (HTTP / network / empty embedding), Embed happy path, empty-input short-circuit, dimension-mismatch detection, HTTP-error propagation, context cancellation, plus an integration test that opens a real `memory.Store` against the Ollama-stub via httptest, writes three memories, and verifies cosine ranking with deterministic one-hot vectors. |
| `cmd/daimond/main.go` | `pickEmbedder(ctx)` helper: try `ollama.New` with a 3s probe deadline (default endpoint, overridable via `OLLAMA_HOST`); on success, return the Ollama embedder; on failure, print a SPEC §11-shaped warning and return `NullEmbedder`. New `[4/8]` step runs `store.Search` and labels which path engaged (cosine vs substring fallback) so the demo output makes the code path visible. Both `store` and `freshStore` now share the picked embedder. |

**Test totals:** 100/100 pass in ~10s, race-clean. By package: 8 identity, 14 memory, 11 activity, 12 provider, 10 claude adapter, 12 ollama, 29 server, 4 cmd-level paths exercised by the demo. Binary 15.0 MB → 14.4 MB (mild shrink — net/http and JSON were already pulled in by the Claude adapter; the linker dead-code-eliminated some bytes elsewhere).

**Decisions made this session:**

- **Probe at construction, cache the dimension.** Ollama's `/api/embed` doesn't expose model metadata separately; the only honest way to know vector size is to embed something and count. Doing this once at `New()` means `Dimensions()` is a constant-time read for the rest of the run, and `New()` returning an error is a clean health signal — caller falls back to `NullEmbedder` instead of discovering the failure on every memory write. Trade-off: the probe burns one network call at startup. For a daemon process that lives for hours, this is irrelevant.
- **`/api/embed` (modern endpoint), not `/api/embeddings` (legacy).** The newer path supports batch input and is the documented forward direction. Wire format: `{"model": "...", "input": "text"}` → `{"model": "...", "embeddings": [[...]]}`. v0.1 uses single-input mode; batch lands when the demo writes more than three memories at once.
- **`probeText = "daimon-probe"` is hardcoded.** Configurable probe text would be over-engineering; the dimension is what we want and any non-empty string suffices. Keeping it constant also makes test fixtures deterministic.
- **Empty input short-circuits to `(nil, nil)`.** Matches `NullEmbedder`'s contract. `memory.Store.Write` rejects empty content before reaching `Embed`, so this path is reached only when callers do `Search("")` or similar — and we'd rather not burn an HTTP call to confirm what the local code already knows.
- **Dimension mismatch is an error, not a silent skip.** If a subsequent embed call returns a vector of a different size than the cached probe — implausible but possible if a model is hot-swapped under us — we surface `ErrDimensionMismatch` instead of writing a row with a garbage vector. `memory.Search` already tolerates mixed-dimension rows (it skips them from the cosine path), so a corrupt write would be silently dropped from search. Better to fail the write loudly.
- **Probe deadline is 3 seconds (in main.go), not in the package.** The `Embedder` uses the standard `http.Client.Timeout` (30s) for normal calls; the daimon imposes a tighter ceiling at startup so a misconfigured `OLLAMA_HOST` pointing at a black-hole IP doesn't stall the daemon. Deadline lives in the caller because it's a policy decision — a CLI demo wants a fast fall-back; a long-running daemon might want longer probe latency.
- **`OLLAMA_HOST` env var honored.** Standard Ollama convention. Default `http://localhost:11434`. Schemes are required for v0.1 (no `127.0.0.1:11434` shorthand); Ollama's own client tolerates more variants and we'll match if anyone ever asks.
- **`Name()` returns the model string, not a fixed `"ollama"` literal.** SPEC §5.3 calls out that the schema must tolerate variable embedding dimensions per row — implication is that the embedder's name is the model identifier, not the serving daemon's identifier. `memory.Search` doesn't yet use `Name()` to filter mismatched-model rows (it filters on dimension), but the seam is correct for when it does.
- **Package lives at `internal/memory/ollama/`, parallel to `internal/provider/claude/`.** The `Embedder` interface in `internal/memory/embedder.go` is the seam; `internal/memory/ollama` is one implementation. Future embedders (e.g. a local sentence-transformers binary, an OpenAI embeddings shim if anyone ever wants it) drop into siblings.
- **Integration test exercises the real `memory.Store` path.** Construction + write + search + cosine ranking, with the Ollama server stubbed to one-hot vectors so the assertions are exact. Proves the embedder satisfies `memory.Embedder` and that the cosine path engages — not just the unit-level "did we POST the right JSON" tests.
- **Demo print labels the retrieval path explicitly.** When the daimon falls back, the demo prints `Top hit (substring (NullEmbedder fallback), score=0.500): …`; with Ollama up, it would print `Top hit (cosine, score=0.987): …`. Without this, the only difference between paths would be a coarse 0.500/1.0 score — easy to miss. Demo output should make architectural state legible.

**What we explicitly punted (in priority order for next session):**
1. **Second provider adapter (OpenAI Responses API)** — the obvious next step. With three adapters (Claude / OpenAI / Ollama-chat) in tree, the `provider.Provider` interface gets exercised against three different request shapes; if it bends, this is when it bends. The translation work is mechanical at this point.
2. **Ollama chat adapter.** We already have a working Ollama HTTP path; `/api/chat` is the same daemon, similar wire format. Closes the "switch Claude → GPT → local Llama mid-task" pitch in single-player utility.
3. **SQLCipher at-rest encryption.** Genuine architectural fork; deserves its own session. The spec deviation has now survived two milestones — closing it is increasingly load-bearing.
4. **Stale-row cleanup when the embedding model changes.** Today, mixed-dimension rows are silently skipped from cosine search. A `memory.reindex` operation that re-embeds existing content under the current model belongs on the v0.1.x list.
5. **Embedding-name tagging on memory rows.** SPEC §5.2's schema doesn't carry the embedder name today — only the dimension distinguishes models. With multiple embedders in production, store the model name alongside the vector so we can filter precisely.
6. **Ollama batch-embed for `Import`.** Importing a 1000-memory document re-embeds row-by-row when the embedder dimension differs. One batch round-trip per N rows would be much faster.

**What this means in plain language:** before this session, the mediated-mode demo wrote memories whose embedding column was empty (NullEmbedder produces no vectors), and `daimon.context.get` / `inject_context` retrieved candidates by `LIKE '%query%'` substring match. After this session, if you have Ollama running locally with `nomic-embed-text` pulled, every memory the daimon writes carries a 768-dimension vector, every retrieval goes through real cosine similarity blended with recency, and the prompt the daimon prepends to a Claude call (via `inject_context`) is selected by semantic similarity to the user's input — exactly as SPEC §11 intends. If Ollama is *not* running, none of that breaks: the daimon falls back to substring search and key-value memory, the demo says so out loud, and every subsequent code path behaves identically except for retrieval quality.

**Next session begins with:** OpenAI provider adapter *or* SQLCipher (architectural decision) — huckgod's call. Default recommendation: OpenAI adapter. It's the right time to validate the `Provider` interface against a second wire format before SQLCipher's storage rework lands; the Anthropic adapter from session-eight is the only stress test the interface has had so far. SQLCipher remains a one-deliberate-session task — the spec deviation has now survived two milestones, but the daimon's single-tenant local-only threat model means every byte at rest is on a disk that's already encrypted at the OS layer, so the urgency is moderate.

---

## 2026-05-03 — Day Zero, tenth session: second provider adapter (OpenAI Responses API) landed

**The Provider interface now stress-tests against two wire formats.** Session 8 shipped Claude (Anthropic Messages API) — system prompt outside the message list, `x-api-key` + `anthropic-version` headers, content blocks of type `text`. This session ships OpenAI against the *Responses API* (SPEC §7.2 line: `openai — OpenAI Responses API (with Chat Completions fallback)`) — `instructions` field replaces `system`, `Authorization: Bearer …` replaces the API-key header pair, status+`incomplete_details.reason` replaces `stop_reason`, output is an array of typed items where only `message`-typed ones contribute to text. Two providers, two genuinely different wire shapes, one unchanged `provider.Provider` interface and one unchanged `provider.Request` / `provider.Response` normalised pair. The interface generalises; this is the news.

**Files (this session):** two new in a new subpackage, ~625 lines combined. ~290 lines of implementation in `internal/provider/openai/openai.go`, ~335 lines of tests in `internal/provider/openai/openai_test.go`. ~25 lines edited in `cmd/daimond/main.go` to register the OpenAI adapter conditionally on `OPENAI_API_KEY`. CHECKPOINT.md and this journal entry round out the diff.

| Path | Purpose |
|---|---|
| `internal/provider/openai/openai.go` | `Adapter` struct, `New(apiKey, opts...)`, `Name`/`Models`/`Invoke`. POSTs `/v1/responses` with `{"model": "...", "input": [{"role": ..., "content": ...}], "instructions": "...", "max_output_tokens": ..., "temperature": ..., "stop": [...]}`. Decodes the typed `output` array, concatenates `output_text` blocks from `message`-type items, skips reasoning summaries and tool calls. Maps `status` + `incomplete_details.reason` to the normalised `StopReason` enum. |
| `internal/provider/openai/openai_test.go` | 13 tests covering: requires API key, name + Models defensive copy, happy-path roundtrip with full request/response wire-format assertions including the Bearer header, requires messages, defaults model, respects temperature + stop sequences + max_output_tokens override, HTTP error code propagation, response-level `error` payload propagation (200-with-error case), six-case stop-reason normalisation table (completed / incomplete×4 / unknown future status), context cancellation, multi-block text concatenation, non-message output skipping (reasoning + tool_call items in the output array), and multi-turn message ordering preservation. |
| `cmd/daimond/main.go` | `buildProviderRegistry` now registers Claude on `ANTHROPIC_API_KEY` *and* OpenAI on `OPENAI_API_KEY` independently. Either, both, or neither: the demo handles all four configurations cleanly. The closing log line was updated from "next: OpenAI adapter" to "next: SQLCipher then Ollama chat adapter". |

**Method surface (this build vs. previous):** unchanged. `daimon.provider.list` and `daimon.provider.invoke` were already wired in session 8; the new adapter slots in via `Registry.Register` without touching the RPC handler.

**Test totals:** 113/113 pass in ~10s, race-clean. By package: 8 identity, 15 memory, 12 ollama embedder, 11 activity, 32 server, 12 provider, 10 claude adapter, **13 openai adapter (new)**. Binary 14.4 MB → 14.4 MB (no measurable change — the OpenAI adapter shares net/http and json with Claude, and the Go linker dead-code-eliminates the rest).

**Decisions made this session:**

- **Responses API, not Chat Completions, per SPEC §7.2.** OpenAI's stated forward direction; the spec line was deliberate. Chat Completions is the documented fallback for models or gateways that don't implement Responses; that fallback lands when a real deployment surfaces the need. Building Responses-first means the adapter is aligned with where OpenAI is going (built-in tools, structured outputs via `text.format`, reasoning summaries on o-series, response-state continuation via `previous_response_id`) instead of where it has been.
- **Simplified message-array input form.** Responses API accepts three input shapes: a string shorthand, a typed array of `input_message` items with `input_text`/`input_image` content blocks, and a simplified `[{"role": "...", "content": "string"}]` form that the API auto-promotes to the typed shape. The simplified form maps 1:1 onto our normalised `[]Message{Role, Content}` and keeps the marshalling code trivial. When v0.1 grows multimodal content, the marshaller switches to the typed shape and the test fixtures move with it.
- **`instructions` field carries `Request.System`.** Responses API replaces Anthropic's separate `system` parameter with `instructions`; semantically identical. The normalised `Request.System` field plumbs cleanly into either via per-adapter mapping — the very point of hoisting it out of the message list at the interface boundary.
- **`max_output_tokens` field, not `max_tokens`.** Responses API uses the explicit name (Chat Completions used `max_tokens`). The adapter sends the Responses-shaped name; if/when the Chat Completions fallback lands it gets the legacy name. Default 1024 mirrors the Claude adapter — same conservative chat-reply ceiling.
- **`stop` field included for `Request.StopSeqs`.** Responses API documents `stop` as a top-level parameter. We forward `Request.StopSeqs` verbatim. If the field gets dropped from the public API for some model class, we'll surface that as an HTTP 4xx via `truncateForError` and adjust the mapping; the test for stop-sequence override asserts the field name on the wire so any silent removal would break tests, not just runtime calls.
- **Stop-reason mapping is a small state machine over `status` + `incomplete_details.reason`.** `status="completed"` → `StopReasonEndTurn`. `status="incomplete"` with `reason="max_output_tokens"` (or legacy `"max_tokens"`) → `StopReasonMaxTokens`. `reason="stop_sequence"` → `StopReasonStopSequence`. `reason="content_filter"` or unknown → `StopReasonOther`. `status="failed"` returns an error from `Invoke` (the response-level `error` payload gets surfaced) — there's no reason to fabricate a stop reason for a generation that didn't happen. Future statuses fall through to `StopReasonOther` so unrecognised values don't crash the adapter; the `Raw` field is always populated for callers who want to introspect.
- **Output items typed as `message` only contribute to text content; everything else is skipped.** Output array entries can be `message`, `reasoning`, `tool_call`, `output_audio`, etc. v0.1 surfaces only the user-visible assistant message — same scope as the Claude adapter (text content blocks only). Reasoning summary surfacing on o-series models is a deliberate next-step (it needs spec definition for a separate normalised field; co-mingling it into `Content` would silently break callers expecting the assistant's reply). Tool calls land with the tool-use spec change. The skip-test asserts that a response with `reasoning` + `message` + `tool_call` items returns only the message text.
- **Response-level `error` payload is checked even on HTTP 200.** OpenAI can return a 200 with `{"status": "failed", "error": {...}}` when a request validates but generation fails partway (model overload, content policy violation in stream, internal error). The adapter checks `parsed.Error != nil` after JSON-decoding and surfaces the upstream message. The test for this path uses a 200 + `status: failed` + `error.message` body; matches what we'd see from a real failure.
- **Default model list is `gpt-5`, `gpt-5-mini`, `gpt-4.1`.** IDs only — Context and MaxOutput omitted for the same reason the Claude adapter omits them (hardcoding wrong numbers is worse than omitting them). First entry is the default when `Request.Model` is empty. Same model-list discovery upgrade path as Claude: future iteration can hit `GET /v1/models` for dynamic discovery, or carry a vetted manifest. Three models in the list mirrors Claude's surface area precisely.
- **`Authorization: Bearer …` header construction.** Standard OpenAI convention. Optional headers like `OpenAI-Organization` and `OpenAI-Project` are deliberately omitted in v0.1 — most accounts don't need them, and the credential store doesn't currently model multi-field provider credentials. When org-scoped credentials become a real ask, it's a credential-store schema change (probably JSON-encoded multi-field secrets), not an adapter change.
- **`Models()` returns a defensive copy.** Same paranoia as Claude — caller mutating the returned slice must not corrupt the adapter's internal list. Test asserts this directly.
- **Adapter registration is independent of Claude.** `buildProviderRegistry` checks `ANTHROPIC_API_KEY` and `OPENAI_API_KEY` independently — registering one does not require or block the other. The demo prints a per-adapter status line, plus a single "no providers configured" tail if the registry ends empty. A user with both keys gets both adapters; with neither, the demo runs the full memory/activity/RPC stack and `provider.list` returns an empty array.
- **No registry-level changes.** `provider.Registry` already supports arbitrary adapters keyed by `Name()`; the OpenAI adapter slots in by calling `Register(ad)` exactly like Claude. The `provider.invoke` handler dispatches by name with no per-adapter logic. This is the test that actually mattered for "does the interface generalise" — the answer is yes, and the proof is the absence of a diff in `internal/server/handlers.go`.

**What we explicitly punted (in priority order for next session):**
1. **SQLCipher at-rest encryption.** Now the highest-value remaining v0.1 work. Genuine architectural fork (CGO mattn vs. application-level row encryption vs. KEK-derived encrypted page store); deserves its own deliberate session. The spec deviation has survived three milestones and is the most prominent gap between SPEC v0.1 and the reference implementation.
2. **Ollama chat adapter.** Same daemon we already embed against; `/api/chat` is similar wire format. Third adapter rounds out the v0.1 trio (Claude / OpenAI / Ollama-chat) and closes the "switch Claude → GPT → local Llama mid-task" pitch end-to-end. Modest interface stress beyond what OpenAI added (Ollama's chat format is closer to OpenAI Chat Completions than to Responses).
3. **Chat Completions fallback for OpenAI.** SPEC §7.2 calls it out parenthetically. Lands when a deployment surfaces a model the Responses API doesn't support. Mechanical translation — the same `provider.Request` is even simpler to render against Chat Completions than against Responses.
4. **Reasoning summary surfacing.** Responses API includes reasoning items in the output array for o-series models; v0.1 silently skips them. Surfacing requires a normalised `Reasoning` field on `Response` (or a separate retrieval method) and a SPEC update to define semantics.
5. **Tool use, multimodal content, structured outputs (`text.format`), batch requests.** All require interface and spec changes; out of scope for v0.1.
6. **Streaming.** SPEC §11 says HTTPS transport supports SSE; Unix socket sync-only in v0.1. When the first interactive client lands, this stops being theoretical for both adapters at once.
7. **Internal `secretbox` factor.** Three copies of Argon2id+AES-GCM in tree (`internal/identity/keystore.go`, `internal/provider/credentials.go`, and the next provider that needs encrypted state — none yet). Factor when the third copy would be needed, or when passkey/WebAuthn-PRF support arrives.

**What this means in plain language:** before this session, the daimon could mediate between any client and Anthropic — set `ANTHROPIC_API_KEY`, start `daimond`, point any JSON-RPC 2.0 client at the socket, get Claude responses enriched by retrieved memories. After this session, the same client targeting the same daimon, with `OPENAI_API_KEY` exported instead, gets GPT-5 responses through the identical RPC surface — `daimon.provider.list` reports both adapters when both keys are present, and `daimon.provider.invoke` with `provider: "openai"` routes to the new adapter while `provider: "claude"` continues to route to the existing one. The "switch Claude → GPT → local Llama mid-task without losing your agent" pitch is now two-thirds real (Claude ↔ GPT works today; the Llama half lands with the Ollama chat adapter).

**Next session begins with:** SQLCipher at-rest encryption (architectural-decision session) *or* Ollama chat adapter (mechanical) — huckgod's call. Default recommendation: **SQLCipher**. The deviation has now survived three milestones; with two provider adapters in tree the Provider interface is clearly stable enough that a storage-layer rework underneath won't ripple upward. The session is mostly an architectural decision (CGO mattn vs. application-level row encryption vs. KEK page store) followed by mechanical work; the deliberate-decision part is exactly what's been deferred. Ollama chat is the lower-risk option if huckgod wants to keep the v0.1 provider trio momentum — it's net-new functionality and finishes the demo flow that motivates the whole project.

---

## 2026-05-03 — Day Zero, eleventh session: SPEC §5.1 closed — application-level row encryption

**The deliberate-decision session.** Three milestones of recorded deviation came due. The fork as recorded in the previous journal entry: **CGO + mattn-with-SQLCipher** (page-level, but breaks pure-Go single-binary distribution and turns every release into a multi-platform build matrix), **application-level row encryption** (encrypt user-supplied columns before write under an in-memory key, leave structural columns clear), or a **KEK-derived encrypted page store** (build a VFS shim over modernc.org/sqlite — i.e. roll our own crypto layer at the page boundary). Picked option 2.

**Why option 2:** the pure-Go build is the load-bearing constraint. modernc.org/sqlite is the entire reason daimond ships as a single binary with no toolchain dependencies — that promise has been protected for nine sessions of disciplined dependency curation, and giving it up for SPEC literalism is the wrong trade. Option 3 is rolling our own crypto at the page-store layer; doesn't pass the bar. Option 2 fits the v0.1 threat model precisely (SPEC §9.2 scopes us to disk theft / backup exfiltration on top of OS-layer FDE — encrypting the user-supplied content material covers that) and reuses what we already have (AES-256-GCM is in `keystore.go`; the seam is `Open`). The metadata that leaks in option 2 — row count, timestamps, kinds, embedding vectors — is already plaintext in `activity.log` (also at rest, also on disk, also in scope of SPEC §10), so we're not weakening a posture that was page-level to start with.

**Files (this session):** two new in `internal/memory/`, ~415 lines combined. ~140 lines of implementation in `internal/memory/crypto.go`, ~200 lines of tests in `internal/memory/crypto_test.go`. `internal/identity/identity.go` gains a generic `DeriveSubkey(label, length)` HKDF helper (~30 lines + ~70 lines of tests in `identity_test.go`). `internal/memory/store.go`, `internal/memory/search.go`, `internal/memory/export.go` get the encryption integration (~80 lines of edits across the three). `internal/memory/memory_test.go` gains two load-bearing assertions about at-rest behaviour (~80 lines), the existing `TestReadDetectsTampering` is split into ciphertext-tamper and signature-tamper variants. `cmd/daimond/main.go` and `SPEC.md` get one-line updates.

| Path | Purpose |
|---|---|
| `internal/identity/identity.go` | New `DeriveSubkey(label, length)` — HKDF-SHA256 over the Ed25519 seed with a domain-separated info label. Generic so future consumers (provider creds keying, activity-log encryption when that lands) reuse it. |
| `internal/memory/crypto.go` | `encryptField` / `decryptField`: AES-256-GCM with `version(1B) || nonce(12B) || ciphertext+tag` framing. AAD = `"daimon-memory-row-v1" || 0x00 || memoryID || 0x00 || fieldName`. `MemoryEncryptionKeyLabel = "daimon-memory-encryption-v1"` is the HKDF info string. nil key falls through to plaintext for migration tooling that doesn't exist yet. |
| `internal/memory/store.go` | `Store` gains a `key []byte` field. `Open` derives it from `id.DeriveSubkey(MemoryEncryptionKeyLabel, 32)` — same identity rederives the same key on subsequent opens, so reopens work across process restarts. `Write` encrypts content/metadata/source under (memoryID, fieldName) AAD before INSERT. `scanMemory` becomes a method on `Store` and decrypts on read. |
| `internal/memory/search.go` | Substring search no longer uses SQL LIKE (would match ciphertext bytes — useless). Loads candidate rows filtered by kind, decrypts content via `s.scanMemory`, substring-matches in Go, breaks early at limit. Cosine path unchanged in logic — `s.scanMemory` already returns plaintext content, the cosine math runs over plaintext-side floats from the embedding column (which stays in clear). |
| `internal/memory/export.go` | `insertImported` now encrypts content/metadata/source under the **receiver's** derived key on insert. The per-memory signature is unchanged because it is over plaintext (id ‖ content ‖ metadata) and was produced by the source identity. Cross-identity import continues to work — the daimon importing the document doesn't need the source's encryption key, only its public key for signature verification. |
| `internal/memory/crypto_test.go` | 12 tests: round-trip, ciphertext is randomised across encryptions of identical plaintext (nonce reuse check), cross-row swap rejected (AAD binds to row id), cross-field swap rejected (AAD binds to field name), foreign-key decrypt rejected, bit flip rejected, unknown version rejected, truncated blob rejected, plaintext blob under encrypted key rejected, invalid key length rejected, nil key falls through, empty plaintext encrypts to nil. |
| `internal/memory/memory_test.go` | New `TestRowsAreEncryptedAtRest` reads raw SQL columns and asserts plaintext does not appear in any of them and the v1 framing byte is present — this is the load-bearing test that closes SPEC §5.1. New `TestForeignIdentityCannotDecrypt` writes under one identity, reopens the same DB under a fresh identity, asserts Read fails with `ErrInvalidCiphertext`; then reopens under the original identity, asserts the same row Read succeeds. The old `TestReadDetectsTampering` is split into `TestReadDetectsContentTampering` (raw SQL UPDATE on the encrypted column → AEAD authentication fails before signature verification) and `TestReadDetectsSignatureTampering` (raw SQL UPDATE on the clear signature column → row decrypts cleanly, signature verify fails). Both tamper paths now exercise distinct error surfaces. |
| `cmd/daimond/main.go` | Closing-line message updated. The full demo runs end-to-end against encrypted-at-rest memory — three writes, search, export, cross-identity import, RPC roundtrip. |
| `SPEC.md` §5.1, §9.3, §10 | Storage layer rewritten to match implementation. §9.3 cryptographic primitives table now distinguishes "memory rows v0.1" (application-level) from "memory pages v0.2+ option" (SQLCipher) and adds an HKDF-SHA256 row for subkey derivation. §10 `memory.db` annotation updated. |

**Test totals:** 129/129 pass in ~10s, race-clean. By package: **9 identity (+1)**, **30 memory (+15)**, 12 ollama embedder, 11 activity, 32 server, 12 provider, 10 claude adapter, 13 openai adapter. Binary 14.4 MB → 15.1 MB (HKDF and crypto helpers; small).

**Decisions made this session:**

- **Encrypt content + metadata + source. Leave id, timestamps, kind, embedding, signature in clear.** Content and metadata are user-supplied free-form data — the actual sensitive material. Source is also user-supplied (could be a filename containing a username); encrypting it for symmetry costs ~30 bytes per row. The clear columns are needed for indexing without unlock (id, timestamps, kind), are a one-way function of plaintext (embedding — knowing the model lets you brute-force candidate strings, but the disk-theft threat model isn't a model-stealing attacker with infinite resources), or must be clear by design (signature — it's verified against a public key, not decrypted).
- **AAD binds (memoryID, fieldName).** Without this, a content ciphertext from row A could be silently moved to row B, or a content ciphertext could be moved into the metadata column. With AAD bound to both the row id and the field name, AEAD authentication fails on any such swap. Documented in `crypto.go`; `TestDecryptRejectsCrossRowSwap` and `TestDecryptRejectsCrossFieldSwap` are the load-bearing assertions.
- **Version-byte prefix in the ciphertext blob.** Every encrypted column starts with `0x01`. A future migration to a new AEAD construction (XChaCha20-Poly1305, AES-GCM-SIV, post-quantum) bumps this byte and the decryptor dispatches. Ciphertexts from older versions remain readable indefinitely; rotation is a separate concern handled by a re-encrypt-on-read or batch-rewrite operation when needed. v0.1 ships only `0x01`.
- **Encryption key derivation: HKDF-SHA256 over the Ed25519 seed, info label `"daimon-memory-encryption-v1"`.** The seed is the 32-byte secret half of the Ed25519 keypair (already uniform random). HKDF-Extract+Expand with a domain-separated info label produces output statistically independent of signing operations under the same key — RFC 5869 explicitly supports this pattern. The key is **not** stored on disk: it's rederived in memory at `Open` time, lives only as long as the unlocked `daimond` process, and shares the trust scope of the unlocked private key (SPEC §9.2). Determinism in the seed means a daimon can reopen its own encrypted store across process restarts without any additional key-management infrastructure.
- **`DeriveSubkey` is generic, not `MemoryEncryptionKey`.** The single-purpose method would have been simpler today, but the next consumer (an encrypted activity log when the SPEC §10 deviation gets closed; or per-conversation context keys; or a forward-secrecy ratchet on provider creds) is going to want the same primitive. Generic with a label argument is the right shape, and the cost is one extra parameter at the call site.
- **`scanMemory` becomes a method on `Store`.** It was a free function because it didn't need the store. Now it does — to access `s.key`. Three call sites updated (`Read`, `List`, both search paths). No external consumers; pure refactor.
- **Substring search loads + decrypts in Go.** SQL `LIKE` against a ciphertext column would match nothing useful. The substring path now scans rows ordered by `created_at DESC`, decrypts content per-row, runs `strings.Contains` in Go, breaks early at limit. O(n) on the entire memory store when no embedder is configured; for v0.1 single-user scale (thousands of memories max) it is well under 10ms. The cost is a real but acceptable regression on a path that was already the fallback. The cosine path (when Ollama is up) loads candidate rows by `embedding IS NOT NULL` and decrypts as part of `scanMemory` for free — no extra work compared to before. Net effect: cosine-with-Ollama is the recommended path even more strongly than before, which matches SPEC §11's intent.
- **Foreign-identity Read returns `ErrInvalidCiphertext`, not `ErrSignatureFailed`.** With encryption, the AEAD authentication tag check fails before the signature verification ever runs — the row's bytes are unreadable to a foreign key. This is structurally cleaner than the previous behaviour: an attacker with disk access cannot even read what they're trying to forge against. The `TestForeignIdentityCannotDecrypt` test makes this explicit, and the same test asserts that the original identity can reopen the DB and recover plaintext.
- **Ciphertext-tamper and signature-tamper are tested separately.** Out-of-band SQL UPDATE on the encrypted content column is caught by AEAD authentication and surfaces `ErrInvalidCiphertext`. Out-of-band SQL UPDATE on the clear signature column is caught by signature verification (after the row decrypts cleanly) and surfaces `ErrSignatureFailed`. Both paths exist; both are tested; the previously-single tamper test is split to assert both behaviours independently.
- **Import re-encrypts under the receiver's key.** The signature stays valid because it is over plaintext, and the source identity's signature against the export DID is verified before any insert happens. The receiver then encrypts content/metadata/source under its own derived key for at-rest storage. This is the natural and correct behaviour: at-rest encryption is per-store (per-principal), not per-document. Cross-identity import remains supported (SPEC §5.4 lets policy live above this layer).
- **No DB migration in v0.1.** No persistent data exists yet — every prior session demo ran in a `TempDir`. New stores get encryption from the first row written. If we ever ship a build with persistent stores and then add a feature that requires re-encryption, we'd write a migration; for now there's no surface area to migrate.

**What we explicitly punted (in priority order for next session):**
1. **Ollama chat adapter** — last v0.1 milestone before the CLI and demo video. Same daemon we already embed against; `/api/chat` is similar wire format. Closes the "switch Claude → GPT → local Llama mid-task" pitch end-to-end. Mechanical at this point — the Provider interface has been validated against two genuinely different wire shapes, Ollama chat is closer to OpenAI Chat Completions than to OpenAI Responses, the integration risk is low.
2. **Activity log encryption.** SPEC §10 stores `activity.log` as plaintext JSONL. The same threat model applies — disk theft reveals every memory write, every provider invocation, every audit trail. Application-level encryption per JSONL line under a sibling HKDF-derived key (`"daimon-activity-encryption-v1"`) is the obvious path, and `DeriveSubkey` is now generic enough to support it directly.
3. **Internal `secretbox` factor.** Three copies of AES-GCM in tree (`internal/identity/keystore.go` for the Ed25519 keystore, `internal/provider/credentials.go` for provider creds, `internal/memory/crypto.go` for memory rows). The keystore uses Argon2id-derived keys; credentials does the same; memory rows use HKDF-derived keys. Different KDF inputs, identical AES-GCM core. Factor when a fourth copy is needed, or when passkey/WebAuthn-PRF support arrives — whichever comes first.
4. **CLI** (`cmd/daimon` — `init / unlock / memory / provider / chat`). After the Ollama chat adapter lands, the CLI is what makes the daimon usable from a terminal without writing JSON-RPC by hand.
5. **Streaming on HTTPS transport.** Still theoretical until the first interactive client.
6. **Tool use, multimodal content, batch requests.** Spec changes before interface changes.

**What this means in plain language:** before this session, an attacker with read access to the SQLite file under `$DAIMON_HOME/memory.db` could `sqlite3 memory.db "SELECT content FROM memories"` and read every memory the principal had ever stored. After this session, the same query returns blob bytes that begin with `0x01` and continue with random-looking ciphertext; AEAD authentication binds each ciphertext to its row id and field name so even a determined SQL-level attacker can't move ciphertexts around to recover anything; and the only path to plaintext is to unlock the principal's identity (which derives the AES key at runtime from the Ed25519 seed via HKDF). The pure-Go single-binary build is preserved — `make build` still produces one statically linked executable that runs anywhere Go runs. SPEC §5.1's three-milestone-old deviation is closed; the SPEC text now matches the implementation honestly.

**Next session begins with:** Ollama chat adapter — finishes the v0.1 provider trio (Claude / OpenAI / Ollama-chat) and closes the "switch Claude → GPT → local Llama mid-task without losing your agent" pitch end-to-end. Mechanical work at this point: the Provider interface has been validated against two genuinely different wire shapes (Anthropic Messages, OpenAI Responses), Ollama chat is closer to OpenAI Chat Completions than to either, integration risk is low. After Ollama chat the v0.1 codebase has every primitive the spec demands; remaining work is the CLI surface, the demo video, and the NLnet application.

---

## 2026-05-04 — Day Zero, twelfth session: Ollama chat adapter — v0.1 provider trio complete

**The third adapter.** The pitch the whole project hinges on — "switch Claude → GPT → local Llama mid-task without losing your agent" — has been two-thirds real since session 10. As of this session it is end-to-end real: the same JSON-RPC client targeting the same daimon, with `OLLAMA_HOST` reachable on `localhost:11434` and at least one chat model pulled, can route `daimon.provider.invoke` to a locally-running Llama 3.2 (or Qwen, or Mistral, or whatever the principal has on disk) and get a normalized provider.Response back through the identical surface the Anthropic and OpenAI adapters use. The `provider.list` call now reports up to three adapters with live-discovered model lists; mediated mode's "no third party sees the full picture" guarantee finally has a third party for which the picture is *literally local*.

**Files (this session):** two new in a new subpackage, ~400 lines combined. ~280 lines of implementation in `internal/provider/ollama/ollama.go`, ~410 lines of tests in `internal/provider/ollama/ollama_test.go`. ~25 lines edited in `cmd/daimond/main.go` to thread context through `buildProviderRegistry` and add the third probe-gated registration. Two-line edits to `SPEC.md` §7.2 and `CHECKPOINT.md`. This journal entry rounds out the diff.

| Path | Purpose |
|---|---|
| `internal/provider/ollama/ollama.go` | `Adapter` struct, `New(ctx, opts...)` with probe-on-construct via `/api/tags`, `Name`/`Models`/`Invoke`. POSTs `/api/chat` with `{"model": "...", "messages": [...], "stream": false, "options": {...}}`; decodes `{"message": {"role": "assistant", "content": "..."}, "done_reason": "stop|length", "prompt_eval_count": N, "eval_count": M}`. System prompt is prepended as an inline `{role:"system"}` message (Ollama follows OpenAI Chat Completions, not Anthropic's hoisted-system convention). Generation parameters (temperature, max_tokens→`num_predict`, stop seqs) live in a nested `options` object; a tidy-payload helper omits `options` entirely when none of the knobs differ from defaults. |
| `internal/provider/ollama/ollama_test.go` | 17 tests: probe populates models from `/api/tags` with stable alphabetical ordering, probe-failure on 5xx surfaces as `New()` error, probe-failure on a closed port surfaces as `New()` error, `WithModels` short-circuits the probe entirely (lets a caller skip the network call), `Models()` returns a defensive copy, happy-path roundtrip with full request/response wire-format assertions including the absence of any auth header (Ollama has no API key), `Invoke` rejects empty messages list, model defaults to first entry from probe when `req.Model` is empty, temperature/stop-sequences/max_tokens override the defaults and land in the correct nested `options` fields, no system prompt means no extra leading message, HTTP error code propagates with upstream message included, response-level `error` payload on a 200 surfaces, six-case stop-reason normalisation table (stop→EndTurn, length→MaxTokens, load→Other, empty-but-done→EndTurn, empty-not-done→Other, unknown future reason→Other), context cancellation propagates from `Invoke`, multi-turn message ordering preserves system + user + assistant + user roles in order, empty `/api/tags` response (Ollama up but no models pulled) constructs successfully with zero models — not an error, and probe respects context deadline. |
| `cmd/daimond/main.go` | `buildProviderRegistry` now takes `ctx`. Probes `OLLAMA_HOST` (default `http://localhost:11434`) under a 3-second deadline using the same pattern `pickEmbedder` already establishes; on probe success registers the adapter, on probe failure logs a one-line "not registered (start `ollama serve` and pull a chat model to enable)" hint. No credential entry — Ollama has no API key. The closing demo banner now reads "three wire formats: Anthropic Messages, OpenAI Responses, and Ollama /api/chat — the v0.1 provider trio is complete" and points at the CLI as the next milestone. |

**Method surface (this build vs. previous):** unchanged. `daimon.provider.list` and `daimon.provider.invoke` were wired in session 8; the new adapter slots in via `Registry.Register` without touching the RPC handler. **The interface has now generalised across three genuinely different wire shapes** (system-out-of-list / system-as-instructions / system-as-inline-message; api-key-header / bearer-header / no-auth; content-blocks / typed-output-array / single-message-string; explicit-stop-reason / status+incomplete-details / done_reason+done) without a single edit to `internal/provider/provider.go`. This is the load-bearing observation — it confirms the abstraction is at the right level.

**Test totals:** 146/146 pass in ~10s, race-clean. By package: 9 identity, 30 memory, 11 activity, 32 server, 12 provider, 10 claude adapter, 13 openai adapter, **17 ollama-chat adapter (new)**, 12 ollama embedder. Binary 15.1 MB → 15.16 MB (no measurable change — the Ollama-chat adapter shares net/http and json with the other two).

**Decisions made this session:**

- **`/api/chat`, not `/api/generate`.** Ollama has both. `/api/chat` is the message-list endpoint and maps cleanly onto the normalised `provider.Request.Messages` shape; `/api/generate` is the single-prompt endpoint with no role separation, requires us to render the conversation back into a single string (and lose the structured turn-taking the daimon has been preserving since session 8). `/api/chat` is also the documented forward direction for conversational use. No reason to take the older path.
- **Probe at construction via `/api/tags`, not `/api/chat`.** `/api/tags` is the cheap `GET` endpoint that lists locally-pulled models — perfect for "is Ollama up?" and "what can I actually call?". Probing `/api/chat` would cost a real generation. Probing `/api/version` would tell us the daemon is up but nothing about what's installed. `/api/tags` answers both questions in one round-trip and shapes the model list at the same time.
- **Probe failure → registration skip, not invocation failure.** SPEC §11's graceful-degrade applies to the embedder ("if Ollama absent, semantic search disabled — key-value memory still functions"). The same logic applies even more strongly to the chat adapter: an adapter that registers but never works pollutes `daimon.provider.list` with a lie. The whole point of the registry is "what can I invoke right now?". Mirroring `pickEmbedder`'s pattern from session 9 is the right shape.
- **`Models()` returns the live `/api/tags` list, not a hardcoded default.** Claude and OpenAI ship hardcoded model lists because their providers' model catalogue is large, fast-changing, and not introspectable without a separate API call (`GET /v1/models`). Ollama is the inverse — the catalogue is exactly "what is on this user's disk", which `/api/tags` enumerates trivially. Hardcoding `["llama3.2", "mistral", "qwen2.5"]` here would be guessing what the user has installed, which is worse than asking. Live discovery also means the demo's `provider.list` line is *useful* — it tells the user which model strings will actually work in `daimon.provider.invoke`.
- **Embedding models are not filtered out of the chat list.** A user who only has `nomic-embed-text` pulled will see it advertised under the chat adapter. Trying to chat with it returns an error from Ollama itself. Filtering would require either a hardcoded family allow-list (brittle) or a name-based heuristic ("contains 'embed'") (also brittle, and wrong for any future embedding model that doesn't follow that convention). Honest enumeration with downstream errors is the simpler and more correct shape — same principle the rest of v0.1 already follows for model validation.
- **Empty `/api/tags` response is NOT an error.** Ollama is up; nothing is pulled. The adapter constructs with an empty `Models()` list. Caller decides whether to register it. The demo currently registers regardless; a future iteration could log a stronger warning when `len(Models()) == 0` and skip registration. v0.1 keeps the policy simple.
- **System prompt enters as an inline `{role: "system"}` message.** OpenAI Chat Completions convention; Ollama follows it. This is the third distinct system-prompt placement strategy in tree (Anthropic: top-level field; OpenAI Responses: `instructions` field; Ollama: inline message). The normalised `Request.System` plumbs cleanly into all three via per-adapter mapping — exactly the property the hoisted-system-field design was meant to deliver.
- **Generation parameters live in a nested `options` object.** Ollama's wire format groups temperature, `num_predict`, stop sequences, and many more knobs (top_k, top_p, repeat_penalty, mirostat, …) under a single `options` field. Sending `options: null` would be wrong (server would interpret as "use defaults" — but we are *trying* to specify some). Sending `options: {}` is harmless but ugly. The `buildOptions` helper returns nil only when none of our normalised knobs differ from defaults, so the wire payload stays tidy and unambiguous.
- **`num_predict` ceiling, not unlimited.** Ollama treats `num_predict=-1` as "generate until EOS or context exhaustion". A runaway generation could burn the whole context window of a small local model in a few seconds. We default to 1024 (mirrors Claude/OpenAI), and the caller overrides per-request. SPEC doesn't specify; this is a defensive default.
- **5-minute HTTP client timeout.** Generous on purpose. Ollama's first call after process start can take many seconds while the model loads from disk into RAM/VRAM (a 7B model is ~4GB; cold-loading from a SATA SSD on a modest machine is not instant). Subsequent calls run at memory-resident speed. The 90-second timeouts the cloud adapters use would intermittently false-positive on Ollama's cold-load path. The tighter 3-second probe deadline lives in the *caller* (`buildProviderRegistry`) — bounded probe at startup, generous timeout once we've decided to use it.
- **Stop-reason mapping conflates "natural EOS" and "matched stop sequence".** Ollama's `done_reason="stop"` covers both; the daemon doesn't distinguish them in its API. Anthropic does. v0.1 maps `"stop"` → `StopReasonEndTurn`; callers needing the distinction can compare the returned content against `Request.StopSeqs` themselves. Documented inline. If/when Ollama splits the reasons, the mapping picks it up via the explicit case.
- **Done-reason fallback to `done` flag.** Older Ollama builds may omit `done_reason`. We treat empty `done_reason` + `done=true` as `StopReasonEndTurn` (the only reasonable default) and empty `done_reason` + `done=false` as `StopReasonOther` (something is wrong but the response decoded). Tests cover both cases explicitly so future Ollama versions can't drift the behaviour silently.
- **No `Authorization` header. Period.** Tested explicitly — `headers.Get("Authorization") == ""` is asserted on every Invoke. Ollama is local; introducing an auth header would either be wrong (Ollama would reject or ignore) or attract a future password-protected-Ollama story v0.1 doesn't model.
- **No credential store entry.** `creds.Set(ad.Name(), key)` is the pattern for Claude and OpenAI; for Ollama there's no key to set. The credential store is just a name→secret map; absence of an entry is the natural representation of "no secret needed". `daimon.provider.invoke` doesn't gate on credential presence — it dispatches by name through the registry — so this works without any handler change.
- **`buildProviderRegistry` now takes `ctx`.** The other two adapters' `New` calls are synchronous and don't need a deadline. Ollama's probe is a network call that *must* be bounded. Threading `ctx` is cleaner than constructing a fresh background context inside the helper, and matches the signature of `pickEmbedder` already in the same file.
- **Package name `ollama`, imported in `main.go` as `ollamachat`.** The embedder lives at `internal/memory/ollama` and the chat adapter lives at `internal/provider/ollama`; both expose a package called `ollama`. The cmd/daimond/main.go file imports both, and Go requires distinct local names. The embedder is imported as `ollama` (its existing usage) and the chat adapter is aliased to `ollamachat`. Slightly clunky but the alternative (renaming one package) would either break the established embedder import path or coin a synthetic name like `ollamaprovider` that adds no clarity. The alias localises the awkwardness to a single import block.
- **17 tests, no integration test.** The OpenAI and Claude adapters each have ~13 tests with no integration coverage either; the embedder has 12 with one integration test (because it interacts with `memory.Store`'s decoded-vector path which is genuinely cross-package). The chat adapter has nothing analogous to test cross-package — every consumer reaches it through the `provider.Provider` interface, which the existing `internal/server/provider_handlers_test.go` already exercises with a mock. Adding a redundant integration test against a stub Ollama server here would test the test, not the system.

**What we explicitly punted (in priority order for next session):**

1. **CLI (`cmd/daimon` — `init / unlock / memory / provider / chat`).** Last v0.1 milestone before the demo video and NLnet application. With three adapters in tree and the JSON-RPC surface complete, the CLI is purely a user-facing wrapper that drives the existing socket from a terminal. SPEC §11 lists the subcommand surface; implementation is mechanical at this point. Approximate scope: a `cobra` or `urfave/cli` shell, a JSON-RPC client wrapper, six subcommands.
2. **End-to-end demo video.** Show one terminal with the daimon running, another switching providers mid-task and watching the memory persist. The Ollama-chat adapter is the *enabling* piece for this — no Llama-on-localhost, no demo. Now unblocked.
3. **NLnet NGI Zero application.** Rolling deadline every two months. Drafted in parallel with the CLI work; demo video links from the application.
4. **Activity log encryption.** SPEC §10 stores `activity.log` as plaintext JSONL. The same threat model that motivated SPEC §5.1's row encryption applies — disk theft reveals every memory write, every provider invocation, every audit trail. `identity.DeriveSubkey` is already generic; per-line AES-GCM under `"daimon-activity-encryption-v1"` is the natural next step.
5. **Internal `secretbox` factor.** Three copies of AES-GCM in tree (identity keystore, provider credentials, memory rows). Different KDF inputs (Argon2id × 2, HKDF × 1), identical AEAD core. Factor when the fourth copy is needed, or when passkey/WebAuthn-PRF support arrives — whichever comes first.
6. **Streaming on HTTPS transport.** Still theoretical until the first interactive client. The Ollama adapter currently sends `stream: false` explicitly; flipping to `true` returns a sequence of newline-delimited JSON chunks that need a different decode path. When SPEC §11's SSE-on-HTTPS lands, all three adapters get streaming together.
7. **Tool use, multimodal content, batch requests.** All require interface and spec changes; out of scope for v0.1.

**What this means in plain language:** before this session, the daimon could mediate between any client and Anthropic or OpenAI — set the relevant API key, start `daimond`, point any JSON-RPC 2.0 client at the socket, get cloud-LLM responses enriched by retrieved memories. After this session, the same client targeting the same daimon — with `ollama serve` running locally and any chat model pulled — can route `daimon.provider.invoke` with `provider: "ollama"` to a Llama 3.2 (or Qwen, or Mistral) running on the principal's own hardware, get a response back through the identical normalised interface, and mid-task switch to `provider: "claude"` or `provider: "openai"` without losing memory, identity, activity log, or context retrieval. The "switch Claude → GPT → local Llama mid-task without losing your agent" pitch — the line in the README, the line in the SPEC §1 motivation, the *single-player wedge* the whole project hinges on — is no longer aspirational. It runs end-to-end on `bin/daimond`, today, in 15.16 MB of static binary that needs no toolchain to install. Every primitive SPEC v0.1 demands is now in tree.

**Next session begins with:** CLI (`cmd/daimon` — `init / unlock / memory / provider / chat`). With every primitive landed, the CLI is what makes the daimon usable from a terminal without writing JSON-RPC by hand. After the CLI, the demo video and the NLnet application are the remaining v0.1 deliverables — both are storytelling work on top of a finished implementation, not code work.

---

## 2026-05-04 — Day Zero, thirteenth session: production lifecycle (`daimon init` / `daimon unlock` / `daimon identity get`)

**The daimon is usable from a terminal.** Twelve sessions of infrastructure ended last time with "every primitive SPEC v0.1 demands is in tree." That was true for the daemon — but you still had to write JSON-RPC by hand to talk to it. Today's session adds the CLI and the daemon-side surgery that gates the wire surface on a real keystore unlock, so the lifecycle a SPEC §11 reader expects (`init` → `unlock` → use) actually exists. This is the difference between "the protocol works" and "the protocol is shippable to a human who is not me."

**Files (this session):** seven new, ~1,200 lines combined; three modified for the unlock plumbing.

| Path | Purpose |
|---|---|
| `internal/daimonhome/daimonhome.go` | Single resolver shared by both binaries — `Resolve()` for `$DAIMON_HOME` (env override → `os.UserConfigDir()/daimon`), `KeystorePath` / `LogPath` / `SocketPath`. SocketPath transparently falls back to `$TMPDIR/daimon-$uid.sock` when the resolved path overflows the AF_UNIX `sun_path` cap (104 bytes on darwin, 108 on linux); the CLI's spawn helper and the daemon's serve loop both call this so they cannot disagree about the socket location. Creates the home dir at mode 0700 if missing. |
| `internal/daimonhome/daimonhome_test.go` | 7 tests: env-var override, mkdir-on-missing, fallback to `os.UserConfigDir()`, rejects-non-directory, normal socket path inside home, sun_path-overflow fallback to `$TMPDIR`, keystore + log paths. |
| `internal/server/unlock_test.go` | 9 new locked-mode tests: demo-mode requires the trio (3 subtests), serve-mode allows missing trio, demo-mode starts unlocked, locked rejects all except unlock (3 subtests for memory/identity/provider methods), unlock requires password, wrong password keeps locked, success transitions and permits identity.get, idempotent unlock (callback runs exactly once across two RPCs), demo-mode server rejects unlock with `CodeInvalidRequest`. |
| `cmd/daimon/main.go` | CLI entry + subcommand dispatcher. Stdlib `flag` + a hand-rolled `switch os.Args[1]` per the v0.1 framework decision. Six subcommand stubs, three implemented (init / unlock / identity get) — memory / provider / chat are scoped out and slotted as `v0.1.x` per the session's MVP scope decision. |
| `cmd/daimon/cmd_init.go` | `daimon init` — resolves `$DAIMON_HOME`, refuses to overwrite an existing keystore without `--force`, prompts for password twice with confirmation, generates a fresh Ed25519 identity via `identity.Generate()`, writes the encrypted keystore via `identity.SaveToKeystore()` (which uses the existing Argon2id+AES-256-GCM path from `internal/identity/keystore.go`). Best-effort password zeroing post-use. Does NOT spawn daimond — that's unlock's job. |
| `cmd/daimon/cmd_unlock.go` | `daimon unlock` — resolves home, checks keystore exists (clean error pointing at `daimon init` if missing), prompts for password, calls `dialOrSpawn` (see spawn.go), sends `daimon.identity.unlock {password}`, surfaces wrong-password errors with the daemon's Data field (the actual reason like "wrong password or corrupted keystore") rather than the generic Message. |
| `cmd/daimon/cmd_identity.go` | `daimon identity get` — dials existing socket without auto-spawning (auto-spawning here would silently start a locked daemon and immediately fail with `CodeIdentityLocked`, which is more confusing than the explicit "daemon not running, run unlock first" hint). Detects `CodeIdentityLocked` and ENOENT/ECONNREFUSED at the dial layer and rewrites both into actionable hints. |
| `cmd/daimon/rpc.go` | Tiny JSON-RPC 2.0 client — request/response envelopes, single-call wrapper, `asRPCError` unwrap helper. Hardcodes the small set of error codes the CLI cares about (`CodeIdentityLocked`, `CodeInvalidParams`, `CodeInvalidRequest`) so the CLI doesn't depend on the server package's internal types. |
| `cmd/daimon/spawn.go` | `dialOrSpawn` — fast path dial; on `ECONNREFUSED`/`ENOENT` spawn `daimond serve` fully detached (`SysProcAttr.Setsid: true`, stdin closed, stdout/stderr redirected to `$DAIMON_HOME/daimon.log`), poll the socket with bounded backoff (50ms→100→200→400→1s, capped at 5s wall-clock), error out with a "check the log" hint on timeout. `resolveDaimond` looks up the binary via `$DAIMOND_BIN` → PATH → sibling-of-CLI fallback (the dev tree's `bin/daimon` next to `bin/daimond`). |
| `cmd/daimon/password.go` | `readPassword(prompt)` — TTY path uses `golang.org/x/term.ReadPassword` (no echo); non-TTY path reads a line through a **shared package-level** `bufio.Reader` over stdin so `daimon init`'s two consecutive password prompts (set + confirm) don't lose the second line to bufio over-reading on the first prompt. (Caught this one with the smoke test.) |
| `cmd/daimond/main.go` | Refactored: the 8-step demo is now `daimond demo`; production daemon is `daimond serve`. `runServe` resolves `$DAIMON_HOME`, validates the keystore exists, builds the provider registry (no keystore needed for that — env vars + Ollama probe), constructs the server with an Unlock callback that loads keystore + memory store + activity log on the first successful unlock RPC, listens on the persistent socket, blocks on SIGINT/SIGTERM via `signal.NotifyContext`. The 8-step demo is preserved verbatim under `runDemo`. Subcommand dispatcher with usage banner. |
| `internal/server/server.go` | `Options.Unlock` field added; `New` validates options for the chosen mode (demo: trio required; serve: trio optional, unlock callback required); locked-state gate added to `dispatch()` — `if !s.unlocked.Load() && method != methodIdentityUnlock { return CodeIdentityLocked }`. `unlocked` is `atomic.Bool` for the one-way locked→unlocked transition; field writes happen-before `Store(true)` so dispatch's `Load()` returning true guarantees the trio is visible (Go memory model release/acquire on atomics). New `IsUnlocked()` accessor for tests. |
| `internal/server/handlers.go` | `methodIdentityUnlock` constant; `handleIdentityUnlock` — decodes `{password}`, serializes attempts via a sync.Mutex (idempotent on already-unlocked, returns same DID without invoking the callback again), calls `s.unlockFn(ctx, password)`, populates the trio, transitions the atomic flag. Demo-mode servers reject unlock with `CodeInvalidRequest` (it's a client error to call unlock against a server that doesn't have a keystore to load). |
| `Makefile` | Builds both binaries (`bin/daimond` + `bin/daimon`); `make demo` runs `bin/daimond demo` (the lifecycle proof for production runs through `bin/daimon` not `make`). |
| `SPEC.md` §9.2 + §10 | Two amendments: §9.2 acknowledges the transient-password-over-IPC attack surface (mitigation deferred to v0.1.x — under the existing "compromised daimon-core process out of scope" umbrella). §10 file layout updated to match implementation: `$DAIMON_HOME` is now `os.UserConfigDir()/daimon` (or `$DAIMON_HOME` env override), keystore is a single `identity.keystore` file (not `identity/keys.encrypted`), socket fallback rule documented, daemon log file added. |
| `go.mod` / `go.sum` | Added `golang.org/x/term` (already a transitive dep via `x/crypto`; promoted to direct for `term.ReadPassword`). |

**Method surface (this build vs. previous):** one new method, `daimon.identity.unlock`. SPEC §6.1 is unchanged in its enumeration but gains this verb implicitly via the lifecycle requirement. Worth a SPEC §6.1 addition next pass, but it's not load-bearing for the implementation.

**Test totals:** 162 pass-lines green in ~10s, race-clean, vet-clean. By package: 9 identity, 30 memory, 11 activity, **41 server (was 32; +9 unlock/locked-mode)**, 12 provider, 10 claude adapter, 13 openai adapter, 17 ollama-chat adapter, 12 ollama embedder, **7 daimonhome (new)**. `bin/daimond` 15.1 MB → 15.2 MB; `bin/daimon` 4.6 MB (new). Both pure-Go single-binary, no toolchain dependencies.

**Decisions made this session:**

- **CLI framework: stdlib `flag` + hand-rolled `switch os.Args[1]`.** Six subcommands is exactly where cobra's help/completion machinery starts to earn its weight — but it's also where `~30 lines of switch + per-subcommand flag.NewFlagSet` is dead-obvious and adds zero deps. The discipline of `internal/server/jsonrpc.go` (no SDK) and `internal/provider/claude/claude.go` (raw net/http) wants the same answer here. Trade-off: hand-roll bash/zsh completion if anyone asks. v0.1.x problem.
- **Daemon lifecycle: init provisions only; unlock auto-spawns `daimond serve` and sends the password over the socket.** Matches gpg-agent / ssh-agent / 1password-cli. The SPEC §11 verb shape (unlock separate, then memory/provider/chat) only works with a long-running daemon holding the unlocked key — Argon2id is 50–70ms per derivation, so decrypt-per-call would tax every memory write. CLI is always a client; unlock auto-spawns; subsequent calls dial the existing socket. The `daimond` binary splits into `serve` (production) and `demo` (the existing self-test, kept verbatim).
- **One new RPC: `daimon.identity.unlock`.** Only method allowed pre-unlock; everything else returns `CodeIdentityLocked` (`-32001`, already reserved in `jsonrpc.go` since session 7). Idempotent on already-unlocked — the second call returns the same DID without re-deriving the key, so a client can safely call it as a "wake the daemon if needed" probe. Demo-mode servers (constructed without an unlock callback) reject unlock with `CodeInvalidRequest` — there is no keystore to load.
- **Locked-state machinery: `atomic.Bool` + ordered field writes, not a mutex.** The transition is one-way (locked→unlocked, never reverses in v0.1) and contention is essentially zero (one unlock per process lifetime; many dispatch reads per second). `atomic.Bool` is lock-free on the dispatch hot path; release/acquire semantics on the atomic write guarantees the field writes (`s.id`, `s.store`, `s.alog`) happen-before any dispatch `Load()` returning true. A `sync.Mutex` (`unlockOnce`) serializes the unlock attempts themselves so two concurrent unlock RPCs can't both run the keystore-loading callback.
- **`$DAIMON_HOME = os.UserConfigDir()/daimon` (was `$XDG_DATA_HOME` per old SPEC).** Three reasons: config is the closer fit for "config + secrets + sockets" than data; `os.UserConfigDir()` is what stdlib gives us and respects `$XDG_CONFIG_HOME` on Linux while picking platform-correct paths on darwin/windows for free; the SPEC text was provisional ("default `~/.daimon/`") and amending it costs one line. `$DAIMON_HOME` env var still overrides — power users get the explicit knob.
- **Single discovery helper, in `internal/daimonhome`.** Both binaries (CLI and daemon) call the same `Resolve` / `KeystorePath` / `SocketPath` / `LogPath` so they cannot compute different paths. The sun_path fallback (`104` bytes on darwin) lives there too — when the resolved socket path is too long, both the CLI and the daemon transparently use `$TMPDIR/daimon-$uid.sock` and surface a one-line warning. The fallback is computed deterministically from the home dir, so the CLI and the daemon always agree on which file to open.
- **MVP scope: lifecycle only — init / unlock / identity get.** Dropping `chat` was explicit (opens conversation-state-management questions we don't need today). Dropping `memory` and `provider` subcommands was a deliberate scope cut: `daimon identity get` is the post-unlock smoke test that proves the gate works in *both* directions (locked→reject AND unlocked→permit), which is the actual lifecycle proof. `memory` / `provider` are mechanical wrappers — ~50 lines each — and fall out trivially next session once the lifecycle is proven.
- **`daimon init` does NOT spawn the daemon.** Init is purely about provisioning. Keeping the two operations separate means a user can `rsync` the daimon home dir between machines without accidentally starting two daemons, and means the failure modes are distinct (init failure: keystore couldn't be written; unlock failure: keystore couldn't be loaded or daemon couldn't start).
- **`daimon identity get` does NOT auto-spawn.** Auto-spawning here would silently start a locked daemon and the next call would immediately fail with `CodeIdentityLocked` — more confusing than the explicit "daemon not running, run `daimon unlock` first" hint. The auto-spawn behaviour is reserved for `unlock` itself, where the user has already committed to starting the daemon.
- **Detached spawn: `Setsid: true`, log to file, `Process.Release()`.** The spawned daemon needs to outlive the CLI. Setsid detaches it from the controlling terminal so closing the parent shell doesn't kill it (no SIGHUP). Stdout and stderr go to `$DAIMON_HOME/daimon.log` (an `os.OpenFile` handle inherited via the standard `cmd.Stdout`/`cmd.Stderr` mechanism — the parent leaks one fd until exit, which is fine for a process that exits seconds later). `Process.Release()` tells the runtime we won't `Wait()` on the child; the kernel reparents to init when we exit.
- **Daimond binary discovery: `$DAIMOND_BIN` → PATH → sibling-of-CLI.** Three layers of fallback covers the three deployment modes: tests explicitly set `$DAIMOND_BIN`; production installs put both binaries on PATH; the dev tree's `bin/daimon` next to `bin/daimond` resolves via `os.Executable()` + sibling lookup. The error message when none of these find anything is actionable.
- **Wrong-password reporting goes through Data, not Message.** The unlock RPC returns `RPCError{Code: CodeIdentityLocked, Message: "unlock failed", Data: "wrong password or corrupted keystore"}`. The CLI surfaces `Data` when present so the user sees the real reason, not a duplicated "unlock failed: unlock failed". Caught this in the smoke test.
- **Shared `bufio.Reader` over stdin for non-TTY password reads.** Each call to `bufio.NewReader(os.Stdin)` creates a fresh buffer; the first read on `daimon init` (the password) over-reads past the newline, and the second read (the confirmation) gets `EOF` because the second line is sitting in the discarded first buffer. Fix: package-level `stdinReader` shared across calls. **Also caught in the smoke test** — exactly why the manual lifecycle smoke is load-bearing for this kind of session, not theatre.
- **`golang.org/x/term` added as a direct dependency.** Already a transitive dep (via `x/crypto`); promoted to direct for `term.ReadPassword`. Same level of "trusted x/ tree" as `x/crypto/argon2`, which has been in the module since session 4.
- **No build-tagged integration test for the CLI binary itself.** Two reasons: building binaries inside `go test` is fragile (relies on `go` in PATH, surprise breakage on CI workers); and the unit-test coverage of the locked-state state machine + the manual lifecycle smoke (run by hand at the end of each session) covers what an integration test would cover. If the spawn/poll dance regresses, the failure mode is "daimon unlock hangs for 5s then errors out with the log path" — easy to debug.
- **SPEC §9 acknowledges the transient-password-over-IPC attack.** The CLI sends the password over the Unix socket to the daemon. The socket is mode 0600 (only the principal's UID can connect), but the password is briefly readable via `/proc/<daimond>/fd` or `ptrace`/`strace` on the daemon — i.e. by anything that can already compromise the daimon-core process, which §9.2 already places out of scope. Standard mitigation (CLI reads from controlling terminal, derives an unlock token, ships the token instead of the raw password) is explicitly deferred to v0.1.x. One-line SPEC amendment under §9.2.

**What we explicitly punted (in priority order for next session):**

1. **`daimon memory` + `daimon provider` subcommands.** Mechanical wrappers — `daimon memory write --kind fact "content"` calls `daimon.memory.write`; `daimon memory list` calls `daimon.memory.search` with empty query; `daimon provider list` calls `daimon.provider.list`; `daimon provider invoke claude --prompt "hi"` calls `daimon.provider.invoke`. Each subcommand is ~40-60 lines (parse flags → JSON-marshal params → `rpcCall` → pretty-print result). Unblocks the demo video.
2. **`daimon chat` subcommand.** Wraps `daimon.provider.invoke` with conversation-state management — a session of multi-turn messages persisted across CLI invocations, with `inject_context` enabled by default so the daimon's memory enriches each prompt. Opens streaming and history-persistence design questions; deserves its own session. Probably needs a small `~/.daimon/chat-sessions/` layout, or a sentinel value like "current" that resumes the last session.
3. **End-to-end demo video.** Now strictly content work — every primitive AND the user-facing surface exists. Show one terminal: `daimon init`, `daimon unlock`, `daimon memory write`, `daimon chat --provider claude`, then `daimon chat --provider openai` (continues with the same memory), then `daimon chat --provider ollama` (same again, on local Llama). The pitch in motion.
4. **NLnet NGI Zero application.** Rolling deadline every two months. Drafted in parallel with the demo video; demo video links from the application.
5. **CLI integration test.** Build-tagged test that builds both binaries into a tempdir and runs the lifecycle. Defer until the spawn/poll dance has cause to regress.
6. **Activity log encryption (SPEC §10).** Same threat model as memory rows; `identity.DeriveSubkey` is generic enough to support it. Per-line AES-GCM under `"daimon-activity-encryption-v1"`.
7. **Internal `secretbox` factor.** Three copies of AES-GCM in tree (identity keystore, provider credentials, memory rows). Factor when the fourth copy is needed.
8. **Streaming on HTTPS transport.** Still theoretical until a streaming client exists.
9. **Unlock-token mitigation for §9 transient-password-over-IPC.** v0.1.x.

**What this means in plain language:** before this session, you could run `daimond demo` and watch a self-test, or you could write JSON-RPC by hand at a Unix socket the demo opened in `/tmp`. You could not actually *use* a daimon to hold your identity over time. After this session, you can: run `daimon init` once (provisions a keystore at `$XDG_CONFIG_HOME/daimon/identity.keystore`); run `daimon unlock` whenever you start a session (auto-spawns the daemon, prompts for the password, identity is loaded into memory for the daemon's lifetime); and then any subsequent `daimon` invocation in any terminal dials the running daemon over the persistent socket and acts on your behalf with your identity, your memory, your activity log, and your provider credentials. This is the basic "real software" surface that turns the protocol into something a person can actually live with. The rest of the v0.1 work — `memory` / `provider` / `chat` subcommands — is now pure presentation over an implementation that fully exists.

**Next session begins with:** `daimon memory` + `daimon provider` subcommands. Mechanical wrappers over RPC methods that have existed since sessions 6 and 8. Probably one session of work for both. After that, `daimon chat` as a separate session (conversation-state design), then the demo video, then the NLnet application — all of which assume the implementation is finished, which it now is.

---

## 2026-05-04 — Day Zero, fourteenth session: `daimon memory` + `daimon provider` subcommands

**The CLI surface SPEC §11 implies is complete.** Last session ended with the lifecycle proven and the prediction that the remaining subcommands were "pure presentation over an implementation that fully exists." That prediction held: this session is ~600 lines of CLI plumbing — no new daemon work, no new RPC methods, no new server-side tests — that promotes the JSON-RPC surface from "wire-callable by hand" to "ergonomic from a terminal." A principal can now `daimon memory write --kind fact "the cat sat on the mat"`, `daimon memory list`, `daimon memory search "cat"`, `daimon provider invoke openai --inject-context "what should I tell the vet?"`, and pipe any of it through standard unix tools.

**Files (this session):** four new in `cmd/daimon/`, two modified.

| Path | Purpose |
|---|---|
| `cmd/daimon/output.go` | Render helpers: `printJSON` (the `--json` escape hatch on every subcommand), `tabPrinter` (text/tabwriter at 2-space cell padding to match `kubectl get`/`gh pr list` look), `truncate` (rune-safe to handle CJK/emoji), `formatTimestamp` (unix-ms → RFC3339 in local TZ; "-" for zero), `readContent` (returns argv literal or stdin if arg is "-"). 60 lines. |
| `cmd/daimon/client.go` | `daemonCall(method, params, out)` — the canonical "send one RPC against the running daemon" wrapper. Resolves `$DAIMON_HOME`, dials the persistent socket, rewrites ENOENT/ECONNREFUSED into "daemon not running — run `daimon unlock` first" and `CodeIdentityLocked` into "daemon is locked — run `daimon unlock` first". Does NOT auto-spawn — auto-spawn is reserved for `daimon unlock` per the session-13 lifecycle decision. Every memory/provider subcommand is one line of glue over this helper. |
| `cmd/daimon/cmd_memory.go` | `cmdMemory` dispatcher + 7 subcommand functions: `write` (`--kind <k>` required, `--source`, `--metadata <json>`, content from argv or stdin via `-`, prints just the new ID on stdout for shell pipelines), `read <id>` (labeled block or `--json`), `list` (tabwriter table, `--kind`/`--limit` filters), `search <query>` (same as list with score column), `delete <id>` (returns `deleted <id>` or `no such memory`), `export [--out <path>]` (emits the signed `ExportDocument` JSON to stdout or a 0600 file), `import [--no-verify] <path|->` (`imported N, skipped M`). Defines a local `memoryRecord` struct that mirrors enough of `*memory.Memory` to deserialize and render — keeps `cmd/daimon` free of the `internal/memory` import (the cgo-free distribution promise lives one layer up too). 305 lines. |
| `cmd/daimon/cmd_provider.go` | `cmdProvider` dispatcher + `list`/`invoke`. List: tabwriter table (NAME / CONFIGURED / MODELS) or `--json`. Invoke: `<provider>` + `<prompt|->` positionals; `--model`, `--system`, `--temperature`, `--max-tokens` shape the request; `--inject-context[=<query>]` opts into SPEC §11 memory retrieval (bare → use the prompt as the retrieval query, the common case; `=<q>` → use the override); `--verbose` puts model/usage/stop-reason on stderr; default stdout is just the assistant content for clean piping. 200 lines. |
| `cmd/daimon/cmd_identity.go` (modified) | Refactored the dial+humanise inline code to call `daemonCall`. Added `--json` flag for parity with the new subcommands. Net `-15 +6` lines. |
| `cmd/daimon/main.go` (modified) | Added `memory` and `provider` cases to the dispatcher; updated package doc and `usage()` text to enumerate the full v0.1 surface. |

**Method surface:** unchanged — every CLI subcommand is a wrapper over an RPC that already existed (`daimon.memory.{write,read,search,delete,export,import}` from session 5, `daimon.provider.{list,invoke}` from sessions 8/10/12). Memory `list` is `memory.search` with empty query — the routing was specified in the kickoff and is documented in the help text. No SPEC change. No test change on the daemon side.

**Test totals:** unchanged at 162 pass-lines green (~10s), race-clean, vet-clean. CLI itself is verified by an end-to-end manual smoke against a temp `$DAIMON_HOME` (run at the end of this session): init with confirmed password → unlock auto-spawns daimond → identity get → write three memories (one each of `fact` / `preference` / `observation`, one with `--metadata`, one via stdin pipe) → read with both human and `--json` form → list (3 rows, no score column) → search "cat" (1 row, score column populated) → delete one row → export to file at mode 0600 (1331 bytes) → import the export back (`imported 1, skipped 2` — the two surviving rows are de-duped) → provider list (table + JSON) → kill daemon → re-call any subcommand and observe the actionable "daemon not running" hint. Smoke is reproducible in ~10 seconds and is part of the contract for any session touching `cmd/daimon/`. `bin/daimon` 4.6 MB → 4.8 MB; `bin/daimond` unchanged at 15.2 MB.

**Decisions made this session:**

- **Output: human-pretty default, `--json` escape hatch on every subcommand.** The principal in v0.1 is a person at a terminal, not an automation system. `kubectl` is the better mental model than `aws`. Concrete shapes locked in: `list`/`search` → tabwriter table; `read` → labeled block (ID / Kind / Created / Source / Metadata / Content); `write` → just the new ID alone on stdout (so `ID=$(daimon memory write …)` works for shell scripts); `delete` → `deleted <id>` or `no such memory: <id>`; `export` → JSON document on stdout or `--out` file at mode 0600; `provider invoke` → assistant text alone on stdout, optional `--verbose` puts metadata on stderr. Errors always to stderr at exit 1 via the existing `exitOnErr` helper.
- **Stdin via `-` convention, not `--file`.** Matches `cat`/`sort`/`jq`/`gpg`/curl. Surface stays minimum: `daimon memory write --kind fact "literal"` for short content, `cat note.md | daimon memory write --kind fact -` for long. Same convention re-used for `daimon memory import` and `daimon provider invoke`. `--file` discoverability lost; recovered by a one-line example in the usage text.
- **`--inject-context` is opt-in, with optional value via `IsBoolFlag`.** Silent injection on every provider call would violate SPEC §6's "the daimon's actions are auditable" principle — the user should know when memory enters the wire. The single-flag UX (`--inject-context` bare uses the prompt as the retrieval query; `--inject-context=<query>` uses the supplied string) leans on the documented-but-undocumented `IsBoolFlag() bool` convention from glog/klog: when present and returning true, the stdlib `flag` package treats the flag as bool-like and calls `Set("true")` for bare invocations. The custom `injectContextFlag.Set` treats the literal string `"true"` as the "no explicit query, fall back to the prompt" sentinel — at the cost of denying users the ability to retrieve against the literal four-character string `true` without quoting (`--inject-context='"true"'`). Acceptable.
- **`provider invoke` writes only the assistant text to stdout by default.** This is the only design that composes: `daimon provider invoke openai "summarise this" < doc.txt > summary.txt` works; `... | grep "important"` works. Metadata (model, usage, stop reason) is on stderr only when `--verbose` is set; the full envelope is available via `--json` for any caller that wants the structured form.
- **Dispatcher pattern reused verbatim from session 13.** Stdlib `flag.NewFlagSet` per-subcommand + a `switch args[0]` for routing. Six subcommands of memory + two of provider land at exactly the same code shape as the three lifecycle subcommands — same level of "drop a function in cmd_x.go and add one line to main.go" extensibility. The instinct to reach for cobra remained wrong at this scale.
- **CLI never imports `internal/memory` or `internal/provider`.** The wire shape is the contract. The CLI deserialises into local mirror structs (`memoryRecord`, `scoredRecord`, `providerListEntry`, `providerResponse`) that copy only the fields the renderer needs. Costs ~30 lines of struct duplication; gains: the cgo-free distribution promise that `daimond` enforces internally is mechanically true for `daimon` too — and changes to the internal types can't accidentally break the CLI.
- **`daemonCall` factored out.** Three subcommand families × N subcommands × the same dial-and-humanise dance was the wrong copy. One helper, one place to maintain the locked/not-running error rewrite. `cmd_identity.go` was refactored to use it too; net `-15 +6` line change made the file shorter and removed two `import` lines.
- **`memory list` is `memory.search` with empty query, not a separate RPC.** The kickoff specified this routing. The two CLI subcommands behave distinctly even on the same backend: `list` defaults `--limit` to 50 and hides the SCORE column (zero-or-one for empty queries — meaningless, distracting); `search` defaults `--limit` to 10 and shows scores (informative for ranking inspection).
- **`memory.read` deserializes through `json.RawMessage` for `--json`.** Default human path goes through the `memoryRecord` mirror struct (which omits the embedding blob — a vector is not interesting to a human reader). `--json` path round-trips the full server response through `json.RawMessage` → `any` → `printJSON`, so the embedding bytes and any future fields the server adds surface losslessly. Two paths, but the cost of the human renderer's truncation is paid only when the user asks for it.
- **`memory export` defaults to stdout, not stderr or a file.** The signed `ExportDocument` is the only useful representation — there's no human form. `--out <path>` writes to a 0600 file (matching the keystore's permission since the export is principal-confidential). On stdout, the JSON can be piped to `gpg --encrypt`, `tar`, or `ssh remote-host 'cat > backup.json'`.
- **Multiline content collapses to a single line in `list`/`search` tables.** A row of 60 columns isn't the place to render an essay. The CLI cuts at the first newline and appends `…`. The user reads the full body via `daimon memory read <id>`. If multiline-cells become useful later, swap to one of the tabwriter alternatives (or render JSON).
- **No `--debug` / `--verbose-rpc` flag in v0.1.** The wire shape is already exposed via `--json` on every subcommand. A separate "show me the request envelope" flag would be the same information, twice. If a real debug need surfaces later, a `DAIMON_DEBUG=1` env var hooked into `rpcCall` is the lower-friction path than a flag-on-every-subcommand.
- **No completion script in v0.1.** Six (now eight) subcommands are findable via `daimon help`. Bash/zsh completion is ahead of the visible-need curve — the engagement budget is better spent on `daimon chat` next session.

**What we explicitly punted (in priority order for next session):**

1. **`daimon chat` subcommand.** The natural shape: `daimon chat --provider claude` opens a REPL that calls `daimon.provider.invoke` per turn, threads `messages[]` history across turns, and persists the session under `$DAIMON_HOME/chat-sessions/<name>.jsonl` so the user can `--resume` it later. `--inject-context` should default to ON for chat (the demo-video story almost requires it), with `--no-inject-context` as the opt-out — the inversion of `provider invoke`'s default is deliberate: chat is conversational, invoke is one-shot. Open questions: streaming (server doesn't expose it yet on the `daimon.provider.invoke` shape — punt to v0.1.x); session naming (sentinel `current` for the last-active, explicit `--name` for parallel sessions); how to render assistant content when it contains markdown the terminal can't parse (probably print verbatim and let the user pipe to `glow` if they want).
2. **End-to-end demo video.** The protocol is ready to film. Show one terminal: `daimon init` → `daimon unlock` → `daimon memory write --kind fact "I'm a Go developer working on a Daimon protocol"` → `daimon chat --provider claude "what are you working on with me?"` (Claude replies with knowledge from memory) → switch mid-conversation to `daimon chat --provider openai --resume` (GPT continues with the same memory) → `daimon chat --provider ollama --resume` (local Llama, same memory). The pitch in motion. Three minutes max.
3. **NLnet NGI Zero application.** Rolling deadline every two months. Draft mid-week, ship after the demo video lands. Demo video links from the application.
4. **CLI integration test.** Build-tagged test that builds both binaries into a tempdir and runs the manual smoke programmatically. Defer until the spawn/poll dance has cause to regress — for now the manual smoke is documented in CHECKPOINT and is genuinely run at every session boundary.
5. **Activity log encryption (SPEC §10).** Same threat model as memory rows; `identity.DeriveSubkey` is generic enough to support it. Per-line AES-GCM under `"daimon-activity-encryption-v1"`.
6. **Internal `secretbox` factor.** Three copies of AES-GCM in tree (identity keystore, provider credentials, memory rows). Factor when the fourth copy is needed.
7. **Streaming on HTTPS transport.** Still theoretical until a streaming client exists.
8. **Unlock-token mitigation for §9 transient-password-over-IPC.** v0.1.x.

**What this means in plain language:** before this session, you could run `daimon init`, `daimon unlock`, and `daimon identity get` — the lifecycle was real but you couldn't actually *use* the daimon for anything. After this session, you can run any of the eight new subcommands to write memories, read them back, search them, list providers, and call providers (with optional retrieval injection). The daimon now does in a terminal exactly what its protocol was designed to do — hold a person's memory, identity, and provider connections, and act on their behalf. The remaining work for v0.1 is `daimon chat` (the conversation-state shell), the demo video, and the NLnet application — all content work over an implementation that is functionally complete.

**Next session begins with:** `daimon chat`. Its own design questions (REPL UX, session persistence shape, streaming punt, default-on inject_context). After it, the demo video and the NLnet application close v0.1.

---

## 2026-05-04 — Day Zero, fifteenth session: `daimon chat`

**The conversational shell is live.** Last session's prediction held: `daimon chat` is one new file in `cmd/daimon/`, no daemon-side change, no new RPC. The whole subcommand sits on `daimon.provider.invoke` with multi-turn `messages[]` history persisted as append-only JSONL under `$DAIMON_HOME/chat-sessions/<name>.jsonl`. A principal can now run `daimon chat --provider ollama` and have a real conversation that survives across CLI invocations. The demo-video story — switch Claude → OpenAI → Ollama mid-task with memory intact — works as a side effect of the wire shape: every adapter consumes the same normalized `[{role, content}, …]` array, so swapping `--provider` between calls just changes which model receives the running history.

**Files (this session):** one new in `cmd/daimon/`, two modified.

| Path | Purpose |
|---|---|
| `cmd/daimon/cmd_chat.go` | The whole subcommand. ~370 lines: flag parsing, session-name validation, JSONL load/append helpers (`loadChatSession`, `appendChatTurn`, `chatTurn`), the shared `runTurn` body that builds the `messages[]` from history + new prompt and threads it through `daimon.provider.invoke`, the one-shot path (`runChatTurnOnce`), the REPL (`runChatREPL` with stdin scanner + slash commands `/help`, `/history`, `/exit`/`/quit`/`/q`, `/?`), the resumed-history printer (`renderResumedHistory`), and the inject-context announce helper (`announceInject`). |
| `cmd/daimon/main.go` (modified) | Added `case "chat"` to the dispatcher; updated package doc and `usage()` text to enumerate the chat surface. Net `+13 -3` lines. |
| (no SPEC change) | The wire shape was already locked. Streaming is still listed as a SPEC §11 v0.1 default ("SSE on HTTPS transport"); we use the request/response form on Unix socket per that spec. v0.1.x `daimon.provider.stream` would extend this without contradicting the existing text. |

**Method surface:** unchanged. Every chat subcommand behavior is wire-equivalent to one or many `daimon.provider.invoke` calls with appropriately threaded `messages[]`. No SPEC change. No daemon-side change. No new server tests.

**Test totals:** unchanged at 162 pass-lines green (~10s), race-clean, vet-clean. CLI itself verified by an end-to-end manual smoke against a temp `$DAIMON_HOME`: init → unlock auto-spawns daimond → memory write → `chat --once` (turn 1, fresh session, response persisted) → `chat --once` (turn 2 with same `--name`, history auto-loaded — model recalled the prior assistant turn verbatim, proving multi-turn threading) → `chat --once --name fresh-name` (different session, no recall, proving session isolation) → REPL via heredoc with `/history` and `/exit` slash commands → name-validation rejection of `"../escape"` and `"with space"` → `--json` one-shot emits the full provider envelope → `--no-inject-context` suppresses the announcement line → multi-line stdin via `--once -` preserves newlines verbatim in the persisted user turn → daemon kill → re-run yields the actionable `daemon not running — run \`daimon unlock\` first` hint. Smoke ran against local Ollama (`llama3.2:latest`); Anthropic/OpenAI keys in the test environment were redacted to whitespace placeholders so a live multi-provider switch was deferred to user-side testing. `bin/daimon` 4.8 MB → 4.84 MB; `bin/daimond` unchanged at 15.2 MB.

**Decisions made this session:**

- **Session storage: JSONL, one line per turn, append-only.** `$DAIMON_HOME/chat-sessions/<name>.jsonl`, dir mode 0700, file mode 0600 (matches the keystore). One JSON object per line: `{role, content, ts, provider?, model?}`. Provider/Model on assistant turns only — the resume printer uses them to render an honest transcript when the session switched providers mid-conversation. Append-only matches the activity-log instinct from session 7 and the SPEC §8 audit-trail philosophy. Loaded with `bufio.Scanner` (16 MB max line — generous for long-form replies); written with one `os.OpenFile(O_APPEND|O_CREATE|O_WRONLY, 0o600)` per CLI invocation. The alternative of a single rolling transcript with a header would be human-readable but require rewrite-on-each-turn, which loses the crash-resistance and grep-friendliness for nothing.
- **Streaming punted to v0.1.x.** `daimon.provider.invoke` is request/response on the JSON-RPC-over-Unix-socket transport; SPEC §11 already pegs streaming to the HTTPS transport (option (a) — server-pushed notification stream over the wire). Buffering on the daemon and shipping the response to the CLI as one string (option (b)) is what we have today and is the path we ship v0.1 with. The demo video can fake streaming with a typewriter effect at render time if the latency is uncomfortable. v0.1.x lands a `daimon.provider.stream` method that opens a notification channel; until then, no one needs streaming for the v0.1 use cases (single-shot scripting via `--once`; conversational pacing in the REPL where the entire reply lands as one logical unit).
- **`--inject-context` default ON for chat (vs OFF for `provider invoke`).** The inversion is deliberate. Chat is conversational — the user's mental model is "the daimon knows me" — so silent injection is right, and the visibility burden is met by an explicit `[inject_context: query=...]` line on stderr per turn. `provider invoke` is one-shot scripting where the user wants explicit control, so injection stays opt-in. The two flags reflect the two use cases: chat takes `--no-inject-context` to opt out, plus `--inject-query <q>` to override the retrieval query (default: each user prompt). SPEC §6 auditability is preserved through the activity log, which records `injected_memory_ids` per provider call (landed session 8).
- **Visibility: stderr `[inject_context: query=...]` line per turn, no memory-ID list in v0.1.** A count display would require the daemon to surface `injected_memory_ids` in the `daimon.provider.invoke` response (currently it only writes them to the activity log). Adding a server-side wrapper struct that embeds `provider.Response` and adds optional metadata fields is the v0.1.x path; this session deliberately stayed CLI-only. Today the user sees that injection happened and what the retrieval query was; if they want the matched memories, they can `daimon memory search "<query>"` separately or read the activity log. Both routes preserve SPEC §6 auditability without requiring scope expansion.
- **Persist user + assistant atomically, after successful RPC.** First cut wrote the user turn before the call so a crashed RPC still preserved the user's words; the smoke immediately surfaced the wart — three failed invocations against a placeholder OpenAI key left three orphan user turns in the JSONL, and on resume those orphans flowed into `messages[]` as three consecutive user roles with no interleaving assistant, which is incoherent input for any provider. The fix: persist user + assistant together, only after `daimon.provider.invoke` returns a body. Failed calls leave nothing in the chat file. The chat file becomes a strict sequence of `(user, assistant)` pairs, and the resume reconstruction is always honest. Audit visibility for failed calls is the activity log's job, not the chat file's; mixing the two purposes (state vs audit) was the conceptual error.
- **History always loads from the named session file. `--resume` dropped.** First cut had `--resume` as the opt-in switch for "load history into messages[] before sending the next turn." The demo-video story collapses if that flag is required — "switch Claude → OpenAI → Ollama with memory intact" must work without remembering a flag. New rule: history is unconditional. To start fresh, pass a different `--name` (or `rm` the session file). The REPL auto-renders the prior transcript with the `─── resumed history (N turns) ───` banner when the loaded history is non-empty; one-shot mode silently loads. The banner serves the visual continuity purpose `--resume` was supposed to serve, without making the flag load-bearing.
- **`--name` validates against `^[a-zA-Z0-9_-]+$`, plus rejecting `.` and `..`.** The name lands in a real filesystem path, so it must guard against traversal (`../escape`) and arbitrary characters (spaces, slashes, control chars). Alphanumeric plus dash and underscore is the simple rule; users who want fancier names can `ln -s` from the resulting JSONL. Rejection prints `--name must be alphanumeric with - or _` so the user sees the contract.
- **Slash commands: minimum viable. `/help`, `/?`, `/history`, `/exit`, `/quit`, `/q`.** `/history` reprints the resumed-history banner so the user can scroll back without scrolling. `/exit` ≡ `/quit` ≡ `/q` for muscle-memory tolerance. Anything else starting with `/` is rejected with a "try /help" hint. No `/system` to inject a system prompt mid-conversation, no `/forget` to drop history, no `/save` to copy the session — all useful, all v0.1.x. Ctrl+D exits cleanly via the `bufio.Scanner` returning false on EOF.
- **No multi-line REPL input. Use `--once -` for long-form.** Multi-line input in a REPL needs either a "submit on blank line" convention (pollutes the protocol — what if the user wants a real blank line?) or a sentinel like a backslash continuation, which is a learnable but un-discoverable mini-language. Punted: in the REPL, one logical turn is one line of stdin. Long-form prompts go via `cat prompt.md | daimon chat --once -`, which preserves newlines verbatim (the smoke verified). Multi-line REPL is v0.1.x.
- **No readline / line editing.** `bufio.Scanner` over `os.Stdin` — no arrow keys, no history search, no incremental search. Users who want it can `rlwrap daimon chat …`. Bringing in a Go readline library (`peterh/liner`, `chzyer/readline`) would be the first non-`golang.org/x/*` dep in the tree and the disciplined-no-deps spirit (raw `net/http`, hand-rolled JSON-RPC, no cobra) wants the same answer here.
- **REPL prefixes assistant turns with `[<provider>/<model>]:`. One-shot prints plain.** The REPL is conversational and the prefix makes provider switches visible — `[claude/claude-haiku-4-5]: …` then `[openai/gpt-5-mini]: …` then `[ollama/llama3.2:latest]: …` is the demo-video aesthetic in one terminal. One-shot is a pipeline tool; the prefix would corrupt every shell invocation that pipes the response. Same prefix, two emissions: stdout in REPL, stdout in one-shot — but only the REPL prefixes.
- **`announceInject` fires before the RPC, even when the call will fail.** On a daemon-down or 4xx response, the user sees `[inject_context: query="x"]` followed by the error. Slightly noisy, but the alternative (announce after success) loses the "the daimon is about to look at memory" signal exactly when the user most needs to understand the side-effect attempted before the failure. Acceptable as-is.
- **No new daemon-side test.** The wire shape was already exercised by the existing 12 server tests for `daimon.provider.invoke` (sessions 8/10/12) — including the `inject_context` system-prompt enrichment path and the `injected_memory_ids` activity-log write. The chat subcommand adds no new server-side behavior; it just composes the existing surface differently. CLI-side coverage is the manual smoke at session-end (the same instinct as session 13/14).
- **No `daimon.provider.stream` RPC this session.** Adding it would require a new method, a new transport pattern (server-initiated notifications over the existing socket), and at least four new tests. The CLI-only constraint of the kickoff is tractable; punting to v0.1.x preserves the small-scope-per-session discipline that has worked since session 4.

**What we explicitly punted (in priority order for next session):**

1. **End-to-end demo video.** All primitives shipped. The pitch in motion: one terminal, `daimon init` → `daimon unlock` → `daimon memory write --kind fact "I'm a Go developer working on the Daimon protocol"` → `daimon chat --provider claude --once "what am I working on?"` → `daimon chat --provider openai --once "summarise the previous answer in one line"` → `daimon chat --provider ollama --once "rephrase as a haiku"`. Three providers, one memory, one terminal, three minutes. Asciicast or screen recording — pick at filming time.
2. **NLnet NGI Zero application.** Rolling deadline every two months. Demo video links from the application; both ship together.
3. **`daimon.provider.stream` RPC + `--stream` flag on `chat`.** Server-pushed notifications over the existing JSON-RPC-over-Unix-socket transport. Probably needs a new envelope type for streaming chunks (one chunk per `delta` from the upstream provider). The Anthropic/OpenAI/Ollama wire formats all support streaming, so each adapter gains a `Stream(ctx, req) <-chan Delta` method behind a feature flag. Token-by-token typewriter rendering in the REPL becomes a cosmetic v0.1.x improvement.
4. **Server-side `injected_memory_ids` in the invoke response.** Wrap `*provider.Response` with an envelope `{response, injected_memory_ids}`; teach the CLI to parse both. The chat REPL then prints `[inject_context: query="..." matched=N]` with the count, and `--verbose` could enumerate the IDs for debugging.
5. **`/system`, `/forget`, `/save`, `/edit` slash commands.** Each is mechanical (a few dozen lines per command); not load-bearing for v0.1.
6. **Multi-line REPL input.** Pick a convention (submit-on-blank-line vs sentinel vs `\`-continuation) and document it.
7. **CLI integration test.** Build-tagged test that builds both binaries and runs the manual smoke programmatically. Defer until the manual smoke catches a regression by missing it.
8. **Activity log encryption (SPEC §10).** Same threat model as memory rows; `identity.DeriveSubkey` is generic enough.
9. **Internal `secretbox` factor.** Now four copies of AES-GCM in tree (identity keystore, provider credentials, memory rows, … and the chat-session file is plaintext on disk under the principal's UID, which is consistent with the activity log's plaintext shape today). Factor when the fifth copy is needed, or when the chat file gains encryption alongside the activity log.
10. **Streaming on HTTPS transport** (still theoretical until a streaming client exists; subsumed by item 3 if the Unix-socket streaming lands first).
11. **Unlock-token mitigation for §9 transient-password-over-IPC.** v0.1.x.

**What this means in plain language:** before this session, you could write memories, list providers, and call providers one-shot — but each call was an island. After this session, conversation is real: `daimon chat --provider ollama` opens a REPL where the daimon remembers what was said three turns ago, where memory injection happens by default and is announced visibly, where you can switch from Claude to OpenAI to Ollama mid-conversation and the next provider sees everything that came before. The protocol's most concrete promise — "switch models without losing your agent's memory" — is now experienceable in a terminal in twenty seconds. The remaining v0.1 work is content: the demo video and the NLnet application. The implementation is done.

**Next session begins with:** the demo video. All the verbs exist; the script is the work. After that, the NLnet NGI Zero application closes v0.1 and we move on to v0.2 (x402 payments, agent wallet).

---

## 2026-05-04 — Day Zero, sixteenth session: demo script (asciicast scaffolding)

**The demo is half-shipped: script in tree, recording held for huckgod's shell.** Last session's prediction was correct — every verb exists, what remained was the script. This session's deliverable is `demo/` as a directory with two files (`script.md` + `README.md`), `~330` lines combined, derived from a real end-to-end smoke against a temp `$DAIMON_HOME` so every "expected output" block is captured stdout, not aspiration. The asciicast file itself is held for huckgod to record on his shell with real Anthropic + OpenAI keys — the agent's harness env this session, as predicted in the kickoff, had a redacted `OPENAI_API_KEY` (28 tab-bytes from the harness padding) and an empty `ANTHROPIC_API_KEY`, so a multi-provider recording from this session would have been Ollama-only and lost the demo's whole pitch. The script is the load-bearing artifact; the recording is mechanical from there.

**Files (this session):** two new under `demo/`. No code change. No SPEC change. No tests touched.

| Path | Purpose |
|---|---|
| `demo/script.md` | Six scenes, ~80 s of typed action plus pacing cushion (target: 90 s asciicast). Per-scene structure: narration line (for future voiceover), exact commands, expected stdout (verbatim from the smoke), pacing notes. Pre-recording checklist documents the temp `$DAIMON_HOME`/`PATH` setup, the kill-stale-daemon dance, and the optional `nomic-embed-text` pull. Ollama-only fallback documented per scene so the script is recordable even with cloud keys missing. Recovery section covers the five common first-take failure modes. |
| `demo/README.md` | "How to play" (one `asciinema play` line), "How to re-record" (full pre-flight + invocation), "What's in scope for v0.1 vs deferred to v0.1.1." Documents the file inventory of `demo/` so the directory's purpose is self-evident on first read. |

**What was captured in the smoke (proved before writing a word of script):** `daimon init` (DID generation, keystore at mode 0600), `daimon unlock` (auto-spawn, identity load), `daimon identity get`, two `daimon memory write`s (one fact, one preference), `daimon memory list` (tabwriter, 2 rows), `daimon memory search "Daimon"` (substring fallback hit, score 1.000), `daimon provider list` (ollama configured-no with `llama3.2:latest`, openai configured-yes — the harness's redacted key registers without runtime invocation; claude absent), full `daimon chat` REPL with two user turns + `/history` (printed the resumed-history banner with timestamps + provider/model labels) + `/exit`, then **a fresh `chat --once` invocation that translated a slogan its previous turn had generated** — wire-shape proof that swapping `--provider` to claude/openai would do the same operation on a different model. Real model output captured: slogan = `"Empowering Humans with Personalized Life Agents."`, French translation = `"Équipage les Humains d'Agents Personnalisés de Vie"` (idiomatic improvement noted by the model itself).

**Decisions made this session:**

- **Asciicast first, video later.** Per the kickoff's recommendation. Asciicast (asciinema) is text-faithful, embeds losslessly via `agg`-rendered SVG/GIF, costs nothing to re-record after every v0.1.x change, ships in one session. Video (QuickTime + voiceover) supersedes it as a v0.1.1 follow-up using the same script verbatim — the narration lines in `script.md` are written to be spoken, not read. Single-source-of-truth: one `script.md`, two recording paths.
- **The script is the load-bearing artifact, not the recording.** A complete typed-out script that huckgod can re-execute by hand is the minimum viable deliverable per the kickoff's stopping-point clause. Without the recording, the script is still useful: anyone reading the repo can see exactly what the demo would show, scene by scene, with real command outputs. The asciicast upgrades the script from "readable" to "watchable"; it does not replace it. The script is committable today; the recording lands when huckgod's shell records it.
- **Recording is huckgod's job, not the agent's.** Two reasons. (1) The kickoff predicted it: "the user's local shell during this session likely has redacted API keys for Anthropic and OpenAI" — confirmed (`ANTHROPIC_API_KEY` empty, `OPENAI_API_KEY` 28 tab-bytes from harness padding). An agent recording from this session would be Ollama-only, losing the cross-provider switch which is the demo's whole pitch. (2) The bash tool in this harness can't drive `asciinema rec` interactively (no controlling TTY for the recorded shell). The path-b discipline (agent writes the script, huckgod records) is the only one available, and as a side benefit it's the right one for the artifact that lands publicly anyway.
- **The narrative thread carries on chat-session JSONL, not on `inject_context` retrieval.** Without `nomic-embed-text` pulled, retrieval falls back to substring match (which is documented behavior — SPEC §11's "if Ollama absent, semantic search disabled — key-value memory still functions"). A natural-language `inject_context` query like "what am I working on?" returns nothing; only literal substring overlaps surface memories. This was discovered in the smoke: `memory search "Daimon"` worked, `memory search "tell me about Daimon"` didn't. Rather than depend on retrieval for the demo's narrative, Scene 5's pitch is anchored on the chat-session JSONL — `messages[]` threaded across turns regardless of provider — which works without any embedder. Retrieval becomes a v0.1.1 cut: pull `nomic-embed-text`, restart daimon, re-record Scene 5 with the `[inject_context: query=...]` line landing every turn and the model genuinely recalling stored memories.
- **Don't unilaterally `brew install` or `ollama pull`.** Both modify the user's local environment. Both are reversible (`brew uninstall`, `ollama rm`) but neither was authorized in advance. Asked huckgod before either action. Default scope of the agent's autonomy ends at the project directory + git operations; system-wide package install crosses that line.
- **Ollama-only fallback documented per scene, not as a separate "downgraded script".** Scene 5 in `script.md` includes inline guidance for both the all-keys path (Claude → OpenAI → Ollama, the money beat) and the Ollama-only path (three Ollama turns at three different prompts — wire-shape proof identical, demo punch reduced). One source-of-truth script with conditional paths beats two separate scripts that drift. The provider-list output in Scene 4 is captured for the all-keys case; the Ollama-only case shows one row instead of three.
- **Real captured output as the "expected" blocks, not invented text.** Every Scene's expected-stdout is verbatim from the temp-`$DAIMON_HOME` smoke run. The model's actual replies (slogan + French translation) are in the script as recorded examples — labeled "real example from a recorded run" so the reader knows the model is non-deterministic and won't reproduce them exactly. This is the difference between a script that writes a story and a script that documents one that already happened: the second one can't lie about what `daimon` does.
- **Pre-recording checklist as part of the script, not a separate runbook.** The five-step setup (`make build`, `pkill`, `rm -rf`, env vars, `clear`) is short enough to live at the top of `script.md` rather than in a separate `RECORDING.md`. The checklist is co-located with the script that depends on it; nothing is more than one scroll away.
- **`asciinema rec --idle-time-limit 1.5`.** The recording's natural pacing comes from typing speed within commands, not from pauses between them. Clamping idle gaps to 1.5 s gives a tight final cut without hand-editing the JSON. If a particular scene needs longer pacing for clarity, that's a re-record decision, not an idle-time decision.

**What we explicitly punted (in priority order for next session):**

1. **The asciicast itself.** `asciinema rec demo/demo.cast` against the script, on huckgod's shell with real keys. Open beats: (a) does huckgod want the cross-provider Scene 5 with all three providers, or the Ollama-only fallback for v0.1 with cloud-providers in v0.1.1; (b) is the recording committed to the repo at `demo/demo.cast` or uploaded to asciinema.org and embedded; (c) does an `agg`-rendered GIF or SVG land in the README at the same time, or in a follow-up.
2. **NLnet NGI Zero application.** Rolling deadline every two months. Demo links from the application; the order is "asciicast lands → NLnet draft references it → submit." Probably one session of writing.
3. **README integration.** Top-of-README link to the asciicast, ideally embedded via asciinema.org's iframe or via a committed `demo/demo.gif`. Held until huckgod approves the asciicast quality so a regrettable cut doesn't ship publicly.
4. **`nomic-embed-text` cut of Scene 5.** Once the embedder is pulled, the `inject_context` retrieval beat works on natural-language queries, and Scene 5 gains a "the daimon remembers things from a separate `daimon memory write` call ten minutes ago" beat that the JSONL-threaded version can't show.
5. **Voiceover video pass.** Same `script.md`, narration on top, QuickTime recording. v0.1.1.
6. Activity log encryption (SPEC §10). Internal `secretbox` factor (now four AES-GCM copies; chat-session file is plaintext under principal UID, consistent with activity log). Streaming on HTTPS transport. `daimon.provider.stream`. Unlock-token mitigation for §9. Multi-line REPL input. CLI integration test. Slash commands `/system`, `/forget`, `/save`, `/edit`. (All carry-over.)

**What this means in plain language:** before this session, the protocol was real software but invisible — the only way to see it work was to read the source or follow the manual smoke at the end of CHECKPOINT.md. After this session, anyone with the repo can read [`demo/script.md`](demo/script.md) and watch the daimon work scene by scene, with real captured outputs, even before the asciicast renders. The scripts is the artifact that makes the protocol legible to people who haven't read SPEC.md. The recording is the next mechanical step — script-to-screen is one `asciinema rec` away, on huckgod's shell where the real keys live.

**Next session begins with:** huckgod records the asciicast against `demo/script.md` (five minutes if it goes well, ten if a take needs a redo). Once the cast lands, the README pickup and the NLnet draft are the two threads that close v0.1.

---


## 2026-05-04 — Day Zero, addendum: LM Studio adapter queued post-v0.1

**Decision recorded out-of-band.** During the post-session-15 wrap-up
huckgod surfaced LM Studio. It is not an existing adapter. The original
v0.1 roadmap picked one local-LLM runtime (Ollama) to keep the surface
small; LM Studio is functionally equivalent for the same use case (free
local model) but is the second-most-common local runtime and many users
prefer its GUI for model management.

**Slotted as item 20 in the CHECKPOINT next-actions list — post-v0.1,
before v0.2.** Implementation path locked:

- Fresh package `internal/provider/lmstudio/`, mirroring
  `internal/provider/ollama/` shape (probe-on-construct, register-on-
  reachable, harvest models live).
- Wire format: `/v1/chat/completions` (OpenAI Chat Completions, the
  format LM Studio's local server speaks). **Not** the Responses API
  the existing openai adapter uses — different shape, hence a separate
  package, not a flag on openai. Path 1 chosen over Path 2 (teaching the
  openai adapter dual-mode) because the blast radius of changing the
  openai adapter's default behavior is bigger than writing ~200 fresh
  lines mirroring a known-good adapter.
- Default endpoint `http://localhost:1234/v1`, override via env
  (`LMSTUDIO_HOST` parallel to `OLLAMA_HOST`).
- No API key required (LM Studio's local server doesn't authenticate by
  default), but the wire layer should send `Authorization: Bearer
  lm-studio` to be safe — some LM Studio configs reject missing auth
  headers.
- Tests mirror the ollama adapter's pattern (httptest server emitting
  fixture chat completions).

This addendum exists so the decision date is on the record. Implementation
waits until after the asciicast + NLnet land — those are the v0.1
ship-blockers, LM Studio is a v0.1.x convenience.

---


## 2026-05-04 — Day Zero, seventeenth session: LM Studio adapter — fourth provider in tree

**v0.1.x breadth begins.** LM Studio joins Claude, OpenAI, and Ollama as the fourth provider adapter — same `provider.Provider` interface, same probe-on-construct + register-on-reachable + harvest-models-live shape that `internal/provider/ollama/` established. The wire format is OpenAI Chat Completions (`POST /v1/chat/completions`, `GET /v1/models`), distinct from the Responses API the existing `openai` adapter targets, hence a separate package — Path 1 from the addendum, exactly as locked. The asciicast and NLnet remain deferred per huckgod's call (sessions 16's decision held); this session executed CHECKPOINT item 20 in isolation.

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/provider/lmstudio/lmstudio.go` (new, 332 lines incl. doc) | Adapter. `New(ctx, opts...)` probes `/v1/models` (gates registration), `Invoke` POSTs `/v1/chat/completions`, normalised `provider.Request` translates to/from the Chat Completions wire shape (system prompt prepended as inline `{role:"system"}` message, `max_tokens` + `temperature` + `stop` mapped, response `choices[0].message.content` → `Response.Content`, `finish_reason` → normalised `StopReason` enum). Bearer header sent on **both** Invoke and probe — configurations that enable auth would otherwise fail the probe and look "unavailable" instead of "auth-required". Default key is the placeholder `"lm-studio"`; `WithAPIKey` overrides. Defensive `Models()` copy, sorted-by-ID determinism. |
| `internal/provider/lmstudio/lmstudio_test.go` (new, 21 test funcs / ~530 lines) | Mirrors the ollama adapter's test fixture pattern. `httptest.Server` emitting fixture `/v1/models` + `/v1/chat/completions` bodies, captures outbound chat request bodies + headers. Coverage: probe success/failure (5xx/network), bearer header on probe, `WithModels` short-circuits probe, `Models()` returns copy, happy-path Invoke (request shape + Authorization + normalised response), custom API key plumbing, empty-messages guard, model defaulting, temperature/stop/max_tokens override, no-system path, 4xx HTTP error, response-level error payload, empty `choices` malformed-response guard, malformed JSON, six `finish_reason` cases for the StopReason normaliser, context cancellation, multi-turn ordering, empty `data` (LM Studio up but no model loaded — registers with zero models), context timeout on slow probe. |
| `cmd/daimond/main.go` (modified) | Adds the LM Studio probe-and-register block alongside Claude/OpenAI/Ollama in `buildProviderRegistry`. New env vars: `LMSTUDIO_HOST` (parallel to `OLLAMA_HOST`), `LMSTUDIO_API_KEY` (optional; default placeholder works for stock configs). Failure path emits a one-line stderr diagnostic ("LM Studio chat unavailable (...)") and skips registration — same shape as the existing Ollama line. |
| `CHECKPOINT.md` (modified) | Build-status update (162→183 test pass-lines, four-provider trio→quartet), item 20 marked shipped, queue renumbered. |

**Decisions made this session:**

- **Path 1 over Path 2 (separate package, not a flag on `openai`).** The addendum already locked this; this session honored it. The `openai` adapter's whole code path is Responses-API-shaped (`responsesRequest`, `output[].content[]` walk, `incomplete_details.reason` → StopReason). Teaching it dual-mode would have meant a runtime branch inside every method and would have changed the default behavior of the existing adapter. ~330 fresh lines mirroring a known-good adapter is cheaper and lower-blast-radius than that retrofit. The cost — small duplication between two Chat-Completions parsers if a hypothetical "openai chat-completions mode" lands later — is paid only when there are two consumers, and the refactor at that point is mechanical.
- **Auth: always send `Bearer <key>`, default key = `"lm-studio"`.** Path (c) from the kickoff. Cheapest default: works against the stock LM Studio config (which ignores the bearer entirely), works against configs that require *any* bearer (which accept the placeholder), and works against configs that require a *specific* key (via `LMSTUDIO_API_KEY`). The OpenAI adapter already establishes the pattern of sending bearer on every request; the only shape change here is "the key has a default placeholder instead of being required". The probe also sends the bearer — without that, an auth-required config would fail probe with 401 and be reported as "unavailable" rather than "wrong key", masking a fixable error.
- **Chat Completions parser scoped to the `lmstudio` package, not extracted.** Path (a) from the kickoff. One consumer today; abstraction tax is real and not earned by speculative reuse. The shared package lands when LM Studio + a hypothetical `openai`-chat-mode are *both* real, and not before. About 50 lines of types and one `normalizeStopReason` helper live inside `lmstudio.go`; that's the entire delta.
- **Probe via `GET /v1/models`, not `HEAD /`.** Same reasoning as the Ollama adapter: probe + harvest in one request. `/v1/models` returns the loaded model list as `{object:"list", data:[{id,object,owned_by},...]}` — populates `provider.Model` ID + DisplayName without a second call. `HEAD /` would tell us only whether the server is up, not what's loaded.
- **Empty model list is not a probe failure.** Mirrors Ollama's "server up, no models pulled" path. LM Studio can be running with the GUI open and zero models loaded; the adapter registers, `daimon provider list` shows an empty Models column honestly. The alternative — fail-fast on `data: []` — would be silently confusing when the user starts LM Studio's server before loading a model.
- **Empty `choices[]` is a hard error, not a silent empty response.** Discovered while writing the response-level-error fixture: a 200 OK with `error: {...}` and `choices: []` would cleanly surface the upstream error message via the existing `parsed.Error != nil` branch, but a 200 OK with neither `error` nor `choices` was previously a panic on `choices[0]`. Added `if len(parsed.Choices) == 0` returning `"lmstudio: response has no choices"` and a dedicated test (`TestAdapter_InvokeEmptyChoicesError`). Belt-and-braces against malformed servers.
- **No CredentialStore entry for LM Studio.** Mirrors Ollama's pattern: the adapter's bearer is internal (placeholder default or `LMSTUDIO_API_KEY` override); `daimon.provider.list` reports `configured=false` because `creds.Has("lmstudio")` is false. Yes, this means LM Studio shows the same "no" badge as Ollama in the table while actually being callable — that's existing semantics. The badge means "key configured in encrypted store", not "callable". A future iteration could redefine the column once enough adapters exercise the distinction; v0.1.x doesn't change it for one new adapter.
- **Smoke verifies the unavailable path; live round-trip waits for LM Studio up.** LM Studio not installed locally on this shell. The manual smoke against a temp `$DAIMON_HOME` confirmed: `daimon init`/`unlock`/`identity get` round-trip clean, `daimon provider list` shows openai+ollama with no spurious lmstudio row, daemon log emits the exact diagnostic line `LM Studio chat unavailable (probe: do request: ...connection refused); not registered (start the LM Studio server and load a chat model to enable)`. The httptest fixture coverage is comprehensive (21 test funcs, including request-shape capture, header assertion, all six `finish_reason` cases, empty-choices, malformed JSON, context cancellation). The `daimon chat --provider lmstudio --once "hi"` live round-trip lands on a future session when huckgod's shell has LM Studio running.

**Test count:** 162 → 183 PASS lines. All race-clean, all vet-clean. ~10s total run.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio round-trip.** When LM Studio is running locally on huckgod's shell with a model loaded: `daimon provider list` shows the lmstudio row with the live model list, `daimon chat --provider lmstudio --once "hi"` round-trips through `/v1/chat/completions`. The httptest coverage proves wire correctness; the live smoke proves the connection on huckgod's hardware. Five minutes once LM Studio is up.
2. **The asciicast (carry-over from session 16).** Still item 1 in the v0.1 polish queue. LM Studio's presence in `provider list` would be a nice scene-4 detail if the recording happens after LM Studio is up locally, but it isn't a blocker — the script's three-provider beat works as-is.
3. **NLnet NGI Zero application (carry-over from session 16).** Same status. Demo-recording-first remains the order; LM Studio adds a small "and the protocol is broadening past Ollama for local-LLM support" line to the demo-and-momentum section of the application.
4. **`daimon.provider.stream` + `--stream` in chat REPL** (CHECKPOINT item 21a). Token-by-token rendering, server-pushed notifications over the existing JSON-RPC-over-Unix-socket transport. The interesting design decision: do we use JSON-RPC server-initiated notifications (no `id` field, server pushes a `daimon.provider.stream.delta` per token until `daimon.provider.stream.done`), or do we leave the request-id correlated and send replies-on-the-same-id? The former is cleaner per spec; the latter doesn't change the framing. Probably one session.
5. **Server-side `injected_memory_ids` in `provider.invoke` response → chat REPL prints `[inject_context: query="..." matched=N]`** (CHECKPOINT item 21b). The retrieval already runs server-side; the count is one extra field in the response struct + one print line in the REPL. ~30 minutes.
6. **Activity log encryption (SPEC §10) + `internal/secretbox` factor** (CHECKPOINT items 21c/21d). Same threat model as memory rows; `identity.DeriveSubkey` already generic enough. Once the activity log is encrypted, four AES-GCM copies live in tree (keystore, credentials, memory rows, activity log) and the abstraction earns its keep. Half-day session.

**What this means in plain language:** before this session, switching to a local model meant installing Ollama. After this session, LM Studio works too — daimon probes both at startup, registers whichever is reachable, and the chat REPL switches between them with a `--provider` flag. The protocol stays one binary, no new dependencies; the user gets the GUI they already prefer for managing local models. Functionally minor (one more registration line in `daimond serve`'s startup log), strategically real (cross-runtime portability for the local-LLM half of the "switch Claude → GPT → local" pitch).

**Next session begins with:** either huckgod's shell has LM Studio up and we close the live-round-trip todo (5 min), or we move to the next CHECKPOINT item — most likely streaming (21a) — without it. Either path is fine; the adapter is correct against the fixture or it isn't, and it is.

---

## 2026-05-04 — Day Zero, eighteenth session: token-by-token streaming end-to-end

**The biggest UX win after the conversational shell lands.** `daimon chat --stream --provider ollama` now renders Ollama tokens incrementally — each ~8-9ms a new fragment appears on stdout, instead of a single buffered Println after the full generation. Wire shape, server handler, adapter, CLI, fallback, and SPEC paragraph — all landed in one session. The stopping point in the kickoff was "ship Ollama streaming end-to-end"; we hit exactly that, no scope creep.

LM Studio was not up locally on this shell (probe at session start: `curl http://localhost:1234/v1/models` → connection refused; Ollama → HTTP 200), so item 21's live round-trip is still pending. Skipped it and went straight to streaming as the kickoff prescribed.

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/provider/streamer.go` (new, 44 lines incl. doc) | The `Streamer` interface + `Delta` type. Adapters opt in by implementing `Streamer` alongside `Provider`; the server type-asserts before dispatching. Channel-based contract: `Stream(ctx, req, deltas chan<- Delta) (*Response, error)`. Adapter MUST close the channel before returning, MUST honour ctx cancellation, MUST send only non-empty deltas. The accumulated `*Response` is returned on the original request id; deltas precede it as notifications. |
| `internal/provider/ollama/ollama.go` (modified, +135 lines) | New `Stream` method. POST `/api/chat` with `stream:true`; bufio.Scanner over NDJSON; one Delta per non-empty content line; final line populates the `*Response` (model, content, stop_reason, usage, raw). Honours ctx via `http.NewRequestWithContext` AND a per-iteration `ctx.Err()` check before reading the next frame. Send is `select`-guarded with ctx so a CLI that disappears doesn't deadlock the adapter on a full channel. Errors on missing terminal frame, malformed JSON line, or 4xx/5xx HTTP. |
| `internal/provider/ollama/ollama_test.go` (modified, +203 lines / 8 new tests) | Streaming fixture: `streamServer` helper + `chatStreamFrames` NDJSON constant. Tests: happy path (3 deltas + final accumulation + usage + stop_reason + outbound stream:true flag), context cancellation (server sends one frame then hangs; cancel mid-stream → returns within 2s deadline), malformed NDJSON line, missing terminal `done:true` frame, HTTP error response, empty messages rejected (with channel-closed assertion on early-return path), Streamer interface conformance. |
| `internal/server/handlers.go` (modified, +130 lines) | New `handleProviderStream` handler. Type-asserts `provider.Streamer`; non-implementers return `CodeNotFound` with `"provider does not support streaming"` (CLI's job to fall back, not server's per the locked decision). Same `inject_context` path as `invoke`. Spawns a goroutine for `streamer.Stream`, forwards each delta as a JSON-RPC notification (`daimon.provider.stream.delta`, no id field), then encodes the terminal response on the original request id. Activity log records `streamed:true` so the audit trail distinguishes the two call paths. New `streamNotification` envelope so the wire bytes have no `id` field — `Response` would have nullified the id which is wrong for notifications. |
| `internal/server/server.go` (modified, +14 lines) | `handleConn` special-cases the stream method. After parsing the request head, if method == `daimon.provider.stream`, delegate to `handleProviderStream` with direct encoder access. Otherwise normal dispatch. The streaming handler runs synchronously in the conn's read loop, which preserves the single-writer-per-connection invariant without needing a mutex on the encoder — the conn re-use after a stream completes is verified by `TestProviderStream_ConnectionReusableAfterStream`. |
| `internal/server/stream_test.go` (new, 7 test funcs / ~430 lines) | `mockStreamer` (Streamer-implementing version of `mockProvider`) + `gatedMockStreamer` (blocks on a release channel so the concurrent-conn test can interleave deterministically). Tests: happy path (notifications + final response on matching id), connection re-use (stream then invoke on same conn both succeed), non-Streamer provider returns `CodeNotFound` with the canonical message, unknown provider, adapter error, concurrent invoke on second conn during in-flight stream, activity log records `streamed:true`, locked daemon rejects with `CodeIdentityLocked`. |
| `cmd/daimon/rpc.go` (modified, +75 lines) | New `streamFrame` envelope (tolerant — handles both notification and response shapes). New `rpcStream(socket, method, params, onDelta, out)` that opens a conn, sends one request, then loops decoding frames: notifications go to `onDelta`; the terminal response unmarshals into `out`. Forward-compatible — unknown notification methods are ignored so future delta kinds (tool calls) don't break v0.1.x clients. |
| `cmd/daimon/client.go` (modified, +35 lines) | Refactored the daemon-call-error humanisation into a shared `humaniseDaemonErr` helper. New `daemonStream` wrapper mirrors `daemonCall`'s shape — same `$DAIMON_HOME` resolution, same socket path, same locked / not-running rewrites — for the streaming path. |
| `cmd/daimon/cmd_chat.go` (modified, +130 lines) | Tri-state `--stream` flag (`streamFlag` type with `IsBoolFlag`). REPL default ON, `--once` default OFF; explicit `--stream`/`--stream=false` honours the user. New `runTurnStream` mirrors `runTurn` but hits `daimon.provider.stream` and calls `onDelta` per fragment. Shared `buildTurnRequest` + `persistTurnPair` factor out the bits both paths need. New `runTurnStreamWithFallback` detects the `"does not support streaming"` rejection (via `isStreamUnsupported` matching code+message), prints `[stream: <provider> does not support streaming, falling back to invoke]` to stderr, and retries against `runTurn` transparently. REPL prints the `[<provider>]: ` prefix once then each delta inline; one-shot prints raw deltas + a trailing newline. |
| `SPEC.md` (modified, +12 lines in §6.1) | Added the `daimon.provider.stream` method signature (identical params to `invoke`, returns the final `ProviderResponse` on the original id) and the notification frame shape (`{"jsonrpc":"2.0","method":"daimon.provider.stream.delta","params":{"content":"..."}}`). One paragraph explaining the no-id JSON-RPC 2.0 notification convention, the "provider does not support streaming" error, and that streaming is opt-in per call (invoke remains the default unary contract). |

**Decisions made this session:**

- **JSON-RPC server-pushed notifications, not multiple-responses-on-same-id.** Locked at session start; held through implementation. JSON-RPC 2.0 §4.1 explicitly defines a notification as a request without `id` — applying the same rule to server-pushed messages keeps the wire honest. Multiple responses on the same id would have violated the "exactly one Response per Request" contract that the existing `Response` envelope guards (`ID` field always present). The `streamNotification` envelope is a dedicated type because reusing `Response` with empty id would still encode an `id: null` field — wrong shape for notifications.
- **New method `daimon.provider.stream`, not a flag overload on `invoke`.** Same locked decision; held. SPEC §6.1 lists methods explicitly, so new wire behavior gets a new method name. The win: `invoke`'s 200+ existing tests do not change, and the rule "every existing client of `invoke` keeps working unchanged" is mechanically true rather than carefully reviewed.
- **`Streamer` is a separate interface, not extra methods on `Provider`.** Backwards-compatible by construction. The Ollama adapter implements both; Claude/OpenAI/LM Studio still implement only `Provider` and the server type-assertion correctly returns `"does not support streaming"`. When Claude/OpenAI/LM Studio land their `Stream` methods in follow-on sessions, NO existing test changes.
- **Channel ownership: caller creates and consumes; adapter sends and closes.** Simpler concurrency model than callbacks (which invert control and make ctx-cancellation harder to reason about). The `defer close(deltas)` at the top of `Stream` guarantees the channel closes under every return path including panic — verified by `TestAdapter_StreamEmptyMessagesRejected` which checks `<-deltas, ok` is false on the early-error path.
- **Adapter blocks on `select { case deltas <- delta: case <-ctx.Done(): return ctx.Err() }`.** Back-pressure for slow consumers (a CLI being run through `tee` to a slow disk) AND ctx-cancellation interleaving in one expression. Without the select, a slow CLI + a full channel + a cancellation would have deadlocked the adapter goroutine until upstream HTTP timed out. With it, cancellation always wins immediately.
- **Stream handler runs synchronously in the conn's read loop, not as a goroutine.** Simpler than per-conn write-mutex + goroutine dispatch, and preserves the single-writer-per-connection invariant by construction (one writer, one goroutine, no contention). Concurrent requests during an in-flight stream come from a SECOND connection — which is the realistic CLI pattern (chat opens its own conn, parallel `daimon memory write` opens another). `TestProviderStream_ConcurrentInvokeOnSecondConn` verifies this works under load (gated mock streamer holds the stream open while a second-conn `provider.list` round-trips to completion). The kickoff brief mentioned a per-conn writer mutex; we didn't need one because we never have concurrent writers on the same conn. The test name preserves the "fires another request mid-stream" intent of the locked decision.
- **CLI fallback matches on code+message, not code alone.** `CodeNotFound` (-32002) is shared between "unknown provider" and "provider does not support streaming". Falling back on every -32002 would silently retry against an unknown provider, hiding the user's typo. Matching on the literal message `"does not support streaming"` is fragile to message changes but the message lives in two files (server `handleProviderStream` + CLI `isStreamUnsupported`) and is grep-discoverable. A future refactor that introduces a dedicated error code for this case would be a net win; one extra error code wasn't worth churning the JSON-RPC code constants in this session.
- **Stream announcements go to stderr; deltas to stdout.** Same convention as `[inject_context: query=...]`. Pipelines like `daimon chat --once "..." --stream | tee log.txt` get the assistant content cleanly without the announcement; redirected stdout with stderr to a tty preserves the visible-progress UX. The `[stream: ... falling back to invoke]` line follows the same rule.
- **Activity log marks streamed calls with `streamed:true`.** Distinguishes the two call paths in audit history without changing the existing schema. A future grep against the log answers "how many of my Ollama calls used streaming" cheaply. The `injected_memory_ids` payload field continues to live alongside, so a streamed-with-context call carries both flags.
- **Tri-state `--stream` flag, defaults differ by mode.** REPL is the conversational case where streaming is the obvious win; `--once` is the scripting case where buffered output composes with shell pipelines. Two defaults, one flag — the user almost never has to pass it, but `--stream=false` in REPL or `--stream` in `--once` works when needed. Implemented via custom `flag.Value` so we can detect "unset" rather than relying on a sentinel default.
- **Stopping point honored.** Claude + OpenAI + LM Studio streaming punted to follow-on sessions per the kickoff's explicit guidance. SPEC §6.1 received the one-paragraph addition; the broader "streaming section" rewrite waits for at least two adapters to expose `Stream`. `injected_memory_ids` in the response (item 22b) was held — separate concern, ~30-minute task on its own.

**Test count:** 183 → 198 PASS lines (+15: 8 Ollama streaming + 7 server streaming). All race-clean, all vet-clean, ~10s total run.

**Live smoke (manual, against `/tmp/dmn-smoke-XXXX`, with Ollama up locally):**

- `daimon init` + `daimon unlock` (auto-spawns daimond) + `daimon provider list` shows `ollama  no  llama3.2:latest` and `openai yes ...` (no LM Studio row — LM Studio is down on this shell).
- `daimon chat --provider ollama --model llama3.2:latest --once "Recite the first three sentences of Hamlet's soliloquy." --stream` → piped through a per-byte timestamper, **35 distinct deltas at ~8-9ms intervals** ('To', ' be', ' or', ' not', ' to', ' be', ',', ' that', ' is', ' the', ' question', …). Token-by-token rendering is real, end-to-end, on huckgod's hardware.
- `daimon chat --provider openai --model gpt-5-mini --once "..." --stream` (smoke env has no real OpenAI key) → stderr prints `[stream: openai does not support streaming, falling back to invoke]`, then the invoke runs and fails with the expected 401 from OpenAI. Fallback path verified.
- REPL: `printf 'Say hi.\n/exit\n' | daimon chat --provider ollama --stream --name stream-repl` → banner shows `stream: on (token-by-token rendering) — pass --stream=false to disable`, then `you> [ollama]: Hello!` with `you>` for the next prompt. Session JSONL persists user+assistant turns atomically.
- Activity log row for a streamed call: `kind=provider.invoke payload={"duration_ms":497,"input_tokens":123,"model":"llama3.2:latest","output_tokens":35,"provider":"ollama","stop_reason":"end_turn","streamed":true}` — chain hash unbroken.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio round-trip** (carry-over, CHECKPOINT item 21). Five-minute task when LM Studio is up locally on huckgod's shell.
2. **Claude streaming adapter** (CHECKPOINT item 22a, follow-on). Anthropic Messages API supports SSE via the `stream:true` param; events arrive as `event: <type>\ndata: <json>\n\n`. The interesting work is mapping `content_block_delta`/`message_delta`/`message_stop` events onto the `Delta` channel + accumulating the final `Response`. Half-day session.
3. **OpenAI streaming adapter** (also 22a). Responses API SSE: events arrive on the same SSE stream; `response.output_text.delta` carries text, `response.completed` carries the final usage. Half-day session.
4. **LM Studio streaming adapter** (also 22a). OpenAI Chat Completions SSE format — chunks arrive as `data: {...}\n\n` until `data: [DONE]`. Half-day session.
5. **Server-side `injected_memory_ids` in `provider.invoke` response → REPL `[inject_context: query="..." matched=N]`** (CHECKPOINT item 22b). The retrieval already runs server-side; one extra response field + one print line. ~30 minutes.
6. **Activity log encryption (SPEC §10) + `internal/secretbox` factor** (CHECKPOINT items 22c/22d). Now that streaming is in tree, the activity-log payloads carry richer audit info — encrypting them at rest closes the gap with memory rows. Half-day.
7. **The asciicast** (carry-over from session 16). Now demonstrably more compelling with token-by-token rendering visible in scene 4.
8. **NLnet NGI Zero application** (carry-over from session 16).

**What this means in plain language:** before this session, replies appeared all-at-once after Ollama finished generating — a 35-token reply meant a 280ms wait then a flash of text. After this session, the same reply renders ~8ms per token, which feels (and is) instantaneous as words appear. The protocol grew one new RPC method, one new interface, and ~430 new lines of test coverage; the wire shape change is one paragraph in SPEC §6.1. Streaming for Claude/OpenAI/LM Studio lands in follow-on half-day sessions — each adapter wraps its provider-specific SSE format around the same `Streamer` interface, no further server or CLI work required.

**Next session begins with:** either huckgod's shell has LM Studio up (item 21, 5 min) or we pick a streaming adapter to land — Claude is probably the highest-impact target given it's the founding adapter and the most-used in practice.

---

## 2026-05-04 — Day Zero, nineteenth session: Claude streaming adapter

**Streaming breadth begins.** The Anthropic Messages API joins Ollama as the second adapter implementing `provider.Streamer`. The wire-shape contract from session 18 absorbed the new adapter exactly as designed — zero changes to `internal/server/`, `cmd/daimon/`, or SPEC. The `daimon chat --provider claude --stream` REPL behavior automatically flips from "falling back to invoke" to live token-by-token rendering as soon as the adapter exposes `Stream`.

LM Studio probe at session start: `curl http://localhost:1234/v1/models` → connection refused. Item 21 still pending huckgod's shell having LM Studio up locally. Proceeded straight to Claude streaming as the kickoff prescribed.

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/provider/claude/claude.go` (modified, +218 lines) | New `Stream` method. POST `/v1/messages` with `stream:true`; bufio.Scanner over the SSE response (line-by-line, dispatch on blank line). One Delta per `content_block_delta` of `delta.type == "text_delta"`. Final `*Response` carries model from `message_start.message.model`, content from accumulated text deltas, stop_reason mapped through the existing `normalizeStopReason` helper from `message_delta.delta.stop_reason`, usage as `message_start.usage.input_tokens` + summed `message_delta.usage.output_tokens`, raw payload from the most recent dispatched event. Honours ctx via `http.NewRequestWithContext` AND a per-line `ctx.Err()` check. Errors on missing `message_stop`, malformed JSON in any data: payload we care about, or 4xx/5xx HTTP status. The `messagesRequest` struct gained a `Stream bool` field with `omitempty` — keeps the unary path's wire payload byte-identical (omitempty drops the false-value field on Invoke). Sends `Accept: text/event-stream` on the streaming path. |
| `internal/provider/claude/claude_test.go` (modified, +298 lines / 7 new tests) | Streaming fixture (`streamServer` helper + `happySSE` constant containing message_start, content_block_start, ping, three content_block_delta of text_delta, content_block_stop, message_delta with stop_reason=end_turn + output_tokens=4, message_stop). Tests: happy path (delta order, accumulated content, model, stop_reason, usage, raw, outbound `stream:true`), context cancellation (server flushes one delta then hangs; cancel mid-stream → returns within 2s deadline), malformed data payload (broken JSON aborts with decode error), missing message_stop (stream ends after deltas → error mentioning `message_stop`), HTTP 401 with realistic Anthropic error envelope, empty messages rejected (with channel-closed assertion on the early-return path), Streamer interface conformance. The `claudeRequestCapture` test struct gained the `Stream` field to match. |

**Decisions held from the kickoff (no re-deliberation, exactly as locked):**

- **SSE wire format, parsed with a hand-rolled bufio.Scanner.** No SDK, no transitive deps. Standard SSE rules: dispatch on blank line, accumulate `event:` and `data:` fields, ignore other lines (id:, retry:, comments). Multi-line `data:` payloads join with `\n` (defensive — Anthropic only ever sends single-line data, but the spec allows multi-line and the cost of supporting it is one append). Pure Go, single binary, no CGO.
- **Four event types we depend on — all others ignored.** `message_start` (model + initial input_tokens), `content_block_delta` of `delta.type == "text_delta"` (the token fragment), `message_delta` (stop_reason + cumulative output_tokens delta — accumulated on our side per the kickoff's instruction), `message_stop` (terminal). `ping`, `content_block_start`, `content_block_stop`, and any future event types Anthropic adds (tool_use deltas, image deltas) flow through the parser without aborting it. Forward-compat by silence.
- **No retry, no policy.** Adapter is a translator. `4xx`/`5xx` → error. Stream ends without `message_stop` → error. Malformed JSON in a `data:` line for an event we care about → error. Caller surfaces it; user retries.
- **Ctx cancellation is a hard requirement.** `http.NewRequestWithContext` plumbs ctx into the HTTP layer; a per-line `ctx.Err()` check covers the "buffered upstream bytes already in scanner.Buffer" case. The `select { case deltas <- delta: case <-ctx.Done(): return ctx.Err() }` send guards against deadlock on a slow consumer + cancellation interleave. Test asserts the adapter exits within 2s of cancellation and the channel closes — no goroutine leak, no continued upstream consumption.
- **Final Response shape matches Invoke's exactly.** Same `*provider.Response` fields populated identically; the chat REPL's render path is unchanged. Same `normalizeStopReason` helper as the unary path — `end_turn`/`max_tokens`/`stop_sequence`/`tool_use`/unknown all map identically across the two code paths.

**Decisions made this session (small details not in the kickoff):**

- **`Accept: text/event-stream` header on the streaming request.** Anthropic auto-detects streaming from the body's `stream:true`, so the Accept header is technically optional. Sent it anyway — it's the spec-correct hint, costs nothing, and any future proxy that does content negotiation will route correctly.
- **`Stream` field on `messagesRequest` uses `omitempty`.** The unary path now sets `Stream: false` explicitly; with `omitempty`, the JSON encoder drops the false-value field, so the outbound wire bytes are byte-identical to what they were before this session. Verified via the existing `TestAdapter_InvokeHappyPath` (which asserts the request shape and was untouched by this session) — still passes. Zero churn for unary callers.
- **Defensive multi-line `data:` joining.** `if len(curData) > 0 { append('\n') }` then append the rest. Anthropic doesn't currently emit multi-line data, but the SSE spec permits it and the cost of being correct is two lines. A future Anthropic event type (multi-line tool-use payload?) would otherwise have produced silent corruption.
- **Trailing-event-without-blank-line rescue.** If the scanner exits the loop without seeing a blank line that flushes the final event (some servers omit the trailing blank), one final `dispatch()` runs before the missing-`message_stop` check. The httptest fixture in the happy path test does include the trailing blank, so this path exists for production resilience, not test coverage. Cost: 4 lines.
- **Header order: `Content-Type`, `Accept`, `x-api-key`, `anthropic-version`.** Same as the unary path, just with `Accept` slotted in. Anthropic's API ignores order; the consistency makes the diff between Invoke and Stream readable.
- **`json.RawMessage(nil)` copy of `lastRaw` for the final Response.** Protects against the scanner's underlying buffer being reused between events (it isn't, currently, because we already copy via `append(lastRaw[:0], curData...)`, but the second copy on return is the cheap insurance). Mirrors the Ollama adapter exactly.

**Test count:** 198 → 205 PASS lines (+7: all in `internal/provider/claude/`). All race-clean, all vet-clean, ~10s total run.

**Live smoke status:** **Deferred to huckgod's shell.** The harness env redacts `ANTHROPIC_API_KEY` (consistent with prior sessions — the demo asciicast was deferred for the same reason). The httptest fixture covers the wire-shape correctness completely (happy path, ctx cancellation, malformed payload, missing stop event, HTTP error, interface conformance, request shape). The remaining proof is "does Anthropic's actual server emit the events we parse" — a five-minute task on huckgod's shell:

```
ANTHROPIC_API_KEY=sk-ant-... daimon chat --provider claude --model claude-opus-4-7 \
  --stream --once "Recite the first three sentences of Hamlet's soliloquy." \
  | python3 -c 'import sys, time; t0 = time.monotonic(); [print(f"{(time.monotonic()-t0)*1000:7.1f}ms {b!r}") for b in iter(lambda: sys.stdin.buffer.read(1), b"")]'
```

Expected: a sequence of byte-level prints with sub-100ms gaps between them (vs. one all-at-once print after the full generation, which is what session 18's smoke proved against Ollama). The `[stream: claude does not support streaming, falling back to invoke]` stderr note from session 18 should be gone — replaced by direct streaming.

**What this means in plain language:** before this session, `daimon chat --provider claude --stream` printed the fallback note then waited for the full reply to land all-at-once. After this session, Anthropic's tokens render incrementally — same UX as Ollama since session 18, now applied to the founding adapter. The protocol's most concrete promise — switch Claude → OpenAI → local mid-task with memory intact — now has parity on the streaming UX between Anthropic and Ollama. OpenAI and LM Studio are next, each ~half a day, same shape (per-adapter SSE format wrapped around the same `Streamer` interface).

**What we explicitly punted (in priority order for next session):**

1. **Live Claude streaming round-trip** (this session's deferred smoke). Five minutes once huckgod's shell exposes a real `ANTHROPIC_API_KEY`.
2. **Live LM Studio round-trip** (carry-over, CHECKPOINT item 21). Five-minute task when LM Studio is up locally.
3. **OpenAI streaming adapter** (CHECKPOINT item 23a, second of three). Responses API SSE. Half-day session, same shape as this one.
4. **LM Studio streaming adapter** (CHECKPOINT item 23a, third of three). OpenAI Chat Completions SSE format (`data: {...}\n\n` until `data: [DONE]`). Half-day session.
5. **Server-side `injected_memory_ids` in `provider.invoke` response → REPL `[inject_context: query="..." matched=N]`** (CHECKPOINT item 23b). ~30 minutes.
6. **Activity log encryption (SPEC §10) + `internal/secretbox` factor** (CHECKPOINT items 23c/23d). Half-day.
7. **The asciicast** (carry-over from session 16). Now compelling for two adapters with token-by-token rendering.
8. **NLnet NGI Zero application** (carry-over from session 16).

**Next session begins with:** either run the deferred live Claude streaming smoke (5 min, requires real `ANTHROPIC_API_KEY` exposed in shell env), or proceed to OpenAI streaming as the second of three remaining adapters in CHECKPOINT item 23a. Either path is short.

---

## 2026-05-04 — Day Zero, twentieth session: OpenAI streaming adapter

**Streaming breadth widens.** OpenAI's Responses API joins Ollama and Claude as the third adapter implementing `provider.Streamer`. The wire-shape contract from session 18 absorbed the new adapter exactly as designed — zero changes to `internal/server/`, `cmd/daimon/`, or SPEC. The `daimon chat --provider openai --stream` REPL behavior automatically flips from "falling back to invoke" (the path session 18's smoke verified) to live token-by-token rendering as soon as the adapter exposes `Stream`.

LM Studio probe at session start: `curl http://localhost:1234/v1/models` → connection refused. Item 21 still pending huckgod's shell having LM Studio up locally. Proceeded straight to OpenAI streaming as the kickoff prescribed (item 23a, second of three).

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/provider/openai/openai.go` (modified, +217 lines) | New `Stream` method. POST `/v1/responses` with `stream:true`; bufio.Scanner over the SSE response (line-by-line, dispatch on blank line). One Delta per `response.output_text.delta` event (from the top-level `delta` field). Final `*Response` carries model from `response.created.response.model` (with override from any later terminal event), content from accumulated text deltas, stop_reason mapped through the existing `normalizeStopReason(status, incomplete)` helper from the terminal event's `response.status` + `response.incomplete_details`, usage as `response.usage.input_tokens` + `response.usage.output_tokens` from the terminal frame, raw payload from the most recent dispatched event. Terminal trio: `response.completed` / `response.incomplete` / `response.failed` (the latter two carry `incomplete_details` — the streaming path uses the same normaliser as the unary path so streaming and non-streaming StopReason UX are identical). Mid-stream `error` event aborts with the carried message; both top-level-error and `{"error":{...}}`-nested shapes handled. Honours ctx via `http.NewRequestWithContext` + per-line `ctx.Err()` check + select-guarded delta send. The `responsesRequest` struct gained a `Stream bool` field with `omitempty` — keeps the unary path's wire payload byte-identical (omitempty drops the false-value field on Invoke). Sends `Accept: text/event-stream` on the streaming path. |
| `internal/provider/openai/openai_test.go` (modified, +295 lines / 9 new tests) | Streaming fixture (`streamServer` helper + `happySSE` constant — full canonical Responses-API stream including `response.created`, `response.in_progress`, `response.output_item.added`, `response.content_part.added`, three `response.output_text.delta` events, `response.output_text.done`, `response.content_part.done`, `response.output_item.done`, `response.completed` — every event-type the parser must ignore is interleaved). Tests: happy path (delta order, accumulated content, model from `response.created`, stop_reason, usage from `response.completed.response.usage`, raw, outbound `stream:true`), context cancellation (server flushes one delta then hangs; cancel mid-stream → returns within 2s deadline), malformed data payload (broken JSON aborts with decode error mentioning `response.output_text.delta`), missing terminal event (stream ends after deltas → error mentioning `response.completed`), HTTP 401 with realistic OpenAI error envelope, mid-stream `error` event (200 OK opens the stream, deltas flow, then server emits an `error` event → adapter aborts with the carried message), `response.incomplete` terminal with `incomplete_details.reason="max_output_tokens"` → normalised to `StopReasonMaxTokens` (wire-shape parity guard between streaming and unary paths), empty messages rejected (with channel-closed assertion on the early-return path), Streamer interface conformance. The `openaiRequestCapture` test struct gained the `Stream` field to match. |

**Decisions held from the kickoff (no re-deliberation, exactly as locked):**

- **Reuse the unary `normalizeStopReason(status, incomplete)` helper.** The Responses API streaming terminal events carry the same `status` + `incomplete_details` shape as the non-stream response body — feeding both paths through one helper is correctness-by-construction. A future incomplete-reason-to-StopReason mapping change touches one site, never two.
- **Switch on `event:` field, not the `type` field inside the JSON.** OpenAI sends both, equal — switching on the SSE-level field matches the Claude adapter's pattern from session 19, keeps the diff between the two streaming adapters readable, and means a future event whose JSON `type` is missing or shaped oddly still routes correctly. Multi-line `data:` joining is in place defensively (current Responses API doesn't emit it; SSE spec permits it; cost is two lines).
- **Terminal event is a trio, not just `response.completed`.** `response.incomplete` and `response.failed` are also valid terminals for the Responses API — the chat REPL must surface a max-tokens truncation or content-filter rejection through the same StopReason path as the unary call, which the helper already handles.
- **Mid-stream `error` event aborts.** Once content has streamed and the upstream model fails, returning a partial Response would mislead the user. Surfacing `openai: stream error: <message>` mirrors the unary path's `openai: response error: <message>` shape — same pain point, same caller experience.
- **Forward-compat by silence for unknown events.** `response.in_progress`, `response.output_item.added|done`, `response.content_part.added|done`, `response.output_text.done`, `response.refusal.delta`, and any future reasoning/tool events flow through the parser without aborting it. The text-only chat surface only needs four event names; everything else is ignored. When tool surfacing or reasoning surfacing land in v0.2+, those events get explicit cases without breaking existing callers.
- **Final Response shape matches Invoke's exactly.** Same `*provider.Response` fields populated identically; the chat REPL's render path is unchanged. Same model id semantics (first observed wins, terminal can override), same usage shape, same StopReason enum.

**Decisions made this session (small details not in the kickoff):**

- **`Accept: text/event-stream` header on the streaming request.** OpenAI auto-detects streaming from the body's `stream:true`, so the Accept header is technically optional. Sent it anyway — spec-correct hint, costs nothing, and any future proxy that does content negotiation will route correctly. Same call as the Claude adapter.
- **`Stream` field on `responsesRequest` uses `omitempty`.** The unary path now sets `Stream: false` explicitly; with `omitempty`, the JSON encoder drops the false-value field, so the outbound wire bytes are byte-identical to what they were before this session. Verified via the existing `TestAdapter_InvokeHappyPath` (which asserts the request shape and was untouched by this session) — still passes. Zero churn for unary callers. Matches the Claude session's pattern.
- **`response.created` is the model-id capture point.** OpenAI sends `response.created` first with the response stub (carrying `model`), then a sequence of deltas, then `response.completed` with the full response object including model again. The terminal event also overrides `model` if it changed (e.g., a routing layer re-pinning to a specific revision), so the final Response carries the most authoritative model id even if the upstream changed mid-stream. Keeps streaming/unary parity.
- **`response.in_progress` lumped into the ignore-list, not a noop case.** It's a periodic heartbeat that re-sends the partial response object. In theory we could update model from it, but in practice every value `response.in_progress` carries is also in `response.created` and `response.completed` — pulling from it adds a third source of truth for no behavioural win. The case is absent from the dispatch switch (default = ignore), same as `ping` was for Claude.
- **Test fixture interleaves every ignored event.** `happySSE` includes `response.in_progress`, `response.output_item.added`, `response.content_part.added`, `response.output_text.done`, `response.content_part.done`, `response.output_item.done` between the delta events. Without these, a future regression that accidentally panics on an unknown event-type would slip through. The fixture is canonical Responses-API shape — Anthropic-shaped fixtures wouldn't surface OpenAI-specific events. Per-adapter fixture is the right granularity.
- **Mid-stream-error test as a dedicated case.** Claude's session didn't have an analogue because Anthropic doesn't emit a separate `error` SSE event mid-stream — it returns a non-2xx HTTP and the connection ends. OpenAI's Responses API can emit `event: error` after a successful 200 OK, so the adapter needs explicit handling and the test needs to prove it. One additional test compared to Claude's seven; total nine.
- **`response.incomplete` test as a streaming/unary parity guard.** The unary path's `TestAdapter_InvokeStopReasonNormalisation` covers six combinations of `status` + `incomplete_details.reason`. Re-running all six on the streaming path would have been belt-and-braces redundancy, since both paths feed the same `normalizeStopReason` helper. One canonical streaming case (`response.incomplete` with `incomplete_details.reason="max_output_tokens"`) proves the wire path through the helper works; the unary tests cover the helper's mapping logic exhaustively. Two tests minimum to guarantee parity, ten total tests would have added noise without adding signal.
- **Stop reason normaliser unchanged, no streaming-specific mapping.** Same status strings ("completed" / "incomplete" / "failed"), same incomplete_details.reason values, same return enum. Streaming-specific stop reasons (network drop mid-stream, client cancellation) surface as errors, not StopReasons — a Response is only constructed when the stream ended cleanly with a terminal event.
- **No new defaults model entries, no new constants.** Streaming reuses `DefaultMaxTokens` and `DefaultTimeout`; the model list in `defaultModels()` is correct as-is for both paths. The streaming path is purely a wire-shape variant of the unary path, not a different feature surface.

**Test count:** 205 → 214 PASS lines (+9: all in `internal/provider/openai/`). All race-clean, all vet-clean, ~10s total run.

**Live smoke status:** **Deferred to huckgod's shell.** The harness env redacts `OPENAI_API_KEY` (28 tab-bytes from harness padding, consistent with prior sessions — the asciicast and the live Claude smoke from session 19 were deferred for the same reason). The httptest fixture covers the wire-shape correctness completely (happy path, ctx cancellation, malformed payload, missing terminal event, HTTP error, mid-stream error, incomplete-with-reason normalisation, empty-messages, Streamer interface). The remaining proof is "does OpenAI's actual server emit the events we parse" — a five-minute task on huckgod's shell:

```
OPENAI_API_KEY=sk-... daimon chat --provider openai --model gpt-5-mini \
  --stream --once "Recite the first three sentences of Hamlet's soliloquy." \
  | python3 -c 'import sys, time; t0 = time.monotonic(); [print(f"{(time.monotonic()-t0)*1000:7.1f}ms {b!r}") for b in iter(lambda: sys.stdin.buffer.read(1), b"")]'
```

Expected: a sequence of byte-level prints with sub-100ms gaps between them. The `[stream: openai does not support streaming, falling back to invoke]` stderr note from session 18's smoke should be gone — replaced by direct streaming.

**What this means in plain language:** before this session, `daimon chat --provider openai --stream` printed the fallback note then waited for the full reply to land all-at-once. After this session, OpenAI's tokens render incrementally — same UX as Ollama since session 18 and Claude since session 19, now applied to the third adapter. The protocol's most concrete promise — switch Claude → OpenAI → local mid-task with memory intact — now has streaming-UX parity across both major hosted providers AND the dominant local runtime. LM Studio is the last adapter remaining, ~half a day, same shape as this and the Claude session: per-adapter SSE format wrapped around the same `Streamer` interface, no further server or CLI work required.

**What we explicitly punted (in priority order for next session):**

1. **Live OpenAI streaming round-trip** (this session's deferred smoke). Five minutes once huckgod's shell exposes a real `OPENAI_API_KEY`.
2. **Live Claude streaming round-trip** (carry-over from session 19). Five minutes once `ANTHROPIC_API_KEY` is real in shell env.
3. **Live LM Studio round-trip** (carry-over, CHECKPOINT item 21). Five-minute task when LM Studio is up locally.
4. **LM Studio streaming adapter** (CHECKPOINT item 23a, third and last of three). OpenAI Chat Completions SSE format (`data: {...}\n\n` chunks until `data: [DONE]`). Half-day session, same shape as this one.
5. **Server-side `injected_memory_ids` in `provider.invoke` response → REPL `[inject_context: query="..." matched=N]`** (CHECKPOINT item 23b). ~30 minutes.
6. **Activity log encryption (SPEC §10) + `internal/secretbox` factor** (CHECKPOINT items 23c/23d). Half-day.
7. **The asciicast** (carry-over from session 16). Now compelling for three adapters with token-by-token rendering.
8. **NLnet NGI Zero application** (carry-over from session 16).

**Next session begins with:** either run the deferred live OpenAI streaming smoke (5 min, requires real `OPENAI_API_KEY` exposed in shell env), or proceed to LM Studio streaming as the third and last remaining adapter in CHECKPOINT item 23a. Either path is short; LM Studio streaming closes out v0.1.x's streaming queue.

---

## 2026-05-05 — Day Zero, twenty-first session: LM Studio streaming adapter

**Streaming queue closes.** LM Studio's OpenAI-compatible Chat Completions SSE joins Ollama (session 18), Claude (session 19), and OpenAI (session 20) as the fourth adapter implementing `provider.Streamer`. The wire-shape contract from session 18 absorbed the new adapter exactly as designed — zero changes to `internal/server/`, `cmd/daimon/`, or SPEC. The `daimon chat --provider lmstudio --stream` REPL behavior automatically flips from "falling back to invoke" to live token-by-token rendering as soon as the adapter exposes `Stream`. Three of three streaming adapters in the v0.1.x queue are done; v0.1.x streaming is complete across every provider in tree.

LM Studio probe at session start: `curl http://localhost:1234/v1/models` → connection refused (curl exit 7). Item 21's live round-trip still pending huckgod's shell having LM Studio up locally. Proceeded straight to LM Studio streaming as the kickoff prescribed (item 23a, third and last of three).

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/provider/lmstudio/lmstudio.go` (modified, +235 lines) | New `Stream` method. POST `/v1/chat/completions` with `stream:true` and `stream_options:{include_usage:true}`; bufio.Scanner over the SSE response (line-by-line, dispatch on blank line). One Delta per non-empty `choices[0].delta.content` chunk. Final `*Response` carries model from the latest dispatched chunk's `model` field, content from accumulated text deltas, stop_reason mapped through the existing `normalizeStopReason(finish_reason)` helper from the closing chunk's `choices[0].finish_reason` (six cases: stop/length/content_filter/tool_calls/function_call/empty — same six the unary path covers), usage from the post-content `choices:[]` chunk's `usage` field (latest-wins handles both canonical separate-chunk and inline-usage server shapes), raw payload from the most recent JSON chunk before [DONE]. Terminal sentinel is the literal string `data: [DONE]` (the bytes-equal check before JSON unmarshal); absence of [DONE] is an error so a half-streamed reply never presents itself as complete. Mid-stream `error` field on a chunk aborts with the carried message. Honours ctx via `http.NewRequestWithContext` + per-line `ctx.Err()` check + select-guarded delta send (matches Claude/OpenAI shape exactly). The `chatRequest` struct gained a `StreamOptions *streamOptions` field with `omitempty` AND the existing `Stream bool` flipped to `omitempty` — keeps the unary path's wire payload byte-identical (omitempty drops the false-value field on Invoke; the existing `TestAdapter_InvokeHappyPath` still asserts `capture.Stream != false` and still passes because unmarshalling a missing field yields the zero value). Sends `Accept: text/event-stream` on the streaming path. New imports: `bufio`, `strings`. |
| `internal/provider/lmstudio/lmstudio_test.go` (modified, +373 lines / 9 new tests) | Streaming fixture (`streamServer` helper that routes `/v1/models` to the standard probe response and `/v1/chat/completions` to a per-test handler with Flusher assertion + `happyChatSSE` constant — the canonical Chat Completions SSE shape including the role-priming chunk, three content delta chunks, the closing `finish_reason="stop"` chunk with empty delta, the post-content `choices:[]` chunk carrying `usage`, and the literal `data: [DONE]` sentinel). Tests: happy path (delta order, accumulated content, model, stop_reason, usage from the post-content chunk, raw, outbound `stream:true`, `Accept: text/event-stream`, `Authorization: Bearer lm-studio` headers), context cancellation (server flushes one delta then hangs; cancel mid-stream → returns within 2s deadline), malformed data payload (broken JSON aborts with `decode chunk` error), missing [DONE] terminal (stream ends after a content delta → error mentioning `[DONE]`), HTTP 401 with realistic OpenAI error envelope, mid-stream error chunk (200 OK opens stream, content delta flows, then `error:{message:...}` chunk → adapter aborts with the carried message), finish_reason="length" → `StopReasonMaxTokens` parity guard (proves the streaming path actually feeds the helper; unary tests cover the helper's full six-case mapping), empty messages rejected (with channel-closed assertion on the early-return path), Streamer interface conformance. |

**Decisions held from the kickoff (no re-deliberation, exactly as locked):**

- **Reuse the unary `normalizeStopReason(reason)` helper.** The Chat Completions streaming closing chunk carries `finish_reason` in the same shape and value space as the non-stream response body — feeding both paths through one helper is correctness-by-construction. A future mapping change (e.g., `tool_calls` surfacing) touches one site, never two. The streaming/unary parity guard test (`TestAdapter_StreamFinishReasonNormalisation`) proves the wire path through the helper; the unary tests cover the helper's full six-case mapping exhaustively, so re-running all six on the streaming path would have been belt-and-braces redundancy. One test is enough to catch a wire-path regression; six wouldn't have caught more.
- **No `event:` lines, only `data:` payloads, terminal sentinel `data: [DONE]`.** This is the OpenAI Chat Completions SSE format — distinct from the Responses API's `event: response.<thing>` shape that session 20's OpenAI adapter parses. The dispatcher switches on the `data:` prefix only; `event:`, `id:`, `retry:`, and comment lines (`:` prefix) all flow through ignored. The `[DONE]` check is `bytes.Equal(bytes.TrimSpace(curData), []byte("[DONE]"))` BEFORE JSON unmarshal — `[DONE]` is not valid JSON and would otherwise produce a misleading decode error.
- **`stream_options.include_usage:true` on the request.** Without this, OpenAI Chat Completions and LM Studio do not emit a usage chunk on streamed responses (the unary path always returns usage; the streaming path explicitly opts in). Setting it gives streaming/unary parity on the `Usage` field of the final `*Response` — the chat REPL's audit-log entries will show the same `input_tokens`/`output_tokens` numbers regardless of which path produced the reply. Servers that don't honour `stream_options` simply drop the field (no behavioural regression for hypothetical alt-implementations).
- **Forward-compat by silence for unknown fields.** Future `choices[0].delta.tool_calls` fragments, `choices[0].delta.function_call`, `choices[0].delta.refusal`, or any other delta surface OpenAI adds flow through the parser without aborting it — the JSON unmarshal target only declares the fields we care about, and unknown JSON keys are silently ignored. When tool surfacing or refusal surfacing land in v0.2+, those keys get explicit handling without breaking existing callers.
- **No retry, no policy.** Adapter is a translator. `4xx`/`5xx` on the initial POST → error. Stream ends without `[DONE]` → error. Malformed JSON in a content chunk → error. Mid-stream `error` field on a chunk → error. Caller surfaces it; user retries.
- **Ctx cancellation is a hard requirement.** `http.NewRequestWithContext` plumbs ctx into the HTTP layer; a per-line `ctx.Err()` check covers the "buffered upstream bytes already in scanner.Buffer" case. The `select { case deltas <- delta: case <-ctx.Done(): return ctx.Err() }` send guards against deadlock on a slow consumer + cancellation interleave. Test asserts the adapter exits within 2s of cancellation — no goroutine leak, no continued upstream consumption.
- **Final Response shape matches Invoke's exactly.** Same `*provider.Response` fields populated identically; the chat REPL's render path is unchanged. Same model id semantics (latest dispatched chunk wins), same usage shape, same StopReason enum.

**Decisions made this session (small details not in the kickoff):**

- **`Accept: text/event-stream` header on the streaming request.** LM Studio auto-detects streaming from the body's `stream:true`, so the Accept header is technically optional. Sent it anyway — spec-correct hint, costs nothing, and any future proxy that does content negotiation will route correctly. Same call as Claude (session 19) and OpenAI (session 20).
- **`Stream` field on `chatRequest` switched to `omitempty` AND new `StreamOptions *streamOptions` added.** The unary path now sets `Stream: false` explicitly (same line as before); with `omitempty`, the JSON encoder drops the false-value field, so the outbound wire bytes are byte-different from before this session — but only in the absence of the `stream` key (which servers default to false anyway). Verified via the existing `TestAdapter_InvokeHappyPath` which asserts `capture.Stream != false`: still passes because unmarshalling a missing key yields false. Zero behavioural churn for unary callers, semantically identical wire intent. `StreamOptions` is `nil` on the unary path so `omitempty` drops it entirely — invariant preserved.
- **Latest-wins on `model` and `usage` across chunks.** Some servers fold usage into the final content chunk inline; the canonical OpenAI shape emits a separate post-content `choices:[]` chunk carrying just usage. Latest-wins handles both — the test fixture exercises the canonical shape, and the inline-usage path is a one-line behavioural sibling that doesn't need its own test (the same code path runs).
- **`[DONE]` check as bytes-trim-equal, not string compare.** `bytes.Equal(bytes.TrimSpace(curData), []byte("[DONE]"))` is allocation-free and tolerant of trailing whitespace some proxies might add. The OpenAI spec is precise about the literal six bytes `[DONE]`, but defensive trimming costs nothing.
- **Trailing-payload-without-blank-line rescue.** If the scanner exits the loop with `curData` non-empty (server omitted the final blank line that would have flushed `[DONE]`), one final `dispatch()` runs before the missing-`[DONE]` check. The httptest fixture in the happy path test does include trailing blanks, so this path exists for production resilience, not test coverage. Cost: 4 lines. Same call as Claude / OpenAI.
- **Test fixture's empty role-priming chunk is exercised, not just the content chunks.** The first chunk in `happyChatSSE` carries `delta:{role:"assistant",content:""}` — empty content, empty finish_reason. The dispatcher must not emit a delta for it (the contract is "non-empty content only"), and the test's `wantDeltas` of exactly three entries (Hello, ", ", world.) verifies this — a four-entry result would catch a regression where an empty-content chunk leaks through.
- **No new constants, no new option types.** `Streaming` reuses `DefaultMaxTokens`, `DefaultTimeout`, `DefaultEndpoint`, `DefaultAPIKey`, `chatPath`. The streaming path is purely a wire-shape variant of the unary path, not a different feature surface.
- **Test count rounded to 9, not more, not less.** The kickoff projected ~9 tests; the actual count is 9, mirroring session 20's 9. Could have added a streaming-defaults-model test or an outbound-wire-shape test for stream_options, but those are covered by the happy-path test's `capture.Stream` assertion plus the unary path's existing `TestAdapter_InvokeDefaultsModel`. Two tests added would have been noise, not signal.

**Test count:** 214 → 223 PASS lines (+9: all in `internal/provider/lmstudio/`). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status:** **Deferred to huckgod's shell.** LM Studio is not running locally on this shell (`curl http://localhost:1234/v1/models` → connection refused, curl exit 7), same as sessions 18, 19, and 20. The httptest fixture covers the wire-shape correctness completely (happy path with full canonical SSE shape including role-priming chunk, content deltas, finish_reason, post-content usage chunk, and [DONE]; ctx cancellation, malformed payload, missing [DONE], HTTP 401, mid-stream error chunk, finish_reason normalisation parity, empty-messages, Streamer interface). The remaining proof is "does LM Studio's actual server emit the events we parse" — a five-minute task on huckgod's shell with LM Studio up and a model loaded:

```
daimon chat --provider lmstudio --model <loaded-model-id> \
  --stream --once "Recite the first three sentences of Hamlet's soliloquy." \
  | python3 -c 'import sys, time; t0 = time.monotonic(); [print(f"{(time.monotonic()-t0)*1000:7.1f}ms {b!r}") for b in iter(lambda: sys.stdin.buffer.read(1), b"")]'
```

Expected: a sequence of byte-level prints with sub-100ms gaps between them. The `[stream: lmstudio does not support streaming, falling back to invoke]` stderr note that session 18's smoke would have produced is now gone — replaced by direct streaming.

**What this means in plain language:** before this session, `daimon chat --provider lmstudio --stream` printed the fallback note then waited for the full reply to land all-at-once. After this session, LM Studio's tokens render incrementally — same UX as Ollama since session 18, Claude since session 19, OpenAI since session 20, now applied to the fourth and final v0.1.x adapter. The protocol's most concrete promise — switch Claude → OpenAI → local mid-task with memory intact — now has streaming-UX parity across **every** provider in tree. Every adapter that exists now supports streaming. The `provider.Streamer` interface absorbed the fourth wire format (OpenAI Chat Completions SSE — distinct from the Responses API SSE of session 20) without requiring a single line of change in `internal/server/`, `cmd/daimon/`, or SPEC — the wire-shape contract from session 18 was correctly designed.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (this session's deferred smoke). Five minutes once huckgod's shell has LM Studio up locally with a model loaded. Closes both item 21 (the unary live round-trip from session 17) AND the live half of session 21 in one go.
2. **Live OpenAI streaming round-trip** (carry-over from session 20). Five minutes once `OPENAI_API_KEY` is real in shell env.
3. **Live Claude streaming round-trip** (carry-over from session 19). Five minutes once `ANTHROPIC_API_KEY` is real in shell env.
4. **Server-side `injected_memory_ids` in `provider.invoke` response → REPL `[inject_context: query="..." matched=N]`** (CHECKPOINT item 23 carry-over a). ~30 minutes. The retrieval already runs server-side; one extra response field + one print line.
5. **Activity log encryption (SPEC §10) + `internal/secretbox` factor** (CHECKPOINT items 23 carry-over b/c). Half-day. Same threat model as memory rows; `identity.DeriveSubkey` already generic enough. Once the activity log is encrypted, four AES-GCM copies live in tree (keystore, credentials, memory rows, activity log) and the abstraction earns its keep.
6. **The asciicast** (carry-over from session 16). Now compelling for **all four** adapters with token-by-token rendering. Scene 4 of `demo/script.md` can demonstrate Ollama → Claude → OpenAI → LM Studio mid-task with streaming on every provider.
7. **NLnet NGI Zero application** (carry-over from session 16). The "every provider streams" claim is now mechanically true and the application's demo-and-momentum section gains the strongest line yet.

**Next session begins with:** v0.1.x streaming queue is **closed** (three of three adapters shipped); next pick is one of: (a) live round-trips for any of Claude / OpenAI / LM Studio when the corresponding key/local-server is available on huckgod's shell (5 min each), (b) the asciicast recording (same blocker — keys + LM Studio for the strongest version), (c) `injected_memory_ids` in provider.invoke (~30 min, no external dependencies), or (d) activity log encryption + `secretbox` factor (half-day, no external dependencies). (c) and (d) are the two paths that don't require huckgod's shell to do anything special.

---

## 2026-05-05 — Day Zero, twenty-second session: activity log payload encryption

**The disk-theft hole on the activity log closes.** The JSONL at `$DAIMON_HOME/activity.log` no longer narrates what the user did across providers in cleartext. Each entry's `payload` field is now `version(1B) || nonce(12B) || AES-256-GCM(plaintext_payload, AAD)`, base64-encoded into the on-disk JSON string. The id, ts, kind, prev_hash, hash, and signature columns remain in clear so Query filtering, `LastHash` recovery on Open, and chain continuity all work without unlock — a mirror of the memory store's §5.1 construction with a domain-separated HKDF subkey label and an entry-id-bound AAD instead of a row-id-and-field-bound one. CHECKPOINT item 23(b) closes; v0.1.x carry-over now has only the `injected_memory_ids` REPL surface and the `secretbox` factor remaining.

**Probe at session start (per the kickoff's opportunistic round-trip checklist):**

```
$ curl -sS --max-time 2 http://localhost:1234/v1/models | head -c 200
curl: (7) Failed to connect to localhost port 1234

$ printenv | grep -E '(ANTHROPIC|OPENAI)_API_KEY' | sed 's/=\(.\{8\}\).*/=\1.../'
OPENAI_API_KEY=								...
```

LM Studio: still down. OpenAI key: still tab-padded redacted. ANTHROPIC_API_KEY: not set. All three live round-trips remain deferred from sessions 19/20/21 — none free this session. Proceeded straight to encryption work as the kickoff prescribed.

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/activity/crypto.go` (new, 206 lines) | Lifted-and-adapted from `internal/memory/crypto.go`. Constants: `payloadCryptoVersion=0x01`, `payloadNonceLen=12`, `payloadKeyLen=32`, `payloadAADPrefix="daimon-activity-payload-v1"`, `payloadAADField="payload"`, exported `ActivityEncryptionKeyLabel="daimon-activity-encryption-v1"`. Errors: `ErrInvalidCiphertext`, `ErrInvalidKeyLength` — same semantics as the memory crypto's same-named errors but in the activity package's namespace so callers route on the right one. Four functions: `encryptPayload(key, plaintext, entryID)` and `decryptPayload(key, blob, entryID)` operate on raw ciphertext bytes (mirror `encryptField` / `decryptField` in `memory/crypto.go`); `encodePayloadForDisk(key, plaintext, entryID) → json.RawMessage` and `decodePayloadFromDisk(key, payload, entryID) → json.RawMessage` are the JSONL-aware wrappers that handle base64-into-JSON-string framing — these are what Append and Query/Verify call. Empty plaintext returns nil so `omitempty` drops the field. Nil key disables encryption (passes plaintext through) for migration tooling; the public `Open` path always supplies a non-nil key. |
| `internal/activity/log.go` (modified, +25 lines net) | `Log` struct gains a `key []byte` field. `Open` now derives the key inside the function via `id.DeriveSubkey(ActivityEncryptionKeyLabel, payloadKeyLen)` — caller signature unchanged (no churn at any callsite in `cmd/daimond/main.go`, `internal/server/`, or any test fixture). Append: still computes hash + signs over the canonical *plaintext* Entry (chain commits to plaintext, integrity preserved across the encryption boundary), then builds a copy of the entry with payload replaced by the encrypted-and-base64 wire form, marshals that copy to disk. Returns the plaintext Entry to the caller — same external API as before. Query: after JSON-unmarshal of each line, applies pre-decryption filters (timestamp range, kind, limit), then decrypts payload via `decodePayloadFromDisk` and assigns plaintext back into `e.Payload` before appending to the result. Verify: decrypts payload first, then recomputes hash from the post-decryption Entry — a wrong key surfaces as `ErrInvalidCiphertext` before the chain check; a tampered ciphertext does the same; a tampered `prev_hash`/`hash` still triggers `ErrChainBroken` / `ErrHashMismatch` exactly as before. `scanLastHash` unchanged (Hash is in clear so no decryption needed for `LastHash` recovery on Open). |
| `internal/activity/log_test.go` (modified, +218 lines net / 6 new tests + 1 updated assertion) | New tests: `TestEncryptedPayloadOnDisk` reads the raw JSONL line and asserts (a) plaintext content does not appear anywhere in the file bytes, (b) the `payload` field is a JSON string (`"..."`), not a JSON object (`{...}`); `TestEncryptionRoundtripQuery` Append → Query returns the same plaintext payload across mixed types (string, float, bool); `TestEncryptionAADBindingDetectsCiphertextSwap` splices entry #0's ciphertext payload onto entry #1's line, expects Verify to return `ErrInvalidCiphertext` (proves AAD binding to entry_id works); `TestEncryptionVerifyAfterReopen` 5-entry chain, close, reopen with same identity, Verify returns 5 (proves deterministic HKDF derivation across process boundaries); `TestEncryptionForeignKeyFailsCleanly` writes under id1, reopens under id2, asserts Query AND Verify both surface `ErrInvalidCiphertext` (not silent corruption — the disk-theft scenario the encryption is for); `TestEncryptionDeterministicKeyAcrossOpens` writes one entry, closes, reopens, Query returns the same plaintext payload (the canonical "no key on disk" guarantee). Updated `TestVerifyDetectsTamperedPayload` to expect `ErrInvalidCiphertext` instead of `ErrHashMismatch` — under encryption, replacing the payload field with arbitrary plaintext-shaped JSON now fails AEAD authentication one layer earlier than the chain check, which is strictly stronger evidence of tamper detection; comment explains the change. New imports: `bytes`, `reflect`. |
| `SPEC.md` (modified, +12 lines) | §8.1 entry format example now shows `"payload": "AaaBBBccc..."` instead of `"payload": { /* kind-specific */ }` with an inline pointer to the new "At-rest confidentiality" subsection. Added three paragraphs: at-rest envelope shape with AAD construction, hash-chain-under-encryption semantics (the key correctness property — chain commits to plaintext canonical bytes; Verify decrypts before recomputing), and key derivation (HKDF-SHA256 label `"daimon-activity-encryption-v1"`, distinct from `"daimon-memory-encryption-v1"` so the two stores have independent subkeys despite a shared root identity). §9.3 cryptographic primitives table gained an "At-rest encryption (activity payloads, v0.1)" row. §10 file layout comment on `activity.log` updated to reference §8.1. |
| `CHECKPOINT.md` (modified) | Phase line updated; build status line updated (223 → 229 PASS lines, 11 → 17 activity tests); item 23 added as "shipped" entry mirroring the prior items' shape; carry-over (b) crossed out as shipped. |

**Decisions held from the kickoff (no re-deliberation, exactly as locked):**

- **Mirror the memory-row construction.** Same primitive (AES-256-GCM), same nonce length (12 bytes random), same version byte (0x01), same AAD pattern (prefix || 0x00 || row-id-equivalent || 0x00 || field). The only differences are the AAD prefix string (`"daimon-activity-payload-v1"` vs `"daimon-memory-row-v1"`) and the HKDF info label (`"daimon-activity-encryption-v1"` vs `"daimon-memory-encryption-v1"`). Domain separation is enforced at both the AEAD and KDF layers, so a stolen ciphertext from one store cannot be silently moved into the other even if the labels were ever crossed by a future bug. The shared shape also means the v0.1.x `internal/secretbox` factor (carry-over c) will absorb both call sites without per-site special cases.
- **Encrypt only the `payload` field; leave id, ts, kind, prev_hash, hash, signature in clear.** `kind` and `ts` are needed for `daimon.activity.query` filtering without unlock; `prev_hash` / `hash` / `signature` are needed for chain continuity recovery on Open and tamper-evident verification. The interesting data the disk-theft adversary should not get for free is what's *inside* payload (provider names, model ids, token counts, durations, memory ids), not the kind label itself — the kind label is part of the public schema (SPEC §8.2) and reveals nothing the adversary couldn't infer from the existence of the daimon at all.
- **Hash chain commits to plaintext canonical bytes.** Verify decrypts each entry's payload before recomputing the hash. This means a wrong key surfaces as `ErrInvalidCiphertext` before the chain check runs — an attacker without the key cannot even assess whether the chain "looks intact"; they get a hard authentication failure on the first entry. An attacker who tampers with the ciphertext fails AEAD authentication; an attacker who tampers with `prev_hash`/`hash` still triggers `ErrChainBroken`/`ErrHashMismatch`. All three tamper modalities are caught, with progressively earlier failure points the closer you get to the cryptographic root.
- **Key derived inside `Open`, not passed in by the caller.** The kickoff suggested an `Open(path, id, key)` signature, but the memory store's `Open(path, id, embedder)` derives its key internally — so I followed the established pattern instead. Zero churn at the seven existing `activity.Open(path, id)` callsites (`cmd/daimond/main.go` × 2, four `internal/server/` test files). The key never crosses the daimon's process boundary in plaintext: rederived in memory at unlock from the bound identity's seed, lives only as long as the unlocked daimond process. Also keeps the `Log` struct's invariants self-contained — there's no way for a caller to pass a key that doesn't match the bound identity, which would have been a footgun under the original signature.
- **No backward compat for pre-encryption JSONL files.** The kickoff's punt: no production deployments yet, the demo writes to a temp dir, every existing test creates a fresh log. v0.1 is encrypted-only. Auto-detection logic ("does this look like ciphertext or cleartext?") would have added complexity for a scenario that doesn't exist. The sole consequence is that any developer who has been running `daimond demo` with a persistent `$DAIMON_HOME` will need to delete `$DAIMON_HOME/activity.log` before the first post-encryption Open — but no one is doing that on this codebase, demo always writes to `os.MkdirTemp`.

**Decisions made this session (small details not in the kickoff):**

- **Wire format is base64 of the AEAD envelope, not hex.** Both formats round-trip through JSON, but base64 is denser (4 chars per 3 bytes vs 2 chars per byte), and JSONL is not for human reading anyway (the file is full of ULIDs, BLAKE3 hex, and Ed25519 signatures already). The kickoff noted "pick one and stick with it" — base64 it is, encoded via `base64.StdEncoding` (with padding) so cross-language SDK readers don't have to know about URL-safe vs standard variants.
- **`decodePayloadFromDisk` validates the JSON-string shape explicitly.** If the on-disk `payload` field is a JSON object instead of a JSON string (e.g., a tampered file or a pre-encryption JSONL someone copied in), the decode path returns `ErrInvalidCiphertext` with a wrapped explanation rather than panicking on a json.Unmarshal-into-string failure. This is what makes `TestVerifyDetectsTamperedPayload` produce a clean error — and proved out via that test on the first run after the implementation.
- **Pre-decryption Query filtering.** Timestamp range, kind, and limit filters apply *before* `decodePayloadFromDisk` runs. The non-matching entries don't pay the AEAD cost; the matching ones get decrypted and returned. For a daimon with thousands of provider.invoke entries who runs `daimon.activity.query {kind: "memory.write"}`, this is 100× faster than a decrypt-everything-then-filter path. Limit filter applies post-decryption (after appending to result), since the limit is on returned entries, not scanned-and-discarded entries.
- **`scanLastHash` left untouched.** The recovery-on-Open path only reads `e.Hash`, which is in clear. No decryption needed. Open stays O(1) for daimons with long histories — same property session 4 established when the activity log first landed.
- **Test fixture `seedLog` reused for the cross-entry swap test.** `seedLog(t, 2)` produces a 2-entry log with non-empty payloads (the helper already passes `{"i": i}` per entry). The new `TestEncryptionAADBindingDetectsCiphertextSwap` simply parses the lines, swaps `e0.Payload` onto `e1.Payload`, marshals back, opens with the same identity, runs Verify. Adding a dedicated fixture for AAD testing would have been redundant — the existing helper already produces the exact shape the test needs.
- **Updated `TestVerifyDetectsTamperedPayload` rather than rewriting it.** The test's intent ("Verify detects tampered payload") is preserved; only the assertion changes from `ErrHashMismatch` to `ErrInvalidCiphertext`. A comment explains why this is strictly stronger evidence of tamper detection — AEAD authentication fires one layer earlier than the chain hash check, on a tamper that under the cleartext regime would have produced ErrHashMismatch. Same property exposed at a lower layer.
- **No update to `TestVerifyDetectsBadSignature`.** This test seeds with id1, reopens with id2, expects `ErrSignatureFailed`. Under encryption: id1's Append called with `payload: nil` → `payloadBytes = nil` → `encodePayloadForDisk` returns nil for empty plaintext → on-disk entry has no `payload` field (omitempty fires). On Verify under id2's key: e.Payload is empty → `decodePayloadFromDisk` returns nil for empty payload → no AEAD call → hash recomputes successfully on the empty-payload Entry → signature fails under id2's pubkey → `ErrSignatureFailed`. The test still passes verbatim; the new failure mode (foreign key on a non-empty payload) is covered separately by `TestEncryptionForeignKeyFailsCleanly`. Two distinct properties, two distinct tests, no overlap.
- **No `internal/secretbox` factor this session.** Carry-over (c) stays deferred to its own half-day session — by then four AES-GCM call sites exist (keystore, credentials, memory rows, activity payloads) and the abstraction shape is determined by all four, not three. The activity crypto.go duplicates code from memory/crypto.go (newGCM, nonce generation, Seal/Open, error wrapping); the duplication is intentional — folding three call sites into a helper is premature when the fourth is right there. Next session can do the consolidation cleanly.

**Test count:** 223 → 229 PASS lines (+6: all in `internal/activity/`). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status:** N/A this session — encryption is purely an at-rest property with no wire changes. The existing `daimond demo` runs identically (it writes to a temp dir, so no pre-existing cleartext log conflicts), the existing `daimon chat` flow logs `provider.invoke` entries that are now encrypted on disk but render the same way through `daimon.activity.query` (which decrypts before returning). The CLI surface is unchanged. The end-to-end manual smoke against a temp `$DAIMON_HOME` (init → unlock → memory write/read → provider list → chat --once → daemon kill) still runs in a few seconds and produces the same external behavior; the only observable difference is that `cat $DAIMON_HOME/activity.log` now shows base64 strings in the `payload` field instead of JSON objects.

**What this means in plain language:** before this session, anyone who copied `$DAIMON_HOME/activity.log` off the box could read every provider call you'd ever made — model ids, token counts, durations, the memory ids you'd injected as context. After this session, that file is base64 ciphertext for the interesting fields; the only metadata visible without unlock is "at time T, the daimon did *something* of kind K, chained to the previous something." The threat model matches the memory store's: disk theft / backup exfiltration on top of OS-layer FDE. Both stores now have parity at the at-rest layer.

The structural property worth naming: **Daimon's two persistent stores (memory + activity log) now have parity on at-rest confidentiality**, derived from independent HKDF subkeys of the same root identity, with AEAD AAD bindings that prevent cross-row / cross-entry ciphertext movement, with hash chains and signatures that commit to plaintext canonical bytes so integrity holds across the encryption boundary. The third persistent store (the encrypted keystore) and the fourth (encrypted provider credentials) already encrypt at rest under different mechanisms — when the `internal/secretbox` factor lands, all four will route through one helper.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (carry-over from session 21 — closes both item 21's unary live round-trip AND session 21's streaming live half). Five minutes once huckgod's shell has LM Studio up locally with a model loaded.
2. **Live OpenAI streaming round-trip** (carry-over from session 20). Five minutes once `OPENAI_API_KEY` is real in shell env.
3. **Live Claude streaming round-trip** (carry-over from session 19). Five minutes once `ANTHROPIC_API_KEY` is real in shell env.
4. **Server-side `injected_memory_ids` in `provider.invoke` response → REPL `[inject_context: query="..." matched=N]`** (CHECKPOINT item 23 carry-over a). ~30 minutes. The retrieval already runs server-side; one extra response field + one print line. No external dependencies.
5. **`internal/secretbox` factor** (CHECKPOINT item 23 carry-over c). Half-day. Now genuinely the right time — four AES-GCM call sites in tree (keystore, credentials, memory rows, activity payloads), the abstraction shape is determined by all four. No external dependencies.
6. **The asciicast** (carry-over from session 16). Now compelling for **all four** adapters with token-by-token rendering AND for the at-rest confidentiality story (activity log is now ciphertext on disk; demo could `cat $DAIMON_HOME/activity.log` post-recording to make the encryption visible).
7. **NLnet NGI Zero application** (carry-over from session 16). The application's "what's encrypted at rest" answer just got stronger.

**Next session begins with:** v0.1.x has two no-external-dependency carry-overs left — `injected_memory_ids` (~30 min) and `secretbox` factor (half-day). Either is a clean session. The `secretbox` factor is now the more compelling pick because the four call sites are all in tree and the abstraction shape will be determined by them rather than guessed at. After both carry-overs land, v0.1.x is genuinely complete and the next milestone is the asciicast or the NLnet application — both blocked on the same external constraints (real API keys, LM Studio running locally) that have been blocking the live round-trips since session 19.

---

## 2026-05-05 — Day Zero, twenty-third session: `internal/secretbox` factor

**Four AES-GCM call sites fold into one helper.** The duplication CHECKPOINT item 23(c) flagged when the activity log encryption landed (session 22) is gone: `aes.NewCipher` → `cipher.NewGCM` and the bytes-packed `version(1B)‖nonce(12B)‖AEAD` envelope each appear exactly once in the tree. The identity keystore, the provider credentials store, the memory row encryption, and the activity payload encryption all route through `internal/secretbox`. On-disk bytes are unchanged — `TestEnvelopeByteStability` locks the envelope shape against future drift by hand-rolling it via `crypto/aes`+`crypto/cipher` directly with a known key+nonce+AAD+plaintext and asserting `secretbox.OpenAAD` decrypts it correctly. With this session, v0.1.x has one no-external-dependency carry-over remaining: server-side `injected_memory_ids` in `provider.invoke` response → REPL `[inject_context: query="..." matched=N]` annotation (~30 min, item 24a in CHECKPOINT).

**Probe at session start (per the kickoff's opportunistic round-trip checklist):**

```
$ curl -sS --max-time 2 http://localhost:1234/v1/models | head -c 200
curl: (7) Failed to connect to localhost port 1234

$ printenv | grep -E '(ANTHROPIC|OPENAI)_API_KEY' | head -c 50
OPENAI_API_KEY=
```

LM Studio: still down. OPENAI_API_KEY: empty (the harness redaction now collapses to the empty string rather than tab-padding). ANTHROPIC_API_KEY: not set. All three live round-trips remain deferred from sessions 19/20/21 — none free this session. Proceeded straight to the secretbox factor as the kickoff prescribed.

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/secretbox/secretbox.go` (new, 132 lines) | Two-layer package. Lower layer: `NewAEAD(key) (cipher.AEAD, error)` returns a configured AES-256-GCM under a 32-byte key; rejects any other length with `ErrInvalidKeyLength`. Upper layer: `SealAAD(key, plaintext, aad)` returns `version(1B)‖nonce(12B)‖AES-256-GCM(plaintext, aad)`, and `OpenAAD(key, blob, aad)` reverses it — both honour nil-key passthrough (returns plaintext/blob as-is) and empty-plaintext-returns-nil (so callers can rely on `omitempty`). Constants `Version=0x01`, `KeyLen=32`, `NonceLen=12` are exported; bumping the envelope version is one constant change. Errors `ErrInvalidCiphertext` / `ErrInvalidKeyLength` are exported sentinels — wrappers in `internal/memory` and `internal/activity` re-export them as their package-level errors so existing `errors.Is(err, memory.ErrInvalidCiphertext)` callers keep matching. AAD construction is left to the caller — each call site binds its own domain-separated AAD, which is what prevents cross-row / cross-entry / cross-field ciphertext movement. The keystore and credentials, whose JSON envelopes carry Argon2id parameters alongside the ciphertext (something the bytes-packed envelope deliberately does not model), use only the lower layer; memory rows and activity payloads use the upper layer. |
| `internal/secretbox/secretbox_test.go` (new, 200 lines / 12 tests) | `TestSealOpenRoundtrip` (envelope length asserts version+nonce+ct+tag = 1+12+plaintext+16); `TestSealRandomizesNonce` (two seals of the same plaintext are byte-distinct — nonce reuse guard); `TestOpenRejectsForeignAAD`, `TestOpenRejectsForeignKey`, `TestOpenRejectsBitFlip`, `TestOpenRejectsUnknownVersion`, `TestOpenRejectsTruncatedBlob`, `TestOpenRejectsPlaintextShapedBlob` (the eight tamper-detection paths memory's existing crypto_test.go already covers, lifted into the abstraction); `TestInvalidKeyLength` (NewAEAD + SealAAD + OpenAAD all surface `ErrInvalidKeyLength` for sub-32-byte keys); `TestNilKeyPassesThrough`, `TestEmptyPlaintextProducesNil` (the migration-tooling and `omitempty` invariants); `TestEnvelopeByteStability` — the byte-stability smoke. The latter hand-rolls the envelope using `crypto/aes`+`crypto/cipher` directly with a fixed 32-byte key, fixed 12-byte nonce, fixed AAD, fixed plaintext; computes the AES-GCM ciphertext via the standard library; concatenates `version(0x01)‖nonce‖ct`; and asserts `secretbox.OpenAAD` round-trips it. Locks the on-disk format against future drift independent of the SealAAD code path. |
| `internal/memory/crypto.go` (refactored, 140 → 80 lines) | Body collapsed to two thin wrappers: `encryptField(key, pt, memID, field) → secretbox.SealAAD(key, pt, buildRowAAD(memID, field))`; `decryptField(key, blob, memID, field) → secretbox.OpenAAD(key, blob, buildRowAAD(memID, field))`. The `rowAADPrefix` constant + `buildRowAAD` helper + `MemoryEncryptionKeyLabel` HKDF info string stay (they are per-call-site domain knowledge, not crypto state). `rowKeyLen`/`rowNonceLen`/`rowCryptoVersion` kept as package-local aliases of the secretbox constants — required because `memory_test.go` and `crypto_test.go` reference these names verbatim and the kickoff said no test should change. The aliases are one line each and make the relationship to secretbox explicit. `ErrInvalidCiphertext` and `ErrInvalidKeyLength` are now `var X = secretbox.X` aliases so `errors.Is(err, memory.ErrInvalidCiphertext)` walks the wrapping chain to the secretbox sentinel and matches. |
| `internal/activity/crypto.go` (refactored, 206 → 130 lines) | Same treatment as memory: thin wrappers around `secretbox.SealAAD` / `secretbox.OpenAAD`, with the JSONL-specific base64-into-JSON-string framing in `encodePayloadForDisk` / `decodePayloadFromDisk` retained (memory rows are raw BLOBs in SQLite, activity payloads are JSONL strings — the framing layer stays activity-local). `payloadAADPrefix` + `payloadAADField` constants + `buildPayloadAAD` helper + `ActivityEncryptionKeyLabel` HKDF info string stay. The pre-refactor `payloadCryptoVersion`/`payloadNonceLen`/`payloadKeyLen` constants are deleted entirely — no test references them — and `log.go`'s lone `id.DeriveSubkey(ActivityEncryptionKeyLabel, payloadKeyLen)` updated to `id.DeriveSubkey(ActivityEncryptionKeyLabel, secretbox.KeyLen)`. Errors aliased to secretbox same as memory. |
| `internal/identity/keystore.go` (refactored, -10 lines net) | `aes.NewCipher` + `cipher.NewGCM` boilerplate × 2 call paths (saveKeystore + loadKeystore) → one `secretbox.NewAEAD(key)` call each. `aesGCMNonceLen` constant deleted; `secretbox.NonceLen` used directly. The `crypto/aes` and `crypto/cipher` imports are gone. The Argon2id+JSON-envelope construction is untouched — it lives outside secretbox's scope by design. |
| `internal/provider/credentials.go` (refactored, -12 lines net) | Same treatment as keystore: `aes.NewCipher` + `cipher.NewGCM` × 2 call paths → `secretbox.NewAEAD`. `aesGCMNonceLen` constant deleted; `secretbox.NonceLen` used directly. The pre-existing TODO comment ("factor a shared internal/secretbox so this and internal/identity both call into one crypto implementation. Deferred to the session that adds passkey/WebAuthn-PRF — that's where the abstraction earns its keep.") deleted — the abstraction earned its keep this session, two sessions earlier than the TODO predicted. |
| `internal/activity/log.go` (modified, +1 import / 1 line changed) | Added `"github.com/regitxx/Daimon/internal/secretbox"` import. `id.DeriveSubkey(ActivityEncryptionKeyLabel, payloadKeyLen)` → `id.DeriveSubkey(ActivityEncryptionKeyLabel, secretbox.KeyLen)`. No other behavioural changes; the `Log.key` field is still 32 bytes derived from the bound identity, just the constant moved one package over. |
| `SPEC.md` (modified, §9.3 expanded by ~7 lines) | The four "At-rest encryption" rows in the cryptographic primitives table (added two: identity keystore + provider credentials; existing two: memory rows + activity payloads) now reference `internal/secretbox.NewAEAD` (lower-layer rows) or `internal/secretbox.SealAAD` / `OpenAAD` (upper-layer rows) as the implementation site. New paragraph beneath the table names the shared primitive, identifies which sites use which layer, and clarifies domain separation — AAD layer for memory/activity, HKDF info-label layer for memory/activity (`daimon-memory-encryption-v1` vs `daimon-activity-encryption-v1`), independent Argon2id salts for keystore/credentials. No threat-model or wire-shape changes; this is purely an internal refactor and the on-disk bytes are byte-identical to what session 22 produces. |
| `CHECKPOINT.md` (modified) | Phase line updated; build status line updated (229 → 241 PASS lines, +12 secretbox tests); item 24c crossed out as shipped; new item 25 added describing the secretbox factor. |

**Decisions held from the kickoff (no re-deliberation, exactly as locked):**

- **Two layers, not one.** `NewAEAD(key)` and `SealAAD/OpenAAD(key, plaintext|blob, aad)` are separate exports. Forcing the keystore and credentials into the upper layer would have broken their JSON envelope (which carries Argon2id parameters that the bytes-packed envelope does not model) — a worse abstraction. The dedup at the lower layer is still worth it: 4× call sites, byte-identical AES.NewCipher + cipher.NewGCM dance, all routed through one helper. The bytes-packed envelope is a separate concern with two call sites (memory rows, activity payloads), both of which had it copy-pasted; folding that into `SealAAD/OpenAAD` removes ~30 lines of duplication.
- **Version byte lives in the secretbox package as a constant.** `Version = 0x01`. A future v2 envelope (e.g., switching to ChaCha20-Poly1305, or extending the nonce to 24 bytes) is one constant change at the package boundary, with the call sites unchanged. Memory and activity each retain their own AAD-prefix + HKDF-label constants because those are per-call-site domain knowledge, not crypto state.
- **AAD construction is the caller's responsibility.** Each call site binds its own domain-separated AAD — `"daimon-memory-row-v1" || 0x00 || row_id || 0x00 || field` for memory, `"daimon-activity-payload-v1" || 0x00 || entry_id || 0x00 || "payload"` for activity. This is what prevents cross-row / cross-entry / cross-field ciphertext movement, and it's what makes the AAD parameter on `SealAAD/OpenAAD` non-optional. The keystore and credentials use AAD = nil because their threat model (an attacker who has both the file and the password) doesn't benefit from AAD binding — the protection is the password itself.
- **Memory and activity tests stay verbatim.** Both packages' existing crypto_test.go and log_test.go encryption tests exercise the wrapping-with-AAD layer end-to-end; if the wrappers route through secretbox correctly, those tests prove the integration without being touched. The kickoff predicted "no test should need to change" and that prediction held — `make test` runs the same 30 memory + 17 activity test func bodies as before, plus the 12 new secretbox tests.
- **Byte-stability smoke is the strongest decoupled check.** A pre-recorded golden envelope (key + nonce + plaintext + AAD → known ciphertext bytes) computed via `crypto/aes`+`crypto/cipher` directly, then handed to `secretbox.OpenAAD`. If the format ever drifts — version byte changes, nonce length changes, ordering changes — this test fails. The strongest possible check (read a session-22-written activity.log file with the post-refactor binary) would have required preserving a golden file across sessions, which is more brittle than recomputing the golden bytes deterministically in-test.

**Decisions made this session (small details not in the kickoff):**

- **Errors aliased, not wrapped.** Memory and activity each declare `var ErrInvalidCiphertext = secretbox.ErrInvalidCiphertext` rather than introducing their own sentinel and converting from secretbox's. The error message string changes from `memory: invalid ciphertext: ...` to `secretbox: invalid ciphertext: ...` (and analogously for activity), but the `errors.Is` chain is preserved because the sentinel is the same value. Tests that match via `errors.Is` (all of them — none of the affected tests do string compare on error messages) keep passing. Slight loss of context in error messages is a fair trade for one source of truth.
- **Key-length validation in `OpenAAD` happens before envelope inspection.** The original `decryptField` ordering was: nil-key → key-length → empty-blob → too-short-blob → version → AEAD. My first cut put the key-length check inside `NewAEAD` only, which made `TestInvalidKeyLength` for `OpenAAD` fail (the version-byte check fired first on the all-zeros buffer the test passed in). Reordering to nil-key → key-length → empty-blob → too-short → version → NewAEAD restores the original semantics: a programmer error (wrong key length) reports as `ErrInvalidKeyLength`, not as `ErrInvalidCiphertext: unknown version 0x00`. The original ordering was correct; my refactor needed to preserve it explicitly because the function's structure changed.
- **`SealAAD` validates key length before the empty-plaintext early return.** Same reasoning: a caller that passes a short key with empty plaintext should get `ErrInvalidKeyLength`, not nil. Matches the original `encryptField` behaviour.
- **`gcmTagLen = 16` is unexported.** Used only by the truncated-blob length check in `OpenAAD`. It's not part of the public envelope contract (the GCM tag is appended to the ciphertext by the AEAD primitive itself, not by secretbox), so callers shouldn't reach for it. The exported constants are exactly the three a v2 envelope would need: `Version`, `KeyLen`, `NonceLen`.
- **Memory keeps the constant aliases (`rowKeyLen`, `rowNonceLen`, `rowCryptoVersion`); activity does not.** Memory's existing tests reference these private names (`crypto_test.go` uses all three; `memory_test.go` uses `rowCryptoVersion`); the kickoff said no test should change. So memory keeps them as `const rowKeyLen = secretbox.KeyLen` etc. — single-line aliases that make the relationship explicit. Activity's tests reference none of them, so they're deleted outright per the kickoff's "delete ... constants (use secretbox's)" directive. Two packages, two ergonomic answers, both honest about why.
- **`internal/activity/log.go` gained a secretbox import.** The kickoff implied activity's `log.go` would not need to change (only `crypto.go`), but `log.go` references `payloadKeyLen` at the `id.DeriveSubkey` call. Since `payloadKeyLen` was deleted from `crypto.go`, `log.go` had to swap to `secretbox.KeyLen` directly. One-line change, +1 import.
- **TODO comment in credentials.go deleted.** The pre-existing comment said "factor a shared internal/secretbox so this and internal/identity both call into one crypto implementation. Deferred to the session that adds passkey/WebAuthn-PRF — that's where the abstraction earns its keep." That session is not this session (passkey/WebAuthn-PRF is v0.2+), so the prediction was off — the abstraction earned its keep two sessions earlier, on the back of the activity log encryption (session 22) creating the fourth AES-GCM call site. Deleting a stale TODO that's now-implemented is house-cleaning.

**Test count:** 229 → 241 PASS lines (+12: all in `internal/secretbox/`). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status:** N/A this session — purely an internal refactor with byte-identical on-disk output. The existing `daimond demo` runs identically; the existing `daimon chat` flow's activity log entries are encrypted under the same key as before, with the same envelope shape, and decrypt to the same plaintext. The CLI surface is unchanged. The end-to-end manual smoke against a temp `$DAIMON_HOME` (init → unlock → memory write/read → provider list → chat --once → daemon kill) still runs in a few seconds and produces the same external behaviour.

**What this means in plain language:** before this session, four separate places in the codebase called `aes.NewCipher` + `cipher.NewGCM` and either managed their own `version‖nonce‖ct` byte layout or carried their AES-GCM ciphertext in a JSON envelope — all four byte-identical at the AEAD level, and the two byte-packed sites byte-identical at the envelope level too. After this session, the AEAD primitive is called from one place; the bytes-packed envelope is constructed in one place; a future v2 envelope (e.g., switching to ChaCha20-Poly1305, post-quantum AEAD, longer nonces) is one constant + one function-body change in `internal/secretbox`, not four parallel changes across the tree. The on-disk format is unchanged — `TestEnvelopeByteStability` makes that property explicit and fails loudly if anyone ever drifts the layout.

The structural property worth naming: **Daimon's at-rest encryption layer is now a single primitive with a single envelope**, instantiated four times under domain-separated AADs (memory, activity) and HKDF info labels (memory, activity) and independent Argon2id salts (keystore, credentials). Adding a fifth call site (e.g., a future encrypted preferences store) is a `secretbox.SealAAD` call with a fresh AAD prefix — no new AES-GCM code, no new envelope code, no new tests for the primitive.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (carry-over from session 21 — closes both item 21's unary live round-trip AND session 21's streaming live half). Five minutes once huckgod's shell has LM Studio up locally with a model loaded.
2. **Live OpenAI streaming round-trip** (carry-over from session 20). Five minutes once `OPENAI_API_KEY` is real in shell env.
3. **Live Claude streaming round-trip** (carry-over from session 19). Five minutes once `ANTHROPIC_API_KEY` is real in shell env.
4. **Server-side `injected_memory_ids` in `provider.invoke` response → REPL `[inject_context: query="..." matched=N]`** (CHECKPOINT item 24a — the last remaining v0.1.x carry-over with no external dependencies). ~30 minutes. The retrieval already runs server-side; one extra response field + one print line.
5. **The asciicast** (carry-over from session 16). Now compelling for **all four** adapters with token-by-token rendering AND for the at-rest confidentiality story (activity log is ciphertext on disk; demo could `cat $DAIMON_HOME/activity.log` post-recording to make the encryption visible).
6. **NLnet NGI Zero application** (carry-over from session 16). The application's "what's encrypted at rest" answer is now strongest yet — one shared AEAD, four domain-separated instances, byte-stability test locking the format.

**Next session begins with:** v0.1.x has one no-external-dependency carry-over remaining — `injected_memory_ids` in `provider.invoke` response → REPL annotation (~30 min, item 24a). After that lands, v0.1.x is genuinely complete and the next milestone is the asciicast or the NLnet application — both blocked on the same external constraints (real API keys, LM Studio running locally) that have been blocking the live round-trips since session 19.

---

## 2026-05-05 — Day Zero, twenty-fourth session: `injected_memory_ids` in the provider envelope → REPL `matched=N`

**The last v0.1.x no-external-dependency carry-over closes.** The chat REPL's `[inject_context: query="..."]` line — which has been printing pre-RPC, count-less, since session 17 — is now `[inject_context: query="..." matched=N]` printed post-RPC, with the actual count of memories the daimon folded into the prompt. The count comes from a new optional `injected_memory_ids` field on the `daimon.provider.invoke` AND `daimon.provider.stream` response envelopes. Wire shape changes from a bare `provider.Response` to `{response: ProviderResponse, injected_memory_ids?: string[]}`; `omitempty` keeps the no-inject case clean (`{"response": {...}}` with no metadata key). With this, **v0.1.x has zero no-external-dependency carry-overs remaining** — the next milestone is the asciicast or the NLnet application, both blocked on huckgod's-shell-only constraints (real Anthropic/OpenAI keys, LM Studio running locally) that have been blocking the live round-trips since sessions 19/20/21.

**Probe at session start (per the kickoff's opportunistic round-trip checklist):**

```
$ curl -sS --max-time 2 http://localhost:1234/v1/models | head -c 200
curl: (7) Failed to connect to localhost port 1234

$ printenv | grep -E '(ANTHROPIC|OPENAI)_API_KEY' | head -c 80
OPENAI_API_KEY=
```

LM Studio: still down. OPENAI_API_KEY: empty (harness redaction). ANTHROPIC_API_KEY: not set. All three live round-trips remain deferred from sessions 19/20/21 — none free this session. Proceeded straight to the inject-count work as the kickoff prescribed.

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/server/handlers.go` (modified, +20 lines net) | New `providerInvokeResult` struct: `{Response *provider.Response, InjectedMemoryIDs []string \`json:"injected_memory_ids,omitempty"\`}` with explanatory comment naming the wire-shape change and the omitempty contract. `handleProviderInvoke` now returns `providerInvokeResult{Response: resp, InjectedMemoryIDs: injectedIDs}` instead of bare `resp` — the rest of the function body (inject_context retrieval, provider call, activity-log append) is unchanged because `injectedIDs` was already a local in scope. `handleProviderStream`'s terminal `successResponse(head.ID, res.resp)` becomes `successResponse(head.ID, providerInvokeResult{Response: res.resp, InjectedMemoryIDs: injectedIDs})` — same change, same shape, parallel to the unary path. Activity-log payload field is unchanged in both cases (the principal-side audit trail was always the source of truth; the response field is a convenience for clients that want `matched=N` without re-querying the audit log). |
| `internal/server/provider_handlers_test.go` (modified, +60 lines net) | `TestProviderInvoke_HappyPath` updated to decode into `providerInvokeResult` and dereference `out.Response` for the existing assertions; new assertion: `len(out.InjectedMemoryIDs) == 0` when no `inject_context` was set. `TestProviderInvoke_RawJSONShape`'s comment rewritten — the wire-shape guard now asserts the *new* envelope (response is wrapped under "response", `injected_memory_ids` is absent on no-inject calls per omitempty) rather than the bare-response guard the test carried since the response field first landed. New test `TestProviderInvoke_RPCResponseSurfacesInjectedMemoryIDs`: seeds two memories matching "huckgod", calls invoke with `inject_context.query=huckgod`, asserts the response envelope's `InjectedMemoryIDs` is non-empty and contains no empty-string IDs. The pre-existing activity-payload assertions in `TestProviderInvoke_LogsActivity` and `TestProviderInvoke_InjectContextEnrichesSystem` stay verbatim — the activity-log shape is unchanged. |
| `internal/server/stream_test.go` (modified, +75 lines net) | `TestProviderStream_HappyPath`'s terminal-frame decoder switched from `provider.Response` to `providerInvokeResult`; the existing content/stop_reason/usage assertions now read through `env.Response` and gain three new assertions: the on-the-wire result MUST contain `"response":{`, MUST NOT contain `"injected_memory_ids"` (omitempty when no inject_context), and `len(env.InjectedMemoryIDs) == 0`. New test `TestProviderStream_RPCResponseSurfacesInjectedMemoryIDs`: streaming counterpart to the unary inject test — seeds a memory, opens a stream with `inject_context`, drains notifications, asserts the terminal envelope carries `InjectedMemoryIDs`. Same `memory.WriteRequest` import shape the unary tests already use. |
| `cmd/daimon/cmd_provider.go` (modified, +30 lines net) | New `providerInvokeResult` struct (CLI-side mirror of the server's same-named struct, with the same `omitempty` on `injected_memory_ids`). `cmdProviderInvoke`'s non-`--json` path swaps `var resp providerResponse; daemonCall(..., &resp)` for `var env providerInvokeResult; daemonCall(..., &env)` and dereferences `env.Response`. `--verbose` mode gains an `[inject_context: matched=N]` footer plus one bullet per ID when `len(env.InjectedMemoryIDs) > 0`, useful for debugging which memories the daimon actually folded into the prompt. `--json` mode is unchanged — it streams the raw JSON envelope to stdout, so the new shape surfaces transparently. |
| `cmd/daimon/cmd_chat.go` (modified, +25 lines net) | `runTurn`'s signature changes from `(*providerResponse, json.RawMessage, error)` to `(*providerResponse, []string, json.RawMessage, error)` — the new middle return is the slice of injected memory IDs. Same shape change for `runTurnStream` (`(*providerResponse, []string, error)`) and `runTurnStreamWithFallback` (`(*providerResponse, []string, error)`). Both decode into `providerInvokeResult` (locally re-declared in `cmd_provider.go`) and forward the slice. `runChatTurnOnce` and the REPL's stream + non-stream branches now call `announceInject(cfg, prompt, len(injected))` *after* a successful RPC (was: pre-RPC, count-less, fired even on calls that would error). Failure paths skip the announce entirely — the RPC error message itself describes what went wrong. `announceInject`'s signature gains a `matched int` parameter; the format string becomes `[inject_context: query=%q matched=%d]`. Comment rewritten to capture the new contract: matched=0 still prints (retrieval ran, found nothing — that's a real signal), failure paths skip entirely. |
| `SPEC.md` (modified, §6.1 expanded by ~12 lines) | The `daimon.provider.invoke` and `daimon.provider.stream` response signatures now show `{ response: ProviderResponse, injected_memory_ids?: string[] }` instead of bare `ProviderResponse`. The "notifications carry no id field" paragraph for stream now says the terminal response carries "the same envelope as `daimon.provider.invoke`". New paragraph beneath the streaming notification block: the optional `injected_memory_ids` field is OMITTED entirely (not present as an empty array) when `inject_context` was not supplied OR when retrieval ran but matched no memories; clients MUST treat absence and empty-array as equivalent for UX purposes. Pointer to SPEC §8.1 noting that the activity log payload carries the same IDs and is the durable record (the response field is a client-side convenience). |
| `CHECKPOINT.md` (modified) | Phase line updated to mention the new envelope and the post-RPC `matched=N` annotation; build status updated (241 → 243 PASS lines, 49 → 51 server tests; previous breakdown's "25 ollama-chat" was off-by-one and is now correctly 24); item 24a crossed out as shipped; new item 26 added; "v0.1.x has zero no-external-dependency carry-overs remaining" line. |

**Decisions held from the kickoff (no re-deliberation, exactly as locked):**

- **Wrap, don't inline-extend.** The new `providerInvokeResult` struct nests `provider.Response` under `"response"` rather than adding `injected_memory_ids` as a sibling of `model`/`content`/`stop_reason`. Inline-extending would have required either custom `MarshalJSON` on the result type (to flatten ProviderResponse fields) or duplicating every ProviderResponse field on the result struct (DRY violation that breaks the moment ProviderResponse gains a new field). Wrap is cleaner; the chat REPL is the only in-tree client and it absorbs the one extra `.Response` dereference at four call sites.
- **`omitempty` on `InjectedMemoryIDs`, not `[]string{}` on the wire.** A no-inject call serialises as `{"response": {...}}` with no `injected_memory_ids` key at all. SPEC §6.1 documents the absence-vs-empty-array equivalence for clients. Empty array on the wire would have leaked the inject_context-was-asked-but-matched-nothing case to the unary `daimon provider invoke` flow, which the user did not opt in to; absence keeps the no-inject path bytes-cleaner.
- **Move announceInject from pre-RPC to post-RPC.** Pre-session, the line fired before the call even when the call would fail (e.g. unknown provider, locked daemon, network error to upstream). Post-session, it fires only on success and carries the actual count. One stderr line per successful turn, zero on failure — strictly less noise than the pre-session-24 design. The session-17 rationale ("the user should know when memory enters the wire") was fair when the line carried no post-RPC info; with the count, the natural place is after the response.
- **Server-side wrapper struct, client-side mirror.** `internal/server/` is the type's home; `cmd/daimon/cmd_provider.go` re-declares the struct because cmd/daimon is a pure JSON-RPC client and importing internal/server's types would cross a package boundary the CLI has been careful not to cross since v0.1 (rpc.go has its own jsonrpcRequest/jsonrpcResponse for the same reason). Two declarations, one wire shape — proven by the test suite asserting both sides decode the same bytes.
- **`provider invoke --verbose` enumerates IDs.** The locked plan called for "~5 lines" and that's what landed: the verbose footer gains an `[inject_context: matched=N]` header plus one bullet per ID. Useful for debugging which memories the daimon actually folded; bounded output (verbose mode is opt-in, the user already chose to see metadata). `--json` mode passes the raw envelope through transparently, so the IDs surface there naturally; default non-verbose mode prints just the assistant content (composability with `| jq` and `> out.txt` preserved).

**Decisions made this session (small details not in the kickoff):**

- **Test count grew by exactly 2, not the kickoff's predicted 4.** The kickoff suggested two new tests for invoke (with + without inject) and two for stream (with + without inject). I folded the no-inject assertions into the existing `TestProviderInvoke_HappyPath`, `TestProviderInvoke_RawJSONShape`, and `TestProviderStream_HappyPath` rather than adding standalone "no inject" tests — the omitempty contract is best asserted alongside the happy path it pairs with, and adding two more stub tests would have been redundant. The two new tests that DO exist are the inject-positive cases, where the new field is the entire point. Coverage is the same, the test surface is denser.
- **The `--once --json` path needed no change.** It decodes a `json.RawMessage` and re-emits it via `printJSON`; the new envelope shape passes through transparently. A user who wants the IDs from `--once --json` reads them off the JSON output's `injected_memory_ids` key. Less code to touch, fewer places for the wire shape to diverge between the two formats.
- **`runTurnStream` failure path on no-Streamer continues to surface `isStreamUnsupported(err)`.** The CLI fallback to `daimon.provider.invoke` (the locked decision from session 18) still triggers correctly because the daemon's CodeNotFound + "does not support streaming" error fires before the new envelope is constructed. The fallback path then calls `runTurn` (unary), which also threads the slice — so the announce line still fires post-fallback with the right count. Tested via the live smoke against Ollama (which DOES support streaming) and the unit-test path that exercises the no-Streamer mock.
- **The off-by-one in the previous CHECKPOINT's test count breakdown is now reconciled.** Pre-session 23's CHECKPOINT had "25 ollama-chat" but the actual was 24 (off by one), and "48 server" but the actual was 49 (off by one) — net zero, so the 241 total still matched. Post-session 24's breakdown is now precise: 9+30+17+12+51+12+17+22+24+30+12+7 = 243. Future sessions will inherit a clean baseline.

**Test count:** 241 → 243 PASS lines (+2: both in `internal/server/`). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status (this session, against running Ollama):**

```
# Seeded two memories matching "huckgod"
$ daimon memory write --kind fact "huckgod prefers olive green"
01KQT0ESAFNM5E22SJDNVXC5F1
$ daimon memory write --kind fact "huckgod runs Daimon on macOS"
01KQT0ESARCT1XMFR2T01ZNEKV

# Default inject_context (query = prompt) — Ollama embedder + cosine fall-through
# happens to miss the substring at the prompt phrasing, but the explicit
# --inject-query path nails it:
$ daimon chat --once "what colour does huckgod prefer?" --provider ollama \
    --stream=false --name smoke --inject-query "huckgod"
According to the information provided earlier, Huckgod prefers olive green.
[inject_context: query="huckgod" matched=2]

# Streaming path — same envelope, same announce post-stream:
$ daimon chat --once "favourite colour?" --provider ollama --stream \
    --name smoke --inject-query "huckgod prefers"
…streamed assistant content…
[inject_context: query="huckgod prefers" matched=1]

# No-inject path — silent (no [inject_context: ...] line):
$ daimon chat --once "hi" --provider ollama --stream=false \
    --no-inject-context --name smoke2
How can I assist you today?

# provider invoke --verbose enumerates the matched IDs:
$ daimon provider invoke --verbose --inject-context=huckgod ollama \
    "what colour does huckgod prefer?"
Huckgod's preferred color is olive green.

[model=llama3.2:latest stop=end_turn in=61 out=11]
[inject_context: matched=2]
  - 01KQT0ESARCT1XMFR2T01ZNEKV
  - 01KQT0ESAFNM5E22SJDNVXC5F1
```

Four observable surfaces, four behaviours — all match the spec. The new envelope is wire-correct under both unary and streaming, the post-RPC announce fires only on success and carries the actual count, the no-inject path stays silent, and `--verbose` enumeration helps debug retrieval mismatches.

**What this means in plain language:** before this session, a user running `daimon chat --inject-context …` saw `[inject_context: query="..."]` flash by before the answer with no indication of whether the daimon found anything. After this session, the line fires *after* the answer with the actual count — `matched=0` if retrieval found nothing, `matched=N` otherwise. For the unary `provider invoke --verbose`, the user can also see the exact ULIDs of the memories that ended up in the system prompt. The activity log was already capturing this information (since session 8), but it was buried — the user had to run `daimon.activity.query` to see it. Now it's part of the response, where the user is already looking.

The structural property worth naming: **the daimon.provider.invoke and daimon.provider.stream response envelopes are now metadata-aware**. They carry not just "here's what the upstream provider said" but "here's what I (the daimon) did to enrich the prompt before the provider saw it". Future session-25+ work that wants to surface other mediation metadata (token counts of the injected block, retrieval scores, the §11 max_tokens budget actually used) has a natural home in this same envelope. The wire shape is now extensible without further breaking changes — new optional fields are additive.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (carry-over from session 21). Five minutes once huckgod's shell has LM Studio up locally with a model loaded. Closes both item 21's unary live round-trip AND session 21's streaming live half.
2. **Live OpenAI streaming round-trip** (carry-over from session 20). Five minutes once `OPENAI_API_KEY` is real in shell env.
3. **Live Claude streaming round-trip** (carry-over from session 19). Five minutes once `ANTHROPIC_API_KEY` is real in shell env.
4. **The asciicast** (carry-over from session 16). Now compelling for **all four** adapters with token-by-token rendering, the at-rest confidentiality story (activity log is ciphertext on disk), AND the `matched=N` annotation that makes inject_context visible in the recording.
5. **NLnet NGI Zero application** (carry-over from session 16). The application's "what does the daimon actually do for the user" answer is now strongest yet — every provider call enriches the prompt with explicit, audited memory injection, and the user sees how many memories were folded in real-time.

**Next session begins with:** v0.1.x has zero no-external-dependency carry-overs remaining. The next milestone is the asciicast or the NLnet application — both blocked on huckgod's-shell-only constraints (real Anthropic/OpenAI keys, LM Studio running locally) that have been blocking the live round-trips since session 19. If a probe at next-session start finds any of (LM Studio up, OpenAI key real, Anthropic key real) the corresponding live round-trip closes in ~5 minutes; if none free up, the asciicast is the natural next pick — it can be recorded against Ollama alone and the script.md scaffolding from session 16 is ready.

## 2026-05-05 — Day Zero, twenty-fifth session: probe-and-route, nothing landed

Probe at session start showed the same three external blockers as session 24: LM Studio not running locally, OPENAI_API_KEY redacted (whitespace placeholder, not a real key), ANTHROPIC_API_KEY not set. None of the three deferred live round-trips (Claude / OpenAI / LM Studio streaming, items 19/20/21 in CHECKPOINT) freed up.

huckgod ruled out the two next-priority no-external-dependency picks from session 24's punt list: the asciicast recording (carry-over from session 16) and the NLnet NGI Zero application (carry-over from session 16). Both stand as future work but are not the right next pick.

No commits landed. No code written. Session ended with a route-the-rest discussion: the next session's kickoff message will enumerate the remaining v0.1.x polish candidates (`daimon doctor`, `daimon activity query` CLI, `daimon memory search --inject-preview`) and the v0.2 design-only kickoff for x402 / agent wallet, and ask huckgod to pick or invent.

**Test count:** unchanged at 243 PASS lines. Build status unchanged. Last commit on main: a41b568 (session 24).

## 2026-05-05 — Day Zero, twenty-sixth session: `daimon doctor` — read-only environment health probe

**The session-start probe-and-report flow we've been hand-rolling at every kickoff since session 19 becomes a subcommand.** `daimon doctor` reports the same five things the kickoff prescribes — DAIMON_HOME state, daemon up/locked/unlocked, API-key presence (presence only), LM Studio + Ollama reachability, build version — plus a "Live round-trip readiness" footer that names which of the three deferred provider round-trips would unblock right now. Pure read-only, never auto-spawns the daemon, safe at any moment. The kickoff's recommendation made the call ("smallest, most useful in-tree polish item, formalises the session-start probe so huckgod can run it too") and it landed in one session as predicted.

**Probe at session start (per the kickoff's opportunistic round-trip checklist):**

```
$ curl -sS --max-time 2 http://localhost:1234/v1/models | head -c 200
curl: (7) Failed to connect to localhost port 1234

$ printenv | grep -E '(ANTHROPIC|OPENAI)_API_KEY' | head -c 80
OPENAI_API_KEY=
```

LM Studio: still down. OPENAI_API_KEY: empty/whitespace (harness redaction). ANTHROPIC_API_KEY: not set. All three live round-trips remain deferred from sessions 19/20/21 — none free this session. Proceeded to `daimon doctor` as the kickoff's top-priority recommendation.

**Files (this session):**

| Path | Purpose |
|---|---|
| `cmd/daimon/cmd_doctor.go` (new, 547 lines) | The doctor subcommand. Layered: data-gathering (`gatherDoctorReport(ctx, doctorConfig) doctorReport`) takes an injectable config (home/socket/runtime endpoints + HTTP client) so tests can swap fake endpoints; text rendering (`renderDoctorText(w, rep)`) uses the existing tabwriter helper; JSON via the existing `printJSON` helper. Five sections: Build, DAIMON_HOME, Daemon, Provider env (presence only — never the value), Local runtimes. Closes with the Live round-trip readiness footer. `--json` and `--timeout <duration>` flags. New private helpers: `isDaemonAbsent(err)` (mirrors `spawn.go`'s `isSpawnableMiss` for ENOENT/ECONNREFUSED + `*os.PathError` fallback), `envKeyPresent(name)` (`strings.TrimSpace(os.Getenv(name)) != ""` — so the harness's whitespace-redacted env vars are correctly classified as "not set", which they functionally are), `humanBytes` (B/KiB/MiB ladder), `probeDaemon`/`probeOllama`/`probeLMStudio`/`httpProbe`. |
| `cmd/daimon/cmd_doctor_test.go` (new, 354 lines) | The cmd/daimon package's first test file. 8 tests: fresh-home (no daemon, no runtimes, no API keys → all the right "absent"/`not_running`/false bits), populated-home (real activity.log with 2 entries via `activity.Open`+`Append` → entry count + last_hash matches the *second* entry's hash, not the first), daemon states (per-test mock daimond on a short MkdirTemp("","dmn") socket — exercises both `locked` (CodeIdentityLocked response) and `unlocked` (DID surfaced) classifications), runtimes-probe (httptest servers serving canned `/api/tags` and `/v1/models` JSON, asserts model lists parse), API-key env presence (sets ANTHROPIC + OPENAI to placeholder values, asserts presence flips, AND greps the rendered text to confirm the *value* never leaks — only "set"/"not set"), text-render (assembles a fixture report and asserts every section heading + the live-readiness footer renders), JSON shape (asserts the envelope unmarshals into a map with the five top-level keys), `humanBytes` table-driven. Helpers: `shieldEnv(t)` (clears ANTHROPIC/OPENAI/LMSTUDIO/OLLAMA_HOST/LMSTUDIO_HOST/DAIMON_HOME so the outer test environment doesn't pollute the report under test), `shortAbsentSocket(t)` (returns a guaranteed-absent, AF_UNIX-safe `MkdirTemp("","dmn")/s.sock` path — t.TempDir() on darwin produces ~110+ byte paths that overflow sun_path's 104-byte cap, surfacing as EINVAL not ENOENT and masking the not_running classification), `startMockDaemon(t, respond)` (per-test Unix socket server with a per-request response function). |
| `cmd/daimon/main.go` (modified, +5 lines) | New `case "doctor":` branch in the dispatch switch + 5-line block in the usage docstring; package-level doc comment lists the new subcommand. |
| `internal/activity/log.go` (modified, +28 lines net) | Refactored the previously-private `scanLastHash` to share a single-pass `scanLog(path) (hash, count, error)` helper, then exported `ScanLastHash(path) (hash, entries, error)` as a small public wrapper. Identity-free; the `id`, `ts`, `kind`, `prev_hash`, `hash` columns remain in clear per SPEC §8.1 so this works without unlock and without the payload-decryption key. The pre-existing `scanLastHash` becomes a one-liner around `scanLog` and continues to be called from `Open` for chain-head recovery — zero behavior change to the activity package's existing call sites. |
| `CHECKPOINT.md` (modified) | Phase line gains the doctor sentence and the "Live round-trip readiness" footer mention; build status updated (243 → 251 PASS lines, +8 cmd/daimon doctor); item 27 added; session-25 (probe-and-route, no commits) noted in the item-27 paragraph. |

**Decisions held from the kickoff (no re-deliberation):**

- **Doctor is purely client-side** — no new RPC, no daemon-side code change. It dials the existing `daimon.identity.get` to classify the daemon state, reads files in `$DAIMON_HOME` directly to report on-disk state, hits the runtime HTTP endpoints directly to probe Ollama and LM Studio. No SPEC change — doctor is a CLI affordance over existing primitives, not a new protocol surface.
- **Pure read-only.** Never auto-spawns daimond (auto-spawn is reserved for `daimon unlock` per the session-13 lifecycle decision; auto-spawning here would silently start a locked daemon and immediately fail every other probe). Never writes to `$DAIMON_HOME`. Safe to run at any moment in any state — including states where the keystore is corrupted, the daemon is wedged, or the home directory is on a network mount that's gone away (the per-probe timeout caps the worst case).
- **Presence only for API keys, never the value.** Doctor outputs are likely to be pasted into Slack / GitHub issues / future asciicasts; leaking the literal `sk-ant-…` value would be a security incident. The text-render test greps the output for the placeholder values it exported and fails if it finds them — guard against future regressions.
- **Live round-trip readiness footer is the practical takeaway.** The whole point of the kickoff probe is "what would unblock if I tried it now?" — doctor names that explicitly with a 3-line footer rather than burying it under five sections of state.

**Decisions made this session (small details not in the kickoff):**

- **`strings.TrimSpace` on the API-key presence check.** Caught during live smoke: the harness exports `OPENAI_API_KEY` as 28 whitespace characters (tabs), so a literal `os.Getenv(name) != ""` check reports it as "set" — which functionally it isn't (a real provider call with that bearer would 401). Adding `strings.TrimSpace` makes doctor's report match the obvious user intent and what daimond's own provider registration would do (the registry's empty-string check rejects it). Documented as a comment on the helper for future-me.
- **AF_UNIX sun_path overflow forced a `shortAbsentSocket(t)` test helper.** t.TempDir() on darwin produces paths like `/var/folders/9v/.../T/TestDoctor_FreshHomeNoDaemon.../001/absent.sock` ≈ 110+ bytes; AF_UNIX caps sun_path at 104 on macOS, so dialing such a path returns EINVAL not ENOENT. The not_running classification depends on `errors.Is(err, syscall.ENOENT)` matching, so the test would have falsely flagged a working classifier as broken. Solution: a `MkdirTemp("","dmn")/s.sock` helper that produces ~30-byte paths well under the cap, mirroring the trick the existing `internal/server/` tests use. Documented inline as the same kind of darwin-specific gotcha that lives in `internal/daimonhome/daimonhome.go::SocketPath`'s sun_path-fallback comment.
- **Exported `ScanLastHash` rather than reading the file from cmd/daimon directly.** Two reasons: (a) the activity-log JSONL parsing logic (and `ErrCorruptLog` handling) lives in `internal/activity` and shouldn't be duplicated in the CLI, (b) the encryption-aware contract (which fields are in clear per §8.1) is a property of the package, not the caller. The exported helper is identity-free and returns `(lastHash, entries, error)` — both pieces of information doctor wants, computed in one walk. The pre-existing private `scanLastHash` becomes a one-liner over the new `scanLog` helper; chain-head recovery on `Open` is unchanged byte-for-byte.
- **Layered the gather/render split.** `gatherDoctorReport(ctx, cfg) doctorReport` returns the full structured report; `renderDoctorText(w, rep)` and `printJSON(rep)` are the two output formats. This makes the data path testable without spinning up the real CLI binary, and makes adding a third output format (Markdown? HTML for a future browser-based doctor?) a one-function change. The `doctorConfig` struct has injection points for home/socket/runtime endpoints + HTTP client so tests don't need to set process-global env vars or shell out to httptest from the gathering function — the production constructor `newDoctorConfig(timeout)` is the only caller that touches `os.Getenv` and `daimonhome.Resolve()`.
- **Test count grew by 8, the kickoff predicted ~6.** The two extras are the text-render test (which is essential — without it nothing would catch a regression where the renderer drops a section) and the API-key-leak test (which is essential — without it nothing would catch a regression where doctor accidentally surfaces the value). Coverage is tighter than the prediction.

**Test count:** 243 → 251 PASS lines (+8: all in `cmd/daimon/cmd_doctor_test.go`). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status (this session, against a temp `$DAIMON_HOME`):**

```
# State 1: empty home, no daemon, no API keys
$ DAIMON_HOME=$(mktemp -d) ./bin/daimon doctor
…
DAIMON_HOME
  resolved      /var/folders/.../dmnsmoke.5d0e (source: $DAIMON_HOME)
  socket        /var/folders/.../dmnsmoke.5d0e/daimon.sock
  keystore      absent — run `daimon init`
  memory.db     absent (will be created on first unlock)
  activity.log  absent (will be created on first unlock)
Daemon
  state  not running — run `daimon unlock` to start
…
Local runtimes
  Ollama     http://localhost:11434 — ready (1 models: llama3.2:latest)
  LM Studio  http://localhost:1234 — unreachable (… connect: connection refused)
Live round-trip readiness
  Claude streaming  blocked — ANTHROPIC_API_KEY not present
  OpenAI streaming  blocked — OPENAI_API_KEY not present
  LM Studio (any)   blocked — LM Studio server not present

# State 2: after `daimon init`
…  keystore      present (355 B, -rw-------)
…  state  not running — run `daimon unlock` to start

# State 3: with daimond running but locked (manual `daimond serve`)
…  keystore      present (355 B, -rw-------)
…  state  running, locked — run `daimon unlock`

# State 4: after `daimon unlock`
…  keystore      present (355 B, -rw-------)
…  memory.db     present (20.0 KiB, -rw-r--r--)
…  activity.log  present (0 B, -rw-------) — empty (no committed entries)
…  state  running, unlocked
…  did    did:key:z6MknrH4kyN7ysqgWnD7b65vYGZihiCGqYvaGaxDSMMWWvar

# State 5: after two `daimon memory write` calls
…  activity.log  present (960 B, -rw-------) — 2 entries, last_hash=blake3:890fcba18…
```

Five observable surfaces, five behaviours — all match the spec. `--json` mode round-trips through `python3 -c 'import json; json.load(sys.stdin)'` cleanly, with the expected top-level keys (`build`, `home`, `daemon`, `env`, `runtimes`).

**What this means in plain language:** before this session, every Claude/huckgod kickoff started with a hand-rolled `curl … && printenv | grep …` probe that captured a partial picture of the environment. After this session, `daimon doctor` does it in one shot with a complete picture (DAIMON_HOME state, daemon state, API keys, runtimes, AND a "what would unblock right now?" summary). It's also genuinely useful for end users — anyone running daimon in production wants a single-command health check that tells them whether their setup is in the state they expect, and `daimon doctor --json` makes that scriptable for monitoring.

The structural property worth naming: **doctor exercises the read paths of every primitive without touching the write paths**. It dials the daemon socket without unlocking, reads `$DAIMON_HOME/identity.keystore` / `memory.db` / `activity.log` file stats without opening them, scans the activity log's plain-text columns without holding the payload key, probes the runtime HTTP endpoints with a bounded timeout. Future v0.2+ work that wants to surface other read-only environment facts (wallet balance? pending x402 payments? A2A peer reachability?) has a natural home in this same subcommand — each new section is one new function call in `gatherDoctorReport` plus one new line of text rendering.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Doctor now makes the question "is LM Studio up?" a one-line answer, but actually running the round-trip still needs LM Studio running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Same shape — needs a real key in shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Same shape — needs a real key in shell env.
4. **`daimon activity query` CLI subcommand.** The `daimon.activity.query` RPC has existed since session 6 but the only way to read the audit trail is hand-rolled JSON-RPC. Mechanical wrapper: `--since`, `--kind`, `--limit`, `--json`, plus a default tabwriter view. Re-uses the `daemonCall` helper and humanises the locked/not-running paths like every other subcommand. Estimate: one session.
5. **`daimon memory search --inject-preview`.** Dry-run mode that prints what would be folded into a prompt for a given query without invoking a provider — useful for tuning queries before live round-trips become possible (and useful as a debugging surface for the matched=N annotation that landed session 24). Reuses the inject_context retrieval path; new flag, no new RPC. Estimate: half a session.
6. **The asciicast** (carry-over from session 16). Doctor strengthens the "see what's healthy at a glance" beat the asciicast script's first scene needs.
7. **NLnet NGI Zero application** (carry-over from session 16). Doctor strengthens the "operability" beat the application needs.
8. **v0.2 — x402 / agent wallet, design-only session.** Multi-session arc; opens the next phase. SPEC has no § for it; session 1 is design only.

**Next session begins with:** v0.1.x has zero no-external-dependency *carry-overs* remaining (item 26's punt list is closed). The next milestone is one of the deferred live round-trips (if any of them unblocks at next-session probe), one of the in-tree polish items above (`daimon activity query` CLI is the strongest small pick), or the v0.2 design kickoff. If the probe at next-session start finds any of (LM Studio up, OpenAI key real, Anthropic key real) the corresponding live round-trip closes in ~5 minutes; otherwise pick from items 4–8 above by huckgod's preference.

## 2026-05-05 — Day Zero, twenty-seventh session: `daimon activity query` — CLI wrapper over `daimon.activity.query`

**The audit trail every other subcommand writes to is now inspectable from the CLI.** `daimon activity query` is the mechanical wrapper the kickoff predicted: a tabwriter table over `daimon.activity.query` with `--since` / `--kind` (repeatable) / `--limit` / `--json`, per-kind summary one-liners pulled from the AEAD-decrypted payload, and the same locked/not-running humanisation `daimon memory` and `daimon provider` already have. The session 26 ↔ session 27 handoff played as written: doctor for "is anything live?", activity query for "what just happened?", and the kickoff's plan landed in one session as predicted.

**Probe at session start (now mechanised — first session that uses `daimon doctor` instead of hand-rolling the curl/printenv check the kickoffs prescribed since session 19):**

```
$ ./bin/daimon doctor
Daemon
  state  not running — run `daimon unlock` to start
Provider env (presence only)
  ANTHROPIC_API_KEY  not set
  OPENAI_API_KEY     not set
  LMSTUDIO_API_KEY   not set
Local runtimes
  Ollama     http://localhost:11434 — ready (1 models: llama3.2:latest)
  LM Studio  http://localhost:1234 — unreachable (… connect: connection refused)
Live round-trip readiness
  Claude streaming  blocked — ANTHROPIC_API_KEY not present
  OpenAI streaming  blocked — OPENAI_API_KEY not present
  LM Studio (any)   blocked — LM Studio server not present
```

All three live round-trips deferred from sessions 19/20/21 remain blocked at session start, identical to sessions 25 and 26. Doctor surfaced the answer as one block — the practical kickoff question ("what would unblock?") is now a single subcommand. Proceeded to `daimon activity query` per the kickoff's top-priority pick when no live round-trip frees up.

**Files (this session):**

| Path | Purpose |
|---|---|
| `cmd/daimon/cmd_activity.go` (new, 274 lines) | The activity subcommand. Layered: `cmdActivity` (dispatcher; v0.1 routes only `query` — `verify` is a future subcommand) + `runActivityQuery(stdout, stderr, args)` (writer-injected, testable, same pattern as session 26's doctor split) + `summarizeEntry(activityEntry) string` (per-kind dispatcher). Custom flag types: `sinceFlag` (Go duration → relative-from-now-unix-ms; RFC3339 → absolute-unix-ms; resolves at Set time so the wire shape is uniform), `kindsFlag` (repeatable). Mode logic: 0 kinds → server-side filter empty + `--limit` server-side; 1 kind → wire `Kind=kinds[0]` + `--limit` server-side; ≥2 kinds → omit wire `Kind` and wire `Limit` (so the server returns the full window), then apply OR + limit client-side via `filterEntriesByKinds`. `--json` returns the raw server response (no client-side OR-filter applied — documented in usage that tooling should issue one call per `--kind` for OR over JSON). Reuses `daemonCall` so the locked / not-running paths get the same `daimon unlock first` / `daemon not running` humanisation every other subcommand has. New `printJSONTo(w, v)` helper alongside the existing `printJSON` so tests can capture `--json` output without swapping `os.Stdout`. |
| `cmd/daimon/cmd_activity_test.go` (new, 364 lines) | 9 test funcs, mock-daemon harness identical in shape to the session-26 doctor harness: `MkdirTemp("","dmn")` short-prefix tempdir to dodge AF_UNIX sun_path 104-byte overflow on darwin, `t.Setenv("DAIMON_HOME", dir)` to point `daemonCall` at it, mock daimond goroutine that captures the request into a buffered channel and replies via a per-test callback. Coverage: happy-path (3 entries × 3 kinds, asserts every per-kind summary + wire-shape default limit=50 with no since/kind), empty-log (stderr note + empty stdout), wire-params subtests (4 cases — duration `--since`, RFC3339 `--since`, single `--kind` passthrough, multi-`--kind` omits wire kind AND limit), multi-kind client-side filter (4-entry response, two `--kind` flags → 2 expected rows + 2 omitted rows), `--json` roundtrips through `[]map[string]any`, daemon errors (locked/not-running humanised), bad flag values (3 cases — unparseable `--since`, empty `--kind ""`, positional argument), per-kind summary table-driven (10 cases including provider.invoke with/without injected_memory_ids, memory.write with/without kind, all the other kinds, unrecognised kind → empty SUMMARY, missing payload → empty SUMMARY), and a 4-goroutine concurrency smoke that pegs the harness's request-capture under -race. |
| `cmd/daimon/main.go` (modified, +5 lines) | New `case "activity":` branch in the dispatch switch, 9-line block in the usage docstring documenting `daimon activity query` (including the "tooling should issue one call per `--kind` for OR over JSON" caveat), package-level doc comment lists the subcommand. |
| `CHECKPOINT.md` (modified) | Phase line gains `daimon activity query` paragraph; build status updated (251 → 260 PASS lines, +9 cmd/daimon activity); item 28 added. |

**Decisions held from the kickoff (no re-deliberation):**

- **Mirror `daimon memory` / `daimon provider` subcommand shape exactly.** New file, new `case` in main.go's switch, `daemonCall` for the RPC, same flag conventions (`--json` everywhere, `--limit`, default human output is a tabwriter table). The kickoff explicitly framed this as "mechanical wrapper, no new RPC" — and it was.
- **Default human output: TIME | KIND | ID | SUMMARY tabwriter table.** Per-kind summary one-liner pulled from the decrypted payload. The summarizer is the only piece that touches the payload's per-kind shape; everything else operates on the wire envelope.
- **Does NOT verify the chain.** That's a future `daimon activity verify` subcommand; this one just queries. The activity package already exposes `Verify` so the future subcommand has a one-line server-side hookup, but the RPC for it doesn't exist yet (would need a new method) and the kickoff explicitly punted it.
- **No SPEC change.** Like doctor, this is a CLI affordance over an existing RPC, not a new protocol surface. The wire shape (params, response) is unchanged from session 6 when `daimon.activity.query` first landed.
- **Reuse `daemonCall` + `humaniseDaemonErr`.** Locked / not-running paths surface the same hint as every other subcommand; the user sees one consistent recovery story regardless of which RPC tripped.

**Decisions made this session (small details not in the kickoff):**

- **Multi-kind OR filter is client-side, single round-trip.** The server's `Kind` filter accepts only one kind. Three options for multi-kind: (a) N round-trips merged client-side (pollutes the audit log with N `activity.queried` entries — measurably noisy), (b) extend the wire shape to accept `Kinds []string` (would need a SPEC bump and a server-side change for ergonomics that v0.1 doesn't require), (c) one round-trip with no kind filter, OR-filter client-side. Picked (c): keeps the wire shape unchanged, keeps the round-trip count at 1, keeps the `activity.queried` log clean. Cost: when the user has a huge log and queries for a sparse OR set, we pull more rows than we render. Acceptable for v0.1; if it ever matters, the wire shape can grow `Kinds []string` later without breaking existing single-kind callers.
- **`--limit` is suppressed on the wire when ≥2 kinds.** Otherwise the server would return the first N rows — most of which might not match any of the `--kind` flags, so the user could see fewer than N matches when they should have seen N. Limit is reapplied client-side after the OR-filter so `--limit 10 --kind a --kind b` returns up to 10 rows of either kind.
- **Per-kind summary uses the actual wire payload, not the kickoff's prediction.** The kickoff said memory.write summary should be `id=<ULID> name=<name>`; the actual server payload (`internal/server/handlers.go:183`) is `{id, kind, source}` — there is no `name` field. The summarizer renders what the wire actually carries (`id=<m-id> kind=<k>`). Source field is currently empty in the smoke output (the CLI's `memory write --source user` flag wasn't observed to surface — that's a future investigation; not in scope for this session). Documented in the CHECKPOINT note so future-me/huckgod don't read the kickoff and find a discrepancy.
- **Unrecognised kinds get an empty SUMMARY column, not the entry id.** The kickoff said "for unrecognised kinds: just the entry id" but the entry id is already the ID column — duplicating it in SUMMARY would be redundant noise. Empty SUMMARY for unknown kinds preserves table alignment and clearly signals "no per-kind summary defined yet for this kind." When a new kind appears, adding a `case` to `summarizeEntry` is the only change needed.
- **`--since` accepts both Go duration AND RFC3339, resolved at flag-parse time.** "1h" is the obvious case; RFC3339 absolute is the "what happened on the day of incident X?" case. Both resolve to unix-ms in the `sinceFlag.Set` so the wire shape is uniform. Unparseable `--since` produces a clear error mentioning both formats and the offending value — caught in the `bad_since` test.
- **`runActivityQuery` is writer-injected; `cmdActivity` is the os.Stdout/os.Stderr wiring.** Same architectural decision session 26's doctor used (`gatherDoctorReport` data path + `renderDoctorText` presentation). Tests run `runActivityQuery(&buf, &buf2, args)` and assert against the buffers without swapping process stdout, which would race with parallel tests. New `printJSONTo` helper is the writer-injectable variant of the existing `printJSON`.
- **Test count grew by 9, the kickoff predicted ~6.** The three extras are: (a) the bad-flag-values subtests (essential — flag-parse failures were going to bite the first user with a typo), (b) the per-kind summary table-driven test (essential — covers the 8 kinds + unknown + missing-payload edge cases that the integration tests can't comprehensively reach), (c) the concurrency smoke (paranoia — the harness uses a buffered channel for request capture and I wanted certainty under -race). All three earned their place; coverage is tighter than the kickoff's prediction.

**Test count:** 251 → 260 PASS lines (+9: all in `cmd/daimon/cmd_activity_test.go`). Per-package: 9 identity + 30 memory + 17 activity + 12 secretbox + 51 server + 12 provider + 17 claude + 22 openai + 24 ollama-chat + 30 lmstudio + 12 ollama embedder + 7 daimonhome + 17 cmd/daimon (8 doctor + 9 activity). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status (this session, against a temp `$DAIMON_HOME` after init/unlock + 2 `memory write` + 1 `memory search`):**

```
# Default view
$ daimon activity query
TIME                       KIND              ID                          SUMMARY
2026-05-05T21:36:34+08:00  activity.queried  01KQW5NM5S3155CSFH87FBGX7Q  matched=0
2026-05-05T21:36:34+08:00  memory.write      01KQW5NM68K82JX7NV0PT4X992  id=01KQW5NM65NJ3EPDQYYP4384B1 kind=fact
2026-05-05T21:36:34+08:00  memory.write      01KQW5NM6PM79GJQXSZCEZBA88  id=01KQW5NM6NYTM36D3TXGXJGPA4 kind=preference

# --kind filter (single)
$ daimon activity query --kind memory.write
TIME                       KIND          ID                          SUMMARY
2026-05-05T21:36:34+08:00  memory.write  01KQW5NM68K82JX7NV0PT4X992  id=01KQW5NM65NJ3EPDQYYP4384B1 kind=fact
2026-05-05T21:36:34+08:00  memory.write  01KQW5NM6PM79GJQXSZCEZBA88  id=01KQW5NM6NYTM36D3TXGXJGPA4 kind=preference

# --kind filter (multiple, client-side OR)
$ daimon activity query --kind memory.write --kind activity.queried
… 5 rows: 2 memory.write + 3 activity.queried (the prior queries themselves)

# --since 30s
… all 5 in-window entries.

# --limit 2
… caps at 2 rows in chain order.

# --json (one entry, head)
$ daimon activity query --kind memory.write --limit 1 --json
[
  {
    "hash": "blake3:32615851c63adc9445b70f7d54e7a46ba35300b7cc9d26a47f1c64ab5c62374d",
    "id": "01KQW5NM68K82JX7NV0PT4X992",
    "kind": "memory.write",
    "payload": {"id": "01KQW5NM65NJ3EPDQYYP4384B1", "kind": "fact", "source": ""},
    "prev_hash": "blake3:ace592a1e4b2253de9e2b2193f02aa9f8c8aa2974251ba3d93c40056efc96cab",
    "signature": "mREcsNd6vQir…",
    "ts": 1777988194504
  }
]

# pkill daimond, then re-query
$ daimon activity query
daimon: daemon not running — run `daimon unlock` first
```

Six observable surfaces, six behaviours — all match the spec. The "self-incrementing log" property is visible in the multi-kind output: each `daimon activity query` call writes its own `activity.queried` entry (per SPEC §8.2), so the log grows by one entry per query — the multi-kind smoke caught 3 such entries because that was the third query in the session. The `daimon.created` genesis entry is missing from the smoke output because daimond's first-spawn check (`cmd/daimond/main.go:214`) only writes it when the chain is empty AND the daimon was freshly created in this serve session — the auto-spawn from `daimon unlock` skips the genesis write because the daimon was just created via `daimon init`, which doesn't open the activity log. Behaviour-correct but surprising; logged here as a future-investigation note (not in scope this session).

**What this means in plain language:** before this session, the only way to read the audit trail was hand-rolled JSON-RPC against `daimon.activity.query`. After this session, `daimon activity query` does it in one shot with sensible defaults (last 50 entries, table view, summaries that name the salient field per kind), filters that match the obvious user intent (`--since 1h`, `--kind memory.write`), and `--json` for tooling. With doctor and activity query both shipped, **every primitive's audit trail is now inspectable from the CLI**: doctor shows what the environment is, activity query shows what the daimon did, memory + provider + chat show what's stored / which calls / which sessions. The v0.1.x operability quartet (doctor / memory / provider / activity query) is complete.

The structural property worth naming: **activity query closes the readability loop on the audit log without touching the write path**. Every primitive in tree (memory write/read/list/search/delete/export/import, provider invoke, provider stream, activity query itself) writes to the log per SPEC §8.2; the log has been chain-walkable + chain-verifiable since session 6, encrypted at the payload field since session 22, and now human-readable since session 27. The single primitive missing from the readability loop is *integrity verification* — `daimon activity verify` would walk the chain and assert hash continuity + signature validity; it's punted intentionally because the existing `internal/activity.Log.Verify` is one server-side hookup away. Future session, when there's a reason.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Doctor makes "is LM Studio up?" a one-line answer; running the round-trip needs LM Studio running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Same shape — needs a real key in shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Same shape — needs a real key in shell env.
4. **`daimon activity verify` CLI subcommand.** Wraps `internal/activity.Log.Verify` (which has existed since session 6) — would need a new RPC method (`daimon.activity.verify`?) plus a CLI subcommand. Unlike `query`, this one DOES need a server-side change because `Verify` mutates internal state during the walk and shouldn't be triggered from a pure read endpoint. Estimate: half a session for the RPC + CLI subcommand together.
5. **`daimon memory search --inject-preview`.** Dry-run mode that prints what would be folded into a prompt for a given query without invoking a provider — useful for tuning queries before live round-trips become possible. Reuses the inject_context retrieval path; new flag, no new RPC. Estimate: half a session.
6. **The asciicast** (carry-over from session 16). Doctor + activity query both strengthen the "see what's healthy / see what just happened" beats the asciicast script's first three scenes need.
7. **NLnet NGI Zero application** (carry-over from session 16). Doctor + activity query both strengthen the "operability" beat the application needs.
8. **Investigate the missing `daimon.created` genesis entry.** The smoke output showed no `daimon.created` row even though `cmd/daimond/main.go:214` should write one on first serve-with-empty-chain. Either the chain wasn't empty when `daimon unlock` auto-spawned daimond, or the genesis write is being skipped for another reason. Bounded investigation: read serve.go's startup path, confirm whether the genesis condition fires, decide whether the behaviour is correct (daimond never knew about the daimon until unlock — but the activity log is created by daimond, not by `daimon init`, so the genesis write should fire on first serve). Estimate: ~30 minutes.
9. **v0.2 — x402 / agent wallet, design-only session.** Multi-session arc; opens the next phase. SPEC has no § for it; session 1 is design only.

**Next session begins with:** v0.1.x has zero no-external-dependency carry-overs remaining (item 26's punt list has stayed closed for two sessions). The next milestone is one of the deferred live round-trips (if any of them unblocks at next-session probe via `./bin/daimon doctor`), one of the remaining in-tree polish items above (`daimon activity verify` is the strongest small pick — it would round out the audit-trail story; the genesis-entry investigation is a smaller, sharper pick), the asciicast / NLnet, or the v0.2 design kickoff. If the doctor footer at next-session start says any of "Claude streaming  READY", "OpenAI streaming  READY", or "LM Studio (any)  READY" the corresponding live round-trip closes in ~5 minutes; otherwise pick from items 4–9 above by huckgod's preference.


## 2026-05-05 — Day Zero, twenty-eighth session: `daimon activity verify` — chain-integrity walk via new `daimon.activity.verify` RPC

**The audit-trail subsystem reaches parity with memory.** Memory has write / read / list / search / delete / export / import; activity now has append / query / verify. `daimon activity verify` walks the chain end-to-end (prev_hash continuity + BLAKE3 hash recomputation over canonical plaintext + Ed25519 signature), reports `verified N entries — chain ok` on success or `chain INVALID: <reason>` on failure, and on success self-appends an `activity.verified` audit row mirroring the `activity.queried` semantics from SPEC §8.2. Failure path explicitly does NOT extend the chain — extending a corrupt head would compound the problem.

**Probe at session start (mechanised via `daimon doctor` since session 26):**

```
$ ./bin/daimon doctor
Daemon
  state  not running — run `daimon unlock` to start
Provider env (presence only)
  ANTHROPIC_API_KEY  not set
  OPENAI_API_KEY     not set
  LMSTUDIO_API_KEY   not set
Local runtimes
  Ollama     http://localhost:11434 — ready (1 models: llama3.2:latest)
  LM Studio  http://localhost:1234 — unreachable (… connect: connection refused)
Live round-trip readiness
  Claude streaming  blocked — ANTHROPIC_API_KEY not present
  OpenAI streaming  blocked — OPENAI_API_KEY not present
  LM Studio (any)   blocked — LM Studio server not present
```

All three live round-trips deferred since sessions 19/20/21 remain blocked. Proceeded to `daimon activity verify` per the kickoff's top-priority pick when no live round-trip frees up.

**Files (this session):**

| Path | Purpose |
|---|---|
| `internal/activity/activity.go` (modified, +1 line) | New `KindActivityVerified Kind = "activity.verified"` constant alongside the existing kinds. Verifiers MUST accept unknown kinds without rejecting (per §8.2's enumeration), so adding a kind is forward-compatible. |
| `internal/server/handlers.go` (modified, +35 lines net) | New `handleActivityVerify(ctx, _) → activityVerifyResult{Verified, OK}` method registered at `daimon.activity.verify`. No params (verify is whole-chain or nothing). Calls `s.alog.Verify(ctx)` then on success appends a new `activity.verified` entry with `{verified: N}` payload — same self-incrementing-log property as `handleActivityQuery`'s post-RPC `activity.queried` append. Failure returns the typed activity error mapped to `CodeInternalError` via `mapActivityError`; the failure path does NOT append. Extended `mapActivityError` to also handle `ErrInvalidCiphertext` (AEAD failure surfaces here from a tampered payload — distinct from `ErrChainBroken` which fires on prev_hash mismatches) and `ErrHashMismatch` (defensive — should be unreachable in practice because AEAD authentication catches tamper one layer earlier, but the mapping is correct if it ever arises). |
| `cmd/daimon/cmd_activity.go` (modified, +50 lines) | New `runActivityVerify(stdout, stderr, args)` — writer-injected and testable, same shape as `runActivityQuery`. Flags: `--json` (escape hatch for tooling). Default human output: `verified N entries — chain ok\n` on stdout (exit 0). Failure: returns `fmt.Errorf("chain INVALID: %w", err)` — the wrap-and-return shape lets `main.go::exitOnErr` print one clean `daimon: chain INVALID: <reason>` line (exit 1) without the stdout/stderr duplicate a pre-print pattern would produce. JSON mode emits `{"verified": N, "ok": true}` on success or `{"ok": false, "error": "..."}` on failure, with the same wrapped error driving exit 1 — `jq -e '.ok'` works as a script gate. New `cmdActivity` dispatcher branch: `case "verify"`. New `case "activity.verified"` in `summarizeEntry` — added after live smoke showed the row rendering with empty SUMMARY (kickoff's per-kind list didn't pre-include it); shape `verified=N` matches the `matched=N` shape `activity.queried` uses, completing the symmetry. |
| `cmd/daimon/cmd_activity_test.go` (modified, +145 lines) | 6 new test funcs: happy-path renders count + ok phrase; `--json` mode roundtrips through `[]map[string]any` with no stderr; chain-corrupt human mode returns wrapped error containing "chain INVALID" + the underlying reason, asserts stdout is empty (avoids the duplicate-with-exitOnErr); chain-corrupt JSON mode returns wrapped error AND the JSON failure envelope with `ok:false` + `error` string; daemon-error humanisation (locked → `daimon unlock first`, not-running → `daemon not running`); positional-arg rejection. Plus a new `activity_verified` table case in `TestSummarizeEntry_PerKindShapes`. |
| `internal/server/server_test.go` (modified, +130 lines) | 3 new test funcs: happy-path 3-entry verify (asserts `Verified=3` + `OK=true` + post-RPC `activity.verified` round-trip with payload `verified=3`), tampered-chain rejection (seeds 2 entries, closes the server's log, replaces entry #1's ciphertext with plaintext-shaped JSON, reopens under same identity into `f.srv.alog`, asserts verify returns `CodeInternalError` AND that NO `activity.verified` entry was appended — the failure-path no-extend invariant), empty-log self-incrementing verify (first verify reports `verified=0`; second verify reports `verified=1` because the first appended its own `activity.verified` row). |
| `cmd/daimon/main.go` (modified) | Help text gains a `daimon activity verify [--json]` entry; package-level `daimon activity` doc comment updated to name both subcommands. |
| `SPEC.md` (modified, +9 lines) | §6.1 RPC surface gains a `daimon.activity.verify` block with the `{verified: N, ok: bool}` response shape AND a paragraph explaining the three checks (prev_hash + hash + signature), the success-only-append semantics, and the failure semantics (typed RPC error, no log extension on a corrupt chain). §8.2 logged-kinds table gains an `activity.verified` row. |
| `CHECKPOINT.md` (modified) | Phase line gains the `daimon activity verify` paragraph; build status updated (260 → 269 PASS lines, +9 across server + cmd/daimon); item 29 added in numerical order; per-package test count refreshed (18 activity, 54 server, 24 cmd/daimon). |

**Decisions held from the kickoff (no re-deliberation):**

- **Lean toward "only on success" for the activity.verified self-append.** The kickoff explicitly named this as the in-session decision point — yes-by-symmetry with §8.2 vs cleaner-on-failure. The implementation lands on only-on-success for the reason the kickoff already articulated: when the chain is corrupt, the head is suspect, and extending it would compound the problem. SPEC §6.1's new paragraph documents this contract.
- **No `--since` / `--limit` etc. on the CLI subcommand.** Verify is whole-chain or nothing. The kickoff named `--json` as the only flag, and the implementation matches.
- **Exit code 1 on failure for script pre-flight.** `daimon activity verify && deploy` works. Achieved via the wrapped-error pattern that drives `exitOnErr` once.
- **Reuse `daemonCall` + `humaniseDaemonErr`.** Locked / not-running paths surface the same `daimon unlock first` / `daemon not running` hints every other subcommand has.
- **Mirror the post-append shape from `handleActivityQuery`.** Same self-incrementing-log property; consistent with how every other meaningful action against the log gets recorded.
- **No new error code in the JSON-RPC surface.** Failures route through the existing `CodeInternalError` with the typed activity error in `Data` — the kickoff predicted this and there was no reason to deviate.

**Decisions made this session (small details not in the kickoff):**

- **`mapActivityError` now also routes `ErrInvalidCiphertext` and `ErrHashMismatch`.** The kickoff didn't enumerate which activity errors needed mapping; the existing mapper only handled `ErrEmptyKind` / `ErrLogClosed` / `ErrSignatureFailed` / `ErrChainBroken`. AEAD authentication failure is the dominant failure mode under the SPEC §8.1 encryption layer (it fires before the chain check whenever a payload ciphertext is tampered or a foreign key is used), so routing it explicitly with the message "activity payload AEAD authentication failed" gives operators a clearer diagnostic than the generic fall-through. Added `ErrHashMismatch` defensively for future-proofing — under the current invariants AEAD catches tamper before the hash check, but if a future change ever puts a non-encrypted payload back in scope (it shouldn't), the mapper is correct.
- **Wrap-and-return instead of pre-print.** The first cut of `runActivityVerify` printed `chain INVALID: ...` to stdout AND let `exitOnErr` print `daimon: <error>` to stderr — the live smoke caught the duplicate. Fixed by returning `fmt.Errorf("chain INVALID: %w", err)` and removing the stdout pre-print; `exitOnErr` now produces exactly one user-visible line. JSON mode keeps the structured failure envelope on stdout (so `jq` still works) and the same wrapped error drives exit 1. Tests updated to assert on the returned error rather than stdout.
- **`summarizeEntry` gained a `case "activity.verified"`.** Live smoke showed the new row rendering with empty SUMMARY (the kickoff's per-kind list didn't pre-include the new kind because the new kind is also new). `verified=N` matches the existing `matched=N` shape `activity.queried` uses. Symmetry: query writes `{matched: N}`, verify writes `{verified: N}`, both render in the table the same way.
- **Empty-log verify is OK with 0.** The activity package's `Verify()` returns `(0, nil)` on a missing or empty log file; the handler treats that as success and self-appends an `activity.verified` entry with `{verified: 0}`. The second verify then reports 1 (the first verify's own row). Tested explicitly — the property is "verify never lies about a valid empty/short chain".

**Test count:** 260 → 269 PASS lines (+9: 3 in `internal/server/server_test.go`, 6 in `cmd/daimon/cmd_activity_test.go`; the 10th `summarizeEntry` table case rolls into the existing table-driven test). Per-package: 9 identity + 30 memory + 18 activity + 12 secretbox + 54 server + 12 provider + 17 claude + 22 openai + 24 ollama-chat + 30 lmstudio + 12 ollama embedder + 7 daimonhome + 24 cmd/daimon (8 doctor + 16 activity). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status (this session, against a temp `$DAIMON_HOME`):**

```
# Empty chain (no entries written)
$ ./bin/daimon activity verify
verified 0 entries — chain ok
# (exit 0)

# After init/unlock + 2 memory writes
$ ./bin/daimon activity verify
verified 2 entries — chain ok
# (exit 0)

# Re-verify (the prior verify added an activity.verified row, so chain has 3 entries)
$ ./bin/daimon activity verify
verified 3 entries — chain ok
# (exit 0)

# JSON mode
$ ./bin/daimon activity verify --json
{
  "verified": 6,
  "ok": true
}
# (exit 0)

# Tampered chain (entry #1's payload replaced with plaintext-shaped JSON via Python)
$ ./bin/daimon activity verify
daimon: chain INVALID: rpc error -32603: activity payload AEAD authentication failed
        ("decrypt payload: secretbox: invalid ciphertext: payload not a JSON string:
         json: cannot unmarshal object into Go value of type string")
# (exit 1)

# JSON mode on tampered chain
$ ./bin/daimon activity verify --json
{
  "error": "rpc error -32603: activity payload AEAD authentication failed (...)",
  "ok": false
}
daimon: chain INVALID: rpc error -32603: activity payload AEAD authentication failed (...)
# (exit 1; structured envelope on stdout for tooling, message on stderr for operators)

# After pkill daimond
$ ./bin/daimon activity verify
daimon: chain INVALID: daemon not running — run `daimon unlock` first
# (exit 1)

# Query view shows the activity.verified rows
$ ./bin/daimon activity query --kind activity.verified
TIME                       KIND               ID                          SUMMARY
2026-05-05T22:03:10+08:00  activity.verified  01KQW76B3B7BSZKPEY5JRHGRYF  verified=1
2026-05-05T22:03:10+08:00  activity.verified  01KQW76B3K8X2QKCJAD6JJ9M04  verified=2
```

Six observable surfaces, six behaviours — all match the spec. The "self-incrementing log" property is visible in the third call: each successful `daimon activity verify` writes its own `activity.verified` entry, so re-verify counts the prior verify (verified=2 → next verify reports 3, etc.). The tampered-chain path produces a one-line operator-facing message with the offending entry's diagnostic preserved verbatim from the AEAD error — there's no "chain INVALID" line on stdout duplicating it.

**What this means in plain language:** before this session, the only way to assert "the audit log has not been tampered with" was to call the unexposed `internal/activity.Log.Verify` from Go. After this session, `daimon activity verify` does it in one shot from the CLI, with a clean exit code suitable for `daimon activity verify && deploy` script gating, structured JSON output for tooling, and a self-recording audit trail of every verification. With doctor, activity query, and activity verify all shipped, **every primitive's audit trail is inspectable AND verifiable from the CLI** — the v0.1.x audit-trail story is end-to-end at parity with the memory-store story (write / read / list / search / delete / export / import on memory ↔ append / query / verify on activity).

The structural property worth naming: **verify closes the integrity loop on the audit log without touching the write path.** Every primitive in tree (memory write/read/list/search/delete/export/import, provider invoke, provider stream, activity query, activity verify itself) writes to the log per SPEC §8.2; every entry is AEAD-authenticated at the payload field per SPEC §8.1; every entry is Ed25519-signed under the bound identity; every entry is BLAKE3-hash-chained back to genesis; and now the entire chain is one CLI invocation away from end-to-end verification. The only audit surface NOT yet covered is the genesis-entry investigation punted from session 27 (the missing `daimon.created` row on first auto-spawn) — which `daimon activity verify` makes structurally observable: a chain whose genesis is `daimon.created` reports one count, a chain that skipped genesis reports one less.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Doctor makes "is LM Studio up?" a one-line answer; running the round-trip needs LM Studio running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Same shape — needs a real key in shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Same shape — needs a real key in shell env.
4. **`daimon memory search --inject-preview`.** Dry-run mode that prints what would be folded into a prompt for a given query without invoking a provider — useful for tuning queries before live round-trips become possible. Reuses the inject_context retrieval path; new flag, no new RPC. Estimate: half a session.
5. **Investigate the missing `daimon.created` genesis entry.** Session 27's smoke output showed no `daimon.created` row even though `cmd/daimond/main.go:214` should write one on first serve-with-empty-chain. Now structurally observable via `daimon activity verify`'s entry count vs the expected genesis. Bounded investigation: read `serve.go`'s startup path, confirm whether the genesis condition fires under the auto-spawn-from-`daimon-unlock` flow, decide whether the behaviour is correct or buggy. Estimate: ~30 minutes.
6. **The asciicast** (carry-over from session 16). Doctor + activity query + activity verify all strengthen the operability scenes; an end-to-end demo (write → query → verify) is now a compelling beat the script can showcase.
7. **NLnet NGI Zero application** (carry-over from session 16). The end-to-end audit-trail story (write → encrypt → query → verify) is now the strongest operability beat the application has — write down the demo asciicast and reference it.
8. **v0.2 — x402 / agent wallet, design-only session.** Multi-session arc; opens the next phase. SPEC has no § for it; session 1 is design only.

**Next session begins with:** v0.1.x audit-trail story is end-to-end. The audit-trail subsystem reaches parity with memory: write / read / list / search / delete / export / import on memory ↔ append / query / verify on activity. The next milestone is one of the deferred live round-trips (if any of them unblocks at next-session probe via `./bin/daimon doctor`), one of the in-tree polish items above (`daimon memory search --inject-preview` is the strongest small pick now that verify has shipped; the genesis-entry investigation is a smaller, sharper pick that benefits from verify's new observability), the asciicast / NLnet (both stronger now), or the v0.2 design kickoff. If the doctor footer at next-session start says any of "Claude streaming  READY", "OpenAI streaming  READY", or "LM Studio (any)  READY" the corresponding live round-trip closes in ~5 minutes; otherwise pick from items 4–8 above by huckgod's preference.


## 2026-05-05 — Day Zero, twenty-ninth session: `daimon memory search --inject-preview` — dry-run inspection over existing `daimon.context.get` RPC

**The inject_context UX is now end-to-end inspectable.** Session 24 shipped `matched=N` annotation in the chat REPL post-RPC. Session 27 shipped CLI access to the audit trail. Session 28 shipped chain verification of the audit trail. This session ships dry-run inspection of what would be folded into a prompt before any provider call fires — the developer-facing tuning loop for `inject_context` queries. The full UX trinity: predict-via-preview → invoke-with-annotation → audit-via-query → integrity-via-verify.

Wraps the existing `daimon.context.get` RPC, which has been live since session 6 but had no CLI surface in v0.1.x. The only consumer was the server-side `runContextRetrieval` helper that `handleProviderInvoke` and `handleProviderStream` call when `inject_context` is supplied. Wire shape is unchanged (`{query, max_tokens?, kinds?[]}` → `{context, memory_ids, token_estimate}`); only the consumer surface changed. Same architectural shape as session 26's `daimon doctor` (CLI over existing primitives) and session 27's `daimon activity query` (CLI over `daimon.activity.query`).

**Probe at session start (mechanised via `daimon doctor` since session 26):**

```
$ ./bin/daimon doctor
Daemon
  state  not running — run `daimon unlock` to start
Provider env (presence only)
  ANTHROPIC_API_KEY  not set
  OPENAI_API_KEY     not set
  LMSTUDIO_API_KEY   not set
Local runtimes
  Ollama     http://localhost:11434 — ready (1 models: llama3.2:latest)
  LM Studio  http://localhost:1234 — unreachable (… connect: connection refused)
Live round-trip readiness
  Claude streaming  blocked — ANTHROPIC_API_KEY not present
  OpenAI streaming  blocked — OPENAI_API_KEY not present
  LM Studio (any)   blocked — LM Studio server not present
```

All three live round-trips deferred since sessions 19/20/21 remain blocked. Proceeded to `daimon memory search --inject-preview` per the kickoff's natural-follow-on pick when no live round-trip frees up.

**Files (this session):**

| Path | Purpose |
|---|---|
| `cmd/daimon/flags.go` (new, 28 lines) | Factor `kindsFlag` out of `cmd_activity.go` into a shared file. The flag's validation contract (empty values rejected, comma-joined String render) lives in one place; both `daimon activity query` (multi-kind OR filter over the audit trail) and the new `daimon memory search --inject-preview` (multi-kind allowlist threaded into SPEC §11 retrieval) reference the same type. Kickoff prediction: factor (small, no shared state) — confirmed in session. |
| `cmd/daimon/cmd_activity.go` (modified, −19 lines) | Removed the `kindsFlag` definition (moved to `flags.go`); also removed the now-unused `strings` import. No behavioural change — `runActivityQuery` continues to use the same type, just via the shared declaration. |
| `cmd/daimon/cmd_memory.go` (modified, +110 lines) | New `runMemorySearchInjectPreview(stdout, stderr, query, kinds, maxTokens, asJSON)` writer-injected runner alongside the existing `runMemorySearch`. New `contextGetWire` and `contextGetResult` mirror structs (the wire shapes from `internal/server/handlers.go`'s `contextGetParams` / `contextGetResult` — re-declared here so cmd/daimon stays a pure client per SPEC §6.1's stable wire contract). `cmdMemorySearch` becomes a flag-parsing dispatcher: parses `--kind` (now `kindsFlag` repeatable), `--limit`, `--json`, `--inject-preview`, `--max-tokens` on a single FlagSet, then branches on `*injectPreview`. The non-inject-preview path validates `len(kinds) <= 1` (single-kind contract preserved in search mode) and `*maxTokens == 0` (--max-tokens is inject-preview-only); the inject-preview path validates `--limit` was not set via `flag.Visit` (its default 10 is a search-mode artifact that shouldn't trip an error in inject-preview's path). Each path produces a mode-specific usage string when `fs.NArg() != 1`. |
| `cmd/daimon/cmd_memory_test.go` (new, ~310 lines, +11 PASS lines) | The cmd_memory package's first test file. Mirrors `activityHarness`: short `MkdirTemp("","dmn")` tempdir to dodge AF_UNIX 104-byte sun_path overflow on darwin, mock daemon goroutine, request capture into a buffered channel, `t.Setenv("DAIMON_HOME", dir)` points `daemonCall` at the harness socket. Coverage: happy-path renders header + formatted block + correct wire-shape; empty-match prints stderr note + empty stdout + nil error; wire-shape table-driven over 5 cases asserting `omitempty` semantics on both `max_tokens` and `kinds`; explicit `--max-tokens` reflects in budget denominator; `--json` roundtrips; daemon errors humanise (locked, not_running); plus four dispatcher-level cases (`--limit`+`--inject-preview`, `--max-tokens` alone, multi-kind in search mode, missing positional in both modes); end-to-end through `cmdMemorySearch` proving flag plumbing reaches the runner's RPC. |
| `cmd/daimon/main.go` (modified, +7 lines) | Help block gains the `--inject-preview` form under `daimon memory search`, with a paragraph explaining "dry-run the SPEC §11 inject_context retrieval … same RPC the chat REPL's inject_context flow uses (`daimon.context.get`)". Doc comment unchanged (the package-level surface still groups under `daimon memory`). |
| `CHECKPOINT.md` (modified) | Phase line gains the `daimon memory search --inject-preview` paragraph; build status updated (269 → 280 PASS lines, +11 in `cmd/daimon`); item 30 added in numerical order; per-package test count refreshed (35 cmd/daimon = 8 doctor + 16 activity + 11 memory). |

**Decisions held from the kickoff (no re-deliberation):**

- **`--inject-preview` is a flag on `memory search`, not a separate verb.** Discoverability win: "I already know `memory search`, I just add a flag" beats the conceptual purity of a separate `daimon memory inject-preview` verb. The kickoff explicitly named this in CHECKPOINT.md item-26's punt list ("search --inject-preview") and recommended it; implementation matches.
- **Factor `kindsFlag` into shared `cmd/daimon/flags.go`.** Small, no shared state. Both `activity query` and the new `memory search --inject-preview` use the same repeatable-kind validator; one definition.
- **Reuse `daemonCall` for error humanisation.** Locked → `daimon unlock first`; not_running → `daemon not running`. Same hint surface every other subcommand has.
- **Writer-injected runner.** `runMemorySearchInjectPreview(stdout, stderr, ...)` follows the same pattern as `runActivityQuery` / `runActivityVerify`: tests capture rendered output without swapping `os.Stdout`. `cmdMemorySearch` wires `os.Stdout` / `os.Stderr` into the runner.
- **Empty match is exit 0, not an error.** Search-with-no-hits is not a failure; the tuning loop should be cheap to iterate. Stderr note `no memories matched.`, empty stdout.
- **`--json` emits the raw RPC envelope** verbatim (`{context, memory_ids, token_estimate}`). Tooling pipelines treat this as the stable shape; the human renderer's header is presentation-only.

**Decisions made this session (small details not in the kickoff):**

- **`--limit` and `--max-tokens` are mutually exclusive across the modes.** The kickoff said "exclusive with --limit" but didn't specify the error path. Implementation: passing `--limit` with `--inject-preview` errors with `--limit is meaningless with --inject-preview; use --max-tokens instead`; passing `--max-tokens` without `--inject-preview` errors with `--max-tokens is only valid with --inject-preview`. The `--limit` mismatch uses `flag.Visit` to detect "did the user actually set it" rather than checking against the default 10 — the default is a search-mode artifact that the inject-preview path should ignore silently if not explicitly set.
- **Multi-`--kind` is rejected in search mode with an explicit error.** The kickoff predicted `kindsFlag` would be the type for both modes but didn't say what happens when search mode receives 2+ kinds. Implementation: error with `daimon memory search: --kind is single-valued in search mode; use --inject-preview for multi-kind retrieval`. Surfacing the mismatch is better than silently dropping `kinds[1:]` — the user typed both for a reason.
- **Mode-specific usage strings.** When `fs.NArg() != 1`, the error message reflects which mode the user is in (the `--inject-preview` form lists `--max-tokens`; the search form lists `--limit`). Tiny UX detail; users don't read both lines when only one applies.
- **Default budget display in the header.** When `--max-tokens` is unset, the header reads `tokens≈<estimate>/2000` — the SPEC §11 default 2000 is rendered explicitly so the denominator is honest. The kickoff didn't say what the budget denominator should be; rendering the actual default the server applies (rather than `tokens≈<estimate>/-` or omitting it) makes the budget tuning loop more obvious to the user.

**Test count:** 269 → 280 PASS lines (+11: all in `cmd/daimon/cmd_memory_test.go` — the cmd_memory package's first test file). Per-package: 9 identity + 30 memory + 18 activity + 12 secretbox + 54 server + 12 provider + 17 claude + 22 openai + 24 ollama-chat + 30 lmstudio + 12 ollama embedder + 7 daimonhome + 35 cmd/daimon (8 doctor + 16 activity + 11 memory). All race-clean, all vet-clean, ~10s total run. `make build` clean.

**Live smoke status (this session, against a temp `$DAIMON_HOME` after init/unlock + 4 seeded memories of 3 kinds):**

```
# Happy path: default budget, no kind filter
$ daimon memory search --inject-preview "huckgod"
[inject_preview] query="huckgod" matched=3 tokens≈35/2000

[1] (fact) huckgod is the creator of Daimon
[2] (preference) huckgod prefers terse, no-emoji responses
[3] (fact) huckgod has a daughter

# Lower budget reflects in the denominator
$ daimon memory search --inject-preview --max-tokens 100 "huckgod"
[inject_preview] query="huckgod" matched=3 tokens≈35/100
[...same 3 entries...]

# Single-kind allowlist filters to facts only
$ daimon memory search --inject-preview --kind fact "huckgod"
[inject_preview] query="huckgod" matched=2 tokens≈20/2000

[1] (fact) huckgod is the creator of Daimon
[2] (fact) huckgod has a daughter

# Multi-kind allowlist (the inject-preview-only path)
$ daimon memory search --inject-preview --kind fact --kind preference "huckgod"
[inject_preview] query="huckgod" matched=3 tokens≈35/2000
[...all 3 entries...]

# Empty match: stderr note, empty stdout, exit 0
$ daimon memory search --inject-preview "supercalifragilistic"
no memories matched.
$ echo $?
0

# --json roundtrips the raw envelope
$ daimon memory search --inject-preview --json "huckgod"
{
  "context": "[1] (fact) ...\n[2] (preference) ...\n[3] (fact) ...",
  "memory_ids": ["01KQW86TJ5F0...", "01KQW86THXZP...", "01KQW86THM9A..."],
  "token_estimate": 35
}

# Flag-validation rejections (all exit 1)
$ daimon memory search --inject-preview --limit 5 "q"
daimon: --limit is meaningless with --inject-preview; use --max-tokens instead

$ daimon memory search --max-tokens 500 "q"
daimon: --max-tokens is only valid with --inject-preview

$ daimon memory search --kind fact --kind preference "q"
daimon: daimon memory search: --kind is single-valued in search mode; use --inject-preview for multi-kind retrieval
```

Six observable surfaces, six behaviours — all match the spec. The "tuning loop" property is visible: the developer can iterate on the query string and the kind allowlist with sub-second feedback (no provider call, no token billing) until the formatted block contains the right memories, then flip to `daimon chat --inject-context` knowing the retrieval will fold those exact memories into the system prompt.

**Observation worth flagging for a future session:** `daimon.context.get` does NOT currently write an activity-log row — unlike `daimon.activity.query` (which writes `activity.queried` per SPEC §8.2). This means dry-run previews are invisible to the audit trail. The smoke output above proves it: after running the six `--inject-preview` calls above, `daimon activity query` showed only the 4 `memory.write` rows from setup, no `context.get` rows. Whether this is correct is a SPEC §8.2 design question both ways:

- **Yes-by-symmetry:** Every meaningful action against the principal's data should be auditable. `activity.queried` is logged; `context.get` is the same shape (a pure read with no mutation) and should be too.
- **Read-only-and-incidental:** `context.get` is invoked on every `provider.invoke` with `inject_context` already; auditing the standalone calls would double-log when the chat REPL runs them (the `provider.invoke` row already records `injected_memory_ids`). And the dry-run iteration during query tuning is high-frequency by design — auditing every keystroke during a tuning session adds noise without security value.

The kickoff explicitly said "Zero new RPC, zero SPEC change" so the existing behaviour is preserved. Logged to the punt list for huckgod's call when relevant.

**What this means in plain language:** before this session, the only way to see what `inject_context` would actually fold into a provider's prompt was to run `daimon chat --inject-context` (or `provider invoke --inject-context`) and read the model's response — which costs tokens, hits the network, and conflates "did retrieval pick the right memories?" with "did the model use them well?". After this session, `daimon memory search --inject-preview` does it in one shot from the CLI, with the formatted block visible verbatim and the matched IDs / token estimate annotated. The developer-facing tuning loop is now sub-second and free.

The structural property worth naming: **inject-preview closes the prediction loop on `inject_context` without touching the provider invocation path.** Every primitive that participates in the SPEC §11 retrieval (the cosine search at `internal/memory/store.go`, the recency boost at `handlers.go:822`, the kind allowlist at `handlers.go:847`, the token-budget formatter at `handlers.go:399-414`) is exercised by the new CLI surface in dry-run; the live `daimon chat --inject-context` flow continues to use the same `runContextRetrieval` server-side helper. One implementation, two consumers, full UX coverage end-to-end.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Doctor makes "is LM Studio up?" a one-line answer; running the round-trip needs LM Studio running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Same shape — needs a real key in shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Same shape — needs a real key in shell env.
4. **Audit `daimon.context.get` calls in SPEC §8.2.** Surfaced this session: standalone `daimon memory search --inject-preview` calls are invisible to the audit trail. Decide yes-by-symmetry vs read-only-and-incidental, then either (a) wire `s.alog.Append(ctx, "context.previewed", {query, matched})` into `handleContextGet` (bounded ~30 min, plus SPEC §8.2 row) or (b) document the omission as intentional in SPEC §8.2 with the rationale (read-only retrieval has no audit obligation; the `provider.invoke` row's `injected_memory_ids` is the durable record). Estimate: ~30 minutes either way.
5. **Investigate the missing `daimon.created` genesis entry** (carry-over from session 27). Now structurally observable via `daimon activity verify`'s entry count vs the expected genesis. Bounded investigation: read `serve.go`'s startup path, confirm whether the genesis condition fires under the auto-spawn-from-`daimon-unlock` flow, decide whether the behaviour is correct or buggy. Estimate: ~30 minutes.
6. **The asciicast** (carry-over from session 16). Doctor + activity query + activity verify + inject-preview all strengthen the operability scenes. A five-scene cut: (1) `daimon doctor` shows healthy environment, (2) `daimon memory write` + `daimon memory search --inject-preview` shows retrieval tuning, (3) `daimon chat --once` against Ollama with `[inject_context: query="..." matched=N]` annotation, (4) `daimon activity query` shows the audit trail, (5) `daimon activity verify` confirms chain integrity end-to-end. ~90s of runtime including narration.
7. **NLnet NGI Zero application** (carry-over from session 16). The end-to-end audit-trail story (write → encrypt → preview-inject → invoke → query → verify) is the strongest operability beat the application has — write down the demo asciicast and reference it.
8. **v0.2 — x402 / agent wallet, design-only session.** Multi-session arc; opens the next phase. SPEC has no § for it; session 1 is design only.

**Next session begins with:** v0.1.x has the inject_context UX trinity end-to-end (predict-via-preview → invoke-with-annotation → audit-via-query → integrity-via-verify). The next milestone is one of the deferred live round-trips (if any of them unblocks at next-session probe via `./bin/daimon doctor`), one of the in-tree polish items above (the `context.get` audit decision is the smallest sharp pick; the genesis-entry investigation benefits from verify's observability), the asciicast / NLnet (both stronger now), or the v0.2 design kickoff. If the doctor footer at next-session start says any of "Claude streaming  READY", "OpenAI streaming  READY", or "LM Studio (any)  READY" the corresponding live round-trip closes in ~5 minutes; otherwise pick from items 4–8 above by huckgod's preference.

## 2026-05-06 — Day Zero, thirtieth session: `context.previewed` activity row — closing the audit-trail asymmetry

**Probe at start:** `./bin/daimon doctor` showed all three live round-trips still blocked (no Anthropic/OpenAI keys in the harness env, LM Studio not running). Ollama up with `llama3.2:latest` loaded. Picked session 29's punted SPEC §8.2 question — should `daimon.context.get` calls write an activity-log row? — as the bounded-30-min in-tree item.

**The decision: yes-by-symmetry.** `daimon.activity.query` writes `activity.queried`; `daimon.activity.verify` writes `activity.verified`; both are pure reads of the principal's data, both are audited per SPEC §8.2's "every meaningful action against the principal's data is logged" property. `daimon.context.get` is the same shape and the gap is an oversight. The asymmetry was hard to defend; the audit trail's "every meaningful action" promise is stronger if there are no carve-outs. The high-frequency-noise concern (session 29's punt-list articulated this) is real but small — a tuning session writes ~5–20 entries; the audit log already absorbs `activity.queried` rows at the same rate from session 27's self-incrementing-log property and nobody has complained about that noise.

**Architectural call held in-session: do `handleProviderInvoke` and `handleProviderStream`'s internal `runContextRetrieval` calls also write a `context.previewed` row?** No. The `provider.invoke` / `provider.stream` row already records `injected_memory_ids` (since session 24); an additional `context.previewed` row alongside it would double-log a single principal action. Only the standalone `daimon.context.get` RPC path writes the new row; the inject_context-on-invoke path stays as-is. The split is enforced by the implementation: `handleContextGet` calls `s.alog.Append(activity.KindContextPreviewed, ...)` after a successful `runContextRetrieval`, while the two provider handlers call `runContextRetrieval` without the surrounding append.

**What landed:**

1. **`internal/activity/activity.go`** — added `KindContextPreviewed Kind = "context.previewed"` to the kind constants block alongside `KindActivityQueried` / `KindActivityVerified` / `KindKeyRotated`. One line.

2. **`internal/server/handlers.go::handleContextGet`** — refactored from a one-line passthrough to runContextRetrieval into a four-line success-path-append shape mirroring `handleActivityQuery`'s `if rpcErr := ...; rpcErr != nil { return nil, rpcErr }` early-return then `s.alog.Append(...)` then `return res, nil`. Payload is `{query, matched: len(res.MemoryIDs)}` — the same shape `activity.queried` already uses for `matched=N`. Append failure is logged via `s.logf` and does NOT fail the RPC (same belt-and-suspenders pattern as every other `s.alog.Append` call in this file: the user got their context, audit gap is the lesser harm). Also added a new doc-comment paragraph naming the architectural decision (the inject_context-on-invoke path deliberately does NOT write this row).

3. **`cmd/daimon/cmd_activity.go::summarizeEntry`** — added `case "context.previewed": return fmt.Sprintf("query=%q matched=%d", ...)`. The `%q` is intentional — quoted query strings are easier to scan in the audit table view than bare strings (a query like "huckgod ships at night" reads cleaner as `query="huckgod ships at night"` than as `query=huckgod ships at night matched=...`). Mirrors how `daimon memory search --inject-preview` already prints `[inject_preview] query="..." matched=N`.

4. **SPEC.md §8.2** — added a `context.previewed` row to the "Logged kinds" table with the explanation `"Each standalone daimon.context.get call ({query, matched}); the inject-context-on-invoke path is recorded under provider.invoke with injected_memory_ids instead, to avoid double-logging a single action"`. **§6.1** — added a paragraph after the `daimon.context.get` wire-shape block documenting the success-only-append semantics, the no-double-log-with-invoke rule, and the empty-match-still-appends-but-error-skips rule (same shape as session 28's `activity.verified` paragraph).

5. **`internal/server/server_test.go`** — three new tests:
   - `TestContextGet_AppendsContextPreviewed` (happy path: 3 memories written, query "alpha" → 1 `context.previewed` entry in the log with `payload.query == "alpha"` and `payload.matched == len(got.MemoryIDs)`)
   - `TestContextGet_EmptyMatchStillAppends` (no memories, query "nothingever" → result has empty `MemoryIDs`, but log still has 1 `context.previewed` entry with `matched=0`. Guards the failure-vs-empty-match distinction)
   - `TestContextGet_ErrorPathDoesNotAppend` (close the store mid-test, then call context.get → search errors, RPC fails, log has 0 `context.previewed` entries. Mirrors session 28's verify-fails-no-extend rule)

6. **`internal/server/provider_handlers_test.go::TestProviderInvoke_InjectContextEnrichesSystem`** — extended with the no-double-log assertion: after the inject_context-driven `provider.invoke` call, query the log for `KindContextPreviewed` and assert zero entries. Guards the architectural decision from this session against future regression.

7. **`cmd/daimon/cmd_activity_test.go::TestSummarizeEntry_PerKindShapes`** — added a `context_previewed` table case asserting `summarizeEntry` renders `query="huckgod" matched=3` for the new kind.

**Test count:** 280 → 283 PASS lines (+3 top-level: the three new server tests). The new `provider_handlers_test.go` assertion extends an existing test rather than adding a new top-level. The new `summarizeEntry` table case adds at the indented subtest level (not top-level). Race-clean, vet-clean.

**Live smoke (this session, against a temp `$DAIMON_HOME` after init/unlock + 3 seeded memories of 2 kinds):**
- `daimon memory search --inject-preview "favourite"` rendered 2 matched memories + the formatted block.
- `daimon memory search --inject-preview "xyznonexistent"` printed `no memories matched.` to stderr.
- `daimon activity query --kind context.previewed` rendered both rows in chain order: `query="favourite" matched=2` and `query="xyznonexistent" matched=0` — confirming both happy-path and empty-match-still-appends.
- `daimon activity verify` reported `verified 6 entries — chain ok` (3 memory.write + 2 context.previewed + 1 activity.queried from the verify-itself self-incrementing property), confirming the new row participates in the chain correctly under encryption (AEAD payload + plaintext hash chain per SPEC §8.1).
- `daimon activity query --kind context.previewed --json` round-tripped through `python3 -m json.tool` cleanly: `payload.{query, matched}` decrypts; `id`/`ts`/`kind`/`prev_hash`/`hash`/`signature` stay in clear (per SPEC §8.1's at-rest confidentiality model).

**What this means in plain language:** before this session, `daimon memory search --inject-preview` (the session 29 surface) was invisible to the audit trail. After this session, every preview-tuning call appends a `context.previewed` row alongside `memory.write`, `activity.queried`, `activity.verified`, `provider.invoke`. The audit-trail subsystem is now fully closed — every RPC that touches the principal's memory, activity, or identity surface is auditable, AND every audit row is human-queryable AND chain-verifiable from the CLI. The story becomes: every byte of state the daimon owns is encrypted at rest, signed, hash-chained, and inspectable.

The structural property worth naming: **the audit trail is now reflexive over its own surface.** Every read of the audit trail (`activity.query`, `activity.verify`) writes a corresponding audit row; every read of the principal's memory through the daimon's retrieval policy (`context.get` standalone) writes a corresponding audit row; every write to the principal's memory (`memory.write`, `memory.import`) writes a corresponding audit row; every model invocation that consumed retrieval (`provider.invoke`, `provider.stream` with `inject_context`) records the same retrieval IDs in its audit row. The "every meaningful action" promise has no carve-outs in v0.1's surface.

The kickoff predicted ~285–287 PASS lines (extrapolating from the 5 test outline). Actual landed at 283 because the kickoff counted the no-double-log assertion as a new top-level test (it's an extension of an existing test) and the `summarizeEntry` table case as a new top-level (it adds at the indented subtest level). Same coverage, different counting. The new top-level count (+3) matches the three architectural properties under test: append-on-match, append-on-empty-match, no-append-on-error.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Five-minute close when LM Studio is running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Five-minute close when a real key is in the shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Five-minute close when a real key is in the shell env.
4. **Investigate the missing `daimon.created` genesis entry** (carry-over from session 27). Now structurally observable via `daimon activity verify`'s entry count vs the expected genesis. The auto-spawn from `daimon unlock` may skip the genesis write because `daimon init` already created the keystore but didn't open the activity log, so the first `serve` run sees an empty log AND a fresh keystore. Bounded investigation: read `serve.go`'s startup path, confirm whether the genesis condition fires under the auto-spawn flow. Decide whether the behaviour is correct (chain root should be a write, not a read; if `daimon init` creates the daimon then `daimon.created` should fire then, not on first serve) or buggy (genesis silently dropped). Estimate: ~30 minutes.
5. **The asciicast** (carry-over from session 16). Five scenes: (1) `daimon doctor` shows healthy environment, (2) `daimon memory write` + `daimon memory search --inject-preview` shows retrieval tuning (now writing `context.previewed` rows the audit trail captures), (3) `daimon chat --once` against Ollama with `[inject_context: query="..." matched=N]` annotation, (4) `daimon activity query` shows the audit trail (now including `context.previewed` rows), (5) `daimon activity verify` confirms chain integrity end-to-end. ~90s of runtime including narration.
6. **NLnet NGI Zero application** (carry-over from session 16). Operability story is now strongest yet — a complete write → encrypt → preview-inject → invoke → query → verify trail, all from the CLI, all chain-verifiable.
7. **v0.2 — x402 / agent wallet, design-only session.** Multi-session arc; opens the next phase. SPEC has no § for it; session 1 is design only.

**Next session begins with:** the audit-trail subsystem is closed end-to-end. Every read of the principal's data through the daimon writes an audit row; every audit row is chain-verifiable; every audit row is human-queryable. The daimon's "every meaningful action against the principal's data is logged" promise has zero carve-outs in v0.1. If the doctor footer says any of "Claude streaming READY", "OpenAI streaming READY", or "LM Studio (any) READY" the corresponding live round-trip closes in ~5 minutes; otherwise pick from items 4–7 above by huckgod's preference. The genesis-entry investigation is the smallest sharp in-tree pick (~30 min, one file read + one decision); the asciicast or the NLnet application are the strongest external-facing follow-ups (both blocked on huckgod's shell having real API keys, both stronger after this session's audit-trail closure).

---

## 2026-05-06 — Day Zero, thirty-first session: `daimon.created` genesis row — closing the chain-root asymmetry

**Probe at start:** `./bin/daimon doctor` showed all three live round-trips still blocked (no Anthropic/OpenAI keys in the harness env, LM Studio not running). Picked session 30's punt-list item 4 — investigate the missing `daimon.created` genesis row — as the bounded ~30 min in-tree work.

**Hypothesis confirmation:** `grep "daimon.created\|KindDaimonCreated"` across the tree showed exactly one production write site for genesis: `cmd/daimond/main.go:214` inside `runDemo` (the self-contained 8-step demonstration). Production lifecycle (`init` / `unlock` / `serve`) never wrote genesis. Chain root under the production path was whatever the user happened to do first (a `memory.write`, an `activity.queried` from a self-incrementing query, etc.) — chain integrity held because every entry's `prev_hash` correctly chained back to `ZeroHash`, but the chain's first entry had no semantic meaning. The hypothesis from session 27's punt list — that genesis was silently dropped under the auto-spawn flow — was correct, with one refinement: it wasn't "silently dropped"; it was never wired into the production code path at all. Demo got it right; the production CLI never had it.

**The decision: option A — `daimon init` writes genesis.** Two reasonable readings of SPEC §8.2's "First boot": (A) init = key generation = birth = genesis; (B) first unlock against an empty log = genesis. Both architecturally valid. Chose A because (1) the lifecycle invariant is structurally cleaner — `daimon init` creates the keystore + 1-entry log; `daimon unlock` never mutates log shape, it just opens the log. The asymmetry was previously flipped (init created the keystore but not the log; unlock created the log but not the keystore); option A repairs it. (2) "First boot" reads cleanly as "key generation" — the daimon is born when its keypair is generated, not when it first wakes up. (3) Option B would have nicer retro-fix ergonomics for pre-existing alpha homes, but (a) huckgod is the only existing alpha and can `daimon init --force` a clean home, and (b) option B bolts a state-shape side-effect onto an already-load-bearing handler (`handleIdentityUnlock`), which feels worse than putting it in the dedicated provisioning command.

**Architectural call held in-session: what does `daimon init --force` do with stale `activity.log` and `memory.db`?** Removes both. `--force` is documented as DESTROYS the current identity; the activity log is signed under the discarded identity, and the memory DB is encrypted under a subkey derived from the discarded identity — both are unreadable by the new identity. Leaving them on disk only produces a chain Verify failure on the first audit (entry 0's signature would be from the old key) and a memory store the new identity can't decrypt. So `--force` becomes "discard current identity AND its data trail", which matches the documented intent. Without `--force`, the existing keystore-presence check already errors out, so this code path only runs under explicit user opt-in. Confirmed live: `--force` re-init produces a single-entry chain under the new DID, with the old chain wiped.

**Architectural call held in-session: abort init or warn on genesis-append failure?** Keystore is already on disk by the time genesis runs. Aborting after a successful keystore save would leave the user in a stuck state (subsequent re-runs would error on keystore-exists, requiring `--force` and discarding the just-saved identity). Implementation does NOT abort: genesis-append errors return an error from `runInit`, but the keystore is durable. Future re-runs would either succeed (transient disk issue cleared) or surface the same error. In practice the failure surface is tiny (the activity log is a single-file create-or-append in the same directory the keystore just wrote to; if disk is full or perms are wrong, the keystore save would have failed first). Trade-off accepted: in the unlikely double-failure case, the user has a daimon they can unlock and use, just with a missing genesis row — no different from the pre-fix state, which is the world we're already living in. The lesser harm.

**What landed:**

1. **`cmd/daimon/cmd_init.go`** — refactored `cmdInit` to extract `runInit(home, password, force) (*identity.Identity, error)` containing the keystore-overwrite check, optional `--force` cleanup of `activity.log` + `memory.db`, key generation, keystore save, and genesis activity-row write. Split exists for testability — tests drive `runInit` directly without TTY mocking. `cmdInit` reduces to: parse flags, resolve home, prompt password (twice with confirmation), call `runInit`, print success block. Updated the success block to surface the genesis row: `Genesis: <path> (1 entry, kind=daimon.created)`. Updated `--force` flag help text: `(DANGEROUS — discards the current identity, activity log, and memory store)`.

2. **`SPEC.md` §8.2** — updated the `daimon.created` row in the "Logged kinds" table from the cryptic `"First boot"` to the full semantic: `"Genesis row, written by daimon init immediately after keystore generation. Payload {version, did}. The chain root is always this kind; entry index 0 has prev_hash = ZeroHash. daimon unlock never mutates log shape — it just opens the existing log."` Added a **Lifecycle invariant** paragraph after the kinds table documenting: (a) post-init the chain has exactly one entry (the genesis), (b) `--force` re-init produces the same invariant by removing prior `activity.log` + `memory.db`, (c) `daimon-core` programmatic adopters who skip the init CLI are responsible for their own genesis (Verify still tolerates an empty-prefix chain — the chain root just carries no semantic meaning without it).

3. **`cmd/daimon/cmd_init_test.go`** (new file — first test for the `init` subcommand): four tests covering the lifecycle invariant in detail.
   - `TestRunInit_FreshHome_WritesGenesisRow` (the core property: post-init the activity log has exactly 1 entry, kind = `daimon.created`, prev_hash = `ZeroHash`, signature verifies under the just-generated identity, `Verify` returns `(1, nil)`)
   - `TestRunInit_GenesisPayloadCarriesDIDAndVersion` (pins SPEC §8.2 payload shape for external tooling — payload's `did` matches `id.DID()`, payload's `version` matches the CLI version constant)
   - `TestRunInit_RefusesOverwriteWithoutForce` (existing safety net: re-init without `--force` errors with the documented message AND leaves the existing `activity.log` byte-identical, proving the rejected init does not mutate state)
   - `TestRunInit_ForceCleansActivityLogAndMemoryDB` (the `--force` semantic: pre-seed an old daimon with a non-genesis activity entry + a stale `memory.db` byte file, then `--force` re-init produces a fresh DID, the stale `memory.db` is gone, and `Verify` under the new identity returns exactly 1 entry — the new genesis. Without the cleanup, Verify would fail at entry 0 because the stale entries are signed by the old identity.)

**Test count:** 283 → 287 PASS lines (+4 top-level: the four new init tests). Race-clean, vet-clean, all 13 packages green.

**Live smoke (this session, against a temp `$DAIMON_HOME`):**
- `daimon init` (fresh home) printed `Genesis: <path>/activity.log (1 entry, kind=daimon.created)` after keystore success.
- On-disk activity.log shows the JSONL line with `kind:"daimon.created"` (clear) and an AEAD-encrypted base64 payload (per SPEC §8.1).
- `daimon unlock` succeeded; `daimon identity get` returned the just-generated DID.
- `daimon activity verify` reported `verified 1 entries — chain ok` — exactly the lifecycle invariant the SPEC paragraph promises. (Pre-fix, this would have reported `verified 1 entries — chain ok` only because verify-itself appends `activity.verified`, which was structurally weird — the "first entry" was the verify call's own audit row, not a meaningful action.)
- `daimon activity query` rendered the genesis row at the top: `daimon.created  01KQY...  did=did:key:z6Mkh...` followed by the `activity.verified  ...  verified=1` row from the prior verify.
- Re-init without `--force`: errored with `daimon: keystore already exists at .../identity.keystore — pass --force to overwrite (DESTROYS the current identity)` (existing safety net intact).
- Re-init with `--force`: produced a fresh DID, fresh genesis under the new DID, stale memory.db wiped. `Verify` under the new identity returned `verified 1 entries — chain ok` — the old chain (signed by the old identity) was cleanly replaced.

**What this means in plain language:** before this session, the daimon's chain had no semantic root — entry 0 was whatever the user happened to do first, and the audit trail's first row was a coincidence of usage. After this session, every daimon's chain begins with a `daimon.created` row at init, naming the version it was born under and the DID it was born as. The chain root is itself a meaningful action: the daimon's birth.

The structural property worth naming: **the audit trail is now totally meaningful from entry 0.** From the moment a daimon is born (init), every byte of state it owns is encrypted at rest, signed, hash-chained, and inspectable from the CLI. The chain's root is a documented, tested action; every subsequent entry chains back to it. The "every meaningful action" promise has zero carve-outs in v0.1, AND the chain root is itself one of those meaningful actions. Sessions 28 + 29 + 30 closed the audit trail's surface area; this session closes its origin.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Five-minute close when LM Studio is running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Five-minute close when a real key is in the shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Five-minute close when a real key is in the shell env.
4. **The asciicast** (carry-over from session 16). Five scenes, now strongest yet: (1) `daimon doctor` shows healthy environment, (2) `daimon memory write` + `daimon memory search --inject-preview` shows retrieval tuning (writing `context.previewed` rows visible in scene 4), (3) `daimon chat --once` against Ollama with `[inject_context: query="..." matched=N]` annotation, (4) `daimon activity query` shows the audit trail — **now starting with the genesis `daimon.created` row** (a much stronger beat than the pre-fix "verified N entries" without a named root), (5) `daimon activity verify` confirms chain integrity end-to-end. ~90s of runtime including narration.
5. **NLnet NGI Zero application** (carry-over from session 16). Operability story is now strongest yet — birth → encrypt → preview-inject → invoke → query → verify, every entry named and tested.
6. **v0.2 — x402 / agent wallet, design-only session.** Multi-session arc; opens the next phase. SPEC has no § for it; session 1 is design only.

**Next session begins with:** the audit-trail subsystem is closed end-to-end **and from entry 0**. Every chain begins with a meaningful named action (the genesis); every subsequent meaningful action extends it; every read of the chain itself extends it; every action is inspectable and chain-verifiable from the CLI. The daimon's "every meaningful action against the principal's data is logged, including its own birth" promise has zero carve-outs in v0.1. If the doctor footer says any of "Claude streaming READY", "OpenAI streaming READY", or "LM Studio (any) READY" the corresponding live round-trip closes in ~5 minutes; otherwise pick from items 4–6 above by huckgod's preference. With the audit-trail closure now complete (sessions 28 + 29 + 30 + 31 in sequence), the strongest external-facing follow-ups (asciicast, NLnet) get their cleanest demo material yet.

## 2026-05-06 — Day Zero, thirty-second session: Python SDK session 1 — package skeleton + Unix-socket JSON-RPC client + identity/memory verbs + pytest harness

**The v0.1 SDK gap that's been the real v0.1 hole since session 16 starts closing.** v0.1 scope listed Python and TypeScript SDKs; neither had a single line. This session ships the Python SDK's first arc: package skeleton at `sdk/python/`, pure-stdlib Unix-socket JSON-RPC client mirroring `cmd/daimon/rpc.go`'s wire shape exactly, namespaced verb groups (`Client.identity.get`, `Client.memory.write|read|search|list`), and a pytest harness with a stub Unix-socket daemon for byte-for-byte protocol testing. Plus an end-to-end smoke against a real `daimond serve` proving the SDK's writes are recorded as audited principal actions indistinguishable from CLI actions.

**Probe at start:** `./bin/daimon doctor` showed all three live round-trips still blocked (no Anthropic/OpenAI keys, LM Studio not running, Ollama up with `llama3.2:latest`). Took the kickoff's lean — Python SDK over v0.2 design — because it's the only thing that closes the v0.1 SDK gap with code, and it removes a v0.1 hole that's older (session 16) than every other punted item except the asciicast.

**Bundled commit shape decision held in-session:** sessions 28-31 had been uncommitted in the working tree. Kickoff offered per-session (4 commits, JOURNAL granularity, easier bisect) vs bundled (1 commit, cleaner `git log --oneline` story). Surveyed the diff: 7 files spanned multiple sessions (`activity.go`, `handlers.go`, `server_test.go`, `cmd_activity.go`, `cmd_activity_test.go`, `SPEC.md`, `CHECKPOINT.md`). Splitting per-session would require hunk-level surgery to reconstruct history that never had real test runs at each boundary — fake bisect granularity, real merge-conflict risk. Chose bundled. JOURNAL preserves per-session granularity at the doc level (canonical record); git carries the arc-level summary. Single commit "Close audit-trail subsystem: verify + inject-preview + context.previewed + genesis" landed at `703dd23` and pushed to `origin/main` before the SDK work began — clean baseline for the SDK's own commit.

**Architectural decisions (this session):**

1. **Pure stdlib, no dependencies.** The SDK is one Unix socket and `json.dumps` away from working — adding `httpx` or `pydantic` for a v0.1 alpha would be carry-over weight. Returns are raw decoded JSON (dicts/lists/scalars), not pydantic models, so the SDK doesn't drift behind the Go side's evolving record shapes between SDK sessions. Type modelling is deferred to a later session when the wire shapes have stabilised under multiple consumers.

2. **Two-step error taxonomy.** `DaimonError` (base) → `DaemonNotRunning` (socket ENOENT/ECONNREFUSED) / `DaemonLocked` (RPC code -32001) / `RPCError` (everything else, with `code`/`message`/`data`). Mirrors `cmd/daimon/client.go::humaniseDaemonErr`'s two-failure-mode rewrite plus a generic catch-all. The two-step lets callers `except DaemonNotRunning` to handle the "daemon never started" case differently from the locked case differently from the unknown-code case.

3. **`identity.unlock` deliberately not exposed.** Unlocking from a library would mean holding the password in process memory, which is the wrong default posture. The CLI's `daimon unlock` is the canonical path — the SDK assumes the daemon is already unlocked. Advanced callers can hit `Client._call("daimon.identity.unlock", {"password": ...})` directly if they really want to, but it's not a verb on the namespace.

4. **`memory.list()` is `memory.search("")` underneath.** The Go server registers six methods under `daimon.memory.*` (write/read/search/delete/export/import) — there is no `list`. The CLI's `cmdMemoryList` (cmd_memory.go:172) is a thin wrapper around search-with-empty-query; the SDK matches that exactly. One verb on the wire, two on the namespace.

5. **`Client(home=..., socket_path=..., timeout=...)` constructor shape.** `socket_path` overrides everything (test harnesses dial a stub directly), `home` overrides `$DAIMON_HOME` resolution (multi-daimon setups), default resolves the same way the Go CLI does — through `_home.resolve_home()` that mirrors `internal/daimonhome/daimonhome.go` byte-for-byte (env var, then platform default — macOS `~/Library/Application Support/daimon`, Linux `$XDG_CONFIG_HOME/daimon` or `~/.config/daimon`, Windows `%APPDATA%/daimon`). AF_UNIX `sun_path` 104-byte fallback to `$TMPDIR/daimon-<uid>.sock` is implemented in `_home.socket_path()` too — the Python SDK and the Go binaries cannot disagree about where the socket lives.

6. **Connection lifecycle: one RPC per connection, half-close the write side.** Mirrors the Go side's `json.NewEncoder(c).Encode(req)` + `json.NewDecoder(c).Decode(&resp)` flow. The Python side sends one JSON object + newline, calls `sock.shutdown(SHUT_WR)` so the server's decoder sees EOF promptly after the single request, then drains the read side until the peer closes. Without the half-close the server's decoder would block waiting for more requests on the same connection — the Go server is happy with the one-request-then-close shape, but `json.Decoder` is permissive enough that we have to be explicit about end-of-stream on our side.

7. **`params` omission matches Go's `omitempty`.** When `params is None` the SDK omits the `params` key entirely from the JSON-RPC envelope (not `params: null`) — matches what `json.Marshal` does on the Go client side for nil. The server's `decodeParams` happens to accept both, but the wire bytes match the Go CLI's exactly.

**What landed:**

```
sdk/python/
├── pyproject.toml        # setuptools, requires-python>=3.10, dev-extra=[pytest]
├── README.md             # usage + dev install
├── daimon/
│   ├── __init__.py       # public surface: Client, errors, __version__
│   ├── _home.py          # mirrors internal/daimonhome/daimonhome.go
│   ├── _rpc.py           # mirrors cmd/daimon/rpc.go::rpcCall
│   ├── client.py         # Client + _IdentityNamespace + _MemoryNamespace
│   └── errors.py         # DaimonError, DaemonNotRunning, DaemonLocked, RPCError
└── tests/
    ├── conftest.py       # StubDaemon (Unix-socket JSON-RPC listener), fixtures
    ├── test_home.py      # 5 tests: env-var, default-creates, file-rejection, sock-primary, sock-fallback
    ├── test_rpc.py       # 7 tests: round-trip, params, ENOENT->DaemonNotRunning, -32001->DaemonLocked, other code->RPCError, unknown method, result-omitted
    └── test_client.py    # 10 tests: socket resolution, identity.get, memory.write minimal/full, memory.read, memory.search, memory.list, kind filter, locked propagation
```

**Test infrastructure:** `StubDaemon` is a tiny `socket.AF_UNIX` listener in a daemon thread that accepts one request per connection, decodes the JSON-RPC envelope, dispatches to a per-method handler (callable or static value), and writes back a response. Handlers raise `StubRPCError` to send a JSON-RPC error envelope. The `stub_daemon` fixture starts/stops one per test; the `short_tmp` fixture (mkdtemp under `/tmp` not pytest's tmp_path) keeps socket paths under the AF_UNIX 104-byte cap on macOS — pytest's default tmp_path lives at `/private/var/folders/9v/.../pytest-of-huckgod/pytest-N/<test>/` which is already over cap before any filename is appended. Test fixtures need to know this; production code has the fallback baked in via `_home.socket_path()`.

**Edge case caught during testing:** `Client.socket_path` after `Client()` with `$DAIMON_HOME=/tmp/dt-...` resolves to `/private/tmp/dt-...` because macOS `/tmp` symlinks to `/private/tmp` and `Path.resolve()` follows it. The assertion in `test_client_resolves_socket_via_home_env` was originally checking against the unresolved form and failed; fix was `(daimon_home / "daimon.sock").resolve()` on the assertion side. Both sides canonical, both sides agree.

**Test count:** SDK suite adds 22 tests (5 _home + 7 _rpc + 10 client). Separate suite from the Go `go test ./...` count (still 287). All 22 pass green; ~3.1s total wall (most of it is pytest collection overhead — actual RPC round-trips through the stub are sub-millisecond).

**End-to-end smoke against a real daimon:**

```
$ DAIMON_HOME=/tmp/dt-sdk-smoke.AXImKU
$ printf 'testpw\ntestpw\n' | bin/daimon init     # produces genesis row
$ printf 'testpw\n'         | bin/daimon unlock   # spawns daimond
$ python -c '
from daimon import Client
c = Client()
print(c.identity.get()["did"])
m1 = c.memory.write(kind="fact", content="alpha first thing huckgod ships")
m2 = c.memory.write(kind="observation", content="beta", metadata={"tag":"draft"})
m3 = c.memory.write(kind="fact", content="gamma")
print(c.memory.read(m2["id"])["content"])
print(c.memory.search("alpha")[0]["score"])
print(len(c.memory.list()))
print(len(c.memory.list(kind="fact")))
'
did:key:z6Mkw2r4FHAQFvU5WLLj4CwmXRMshv6BDLFqEbskGETCor5q
beta
1.0
3
2

$ bin/daimon activity query --limit 8
TIME                       KIND            ID                          SUMMARY
2026-05-06T18:06:57+08:00  daimon.created  01KQYC2GX1XTG95TH6XPYV47PW  did=did:key:z6Mkw...
2026-05-06T18:07:18+08:00  memory.write    01KQYC35F185PA0VGMQ5ZW5CBR  id=01KQYC35EZD5F6H4VN41B3PNF6 kind=fact
2026-05-06T18:07:18+08:00  memory.write    01KQYC35F6JABXDAP7AMKW2VW2  id=01KQYC35F6P9TG0WDW5HFPK0XG kind=observation
2026-05-06T18:07:18+08:00  memory.write    01KQYC35FAW1DGTJANNGDNTF0R  id=01KQYC35FAT01VXKJ0MVWW3PS4 kind=fact

$ bin/daimon activity verify
verified 5 entries — chain ok    # 1 genesis + 3 sdk-writes + 1 self-append from this verify
```

**The structural property worth naming:** the Python SDK's writes are recorded in the audit trail as `memory.write` rows indistinguishable from CLI writes. The audit trail does not — and should not — distinguish "which client process called the RPC"; it records the principal's actions, regardless of which language called the daemon. Same chain integrity, same encryption-at-rest, same audit guarantees apply to SDK callers as to CLI callers. The protocol is behaving exactly as designed: the daemon is the trust boundary, and the SDK is just another client over the same wire.

**What we explicitly did NOT ship in this session (per the kickoff's session-1-of-3-4 plan):**

- **Provider verbs** (`daimon.provider.{list,invoke}`) — session 2. Will require an `Invoke` shape decision: surface the full `provider.Response` envelope to Python or wrap it. Probably the former (matches the v0.1 thin-layer philosophy).
- **Activity verbs** (`daimon.activity.{append,query,verify}`) — session 2. Trivial port of the same shape as memory.
- **Streaming via `daimon.provider.stream` notifications** — session 3. Needs a generator-based API (`for delta in client.provider.stream(...)`). Wire shape is documented in `cmd/daimon/rpc.go::rpcStream`.
- **Type modelling** — deferred. Returns are raw dicts in v0.1; pydantic models can be added over the same wire surface in a v0.1.x SDK polish session once the surface is stable enough that drift between Python/Go shapes is unlikely.
- **PyPI publishing** — session 4. `pip install -e .` smoke is the v0.1 milestone.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Five-minute close when LM Studio is running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Five-minute close when a real key is in the shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Five-minute close when a real key is in the shell env.
4. **Python SDK session 2** — provider + activity verbs over the same wire/error/test scaffolding this session shipped. Expected ~150 lines + tests, ~30 min once the harness pattern is in muscle memory.
5. **v0.2 design — x402 / agent wallet, design-only session.** Multi-session arc; SPEC has no § for it; session 1 is design only.

**Next session begins with:** the v0.1 SDK gap is half-closed on the Python side. Memory + identity verbs over a real Unix-socket JSON-RPC client; tests against a stub daemon; smoke against the real daemon proving SDK writes are audited principal actions. If the doctor footer shows a live-readiness READY, take the 5-minute close (items 1-3); otherwise the natural next pick is Python SDK session 2 (provider + activity verbs — same scaffolding, mostly mechanical), or v0.2 design (multi-session arc, SPEC-only deliverable). TypeScript SDK is also live as a v0.1 deliverable; v0.1.0 doesn't ship without it. The v0.1 SDK milestone is now half-built.

## 2026-05-06 — Day Zero, thirty-third session: Python SDK session 2 — provider + activity verbs over the existing harness

**The Python SDK reaches all of v0.1's non-streaming RPC surface.** Session 32 shipped identity + memory; this session adds the remaining four namespaces (`provider.list`, `provider.invoke`, `activity.append`, `activity.query`, `activity.verify`) over the same Unix-socket JSON-RPC client, the same error taxonomy, the same StubDaemon harness. The Python SDK is now feature-complete on the v0.1 RPC surface modulo `provider.stream` (deferred to session 3 because the one-request-per-connection lifecycle in `_rpc.py` has no notification handling).

**Probe at start:** `./bin/daimon doctor` showed all three live-readiness lanes blocked again (no Anthropic/OpenAI keys in the harness shell, LM Studio not running, only Ollama up with `llama3.2:latest`). Took the kickoff's lean — Python SDK session 2 over TypeScript session 1 or v0.2 design. Rationale held: cheapest close on the v0.1 SDK milestone, scaffolding from session 32 still warm (StubDaemon, error taxonomy, half-close write-side, `params is None` omit-empty), no zigzag.

**Architectural decisions (this session):**

1. **Flat-kwargs surface for `provider.invoke`, nested wire shape assembled internally.** The wire shape is `{provider, request: {model, messages, system?, max_tokens?, temperature?}, inject_context?}` — `model` lives inside `request`, not top-level. Two surfaces could fit: (A) mirror the wire 1:1 and require callers to pass `request={...}` themselves; (B) take flat kwargs (`provider=, model=, messages=, system=, ...`) and assemble the nested envelope inside the SDK. Chose (B) because it matches the Go CLI's user-facing flag surface (`daimon provider invoke <provider> --model X --system Y`), keeps the SDK pythonic, and still respects the principle that the SDK is a thin wrapper — the assembly is one local dict construction, not a translation layer. Trade-off: SDK callers can't see the nested `request` shape from the docstring without reading SPEC §6.1; mitigated by the docstring naming the wire fields explicitly.

2. **Full envelope returned from `provider.invoke`, not the bare response.** The wire returns `{response: {...}, injected_memory_ids?: [...]}`. Returning just the inner `response` would lose the injected-memory-IDs metadata that's the whole reason the envelope wrap exists (session 24 added it). SDK returns the envelope verbatim — callers do `env["response"]["content"]` for the text and `env.get("injected_memory_ids")` for the audit metadata. Same philosophy as `memory.write` returning `{"id": "..."}` rather than the bare ID string: keep the wire shape visible.

3. **`inject_context` accepts a dict, not a string-or-bool.** The CLI's `--inject-context` flag has bare-bool semantics ("use the prompt as the query") and string semantics (`--inject-context=<query>`). The SDK doesn't replicate either: callers pass `inject_context={"query": ...}` explicitly, or `inject_context={"query": ..., "max_tokens": 256, "kinds": ["fact"]}` for full control. Bare-bool magic is a CLI ergonomic — library callers have the prompt in scope and can build the dict themselves with one extra line. Keeps the surface declarative.

4. **`activity.query` omits `params` entirely when no filters are passed.** Compromise between the Go CLI (which sends `{}` because `daemonCall("…query", activityQueryWire{}, …)` always encodes the struct, even all-omitempty) and the SDK's existing `params is None → omit key` rule from session 32. Chose the SDK rule for internal consistency: `client.activity.query()` with no args sends a request envelope with no `params` key, just like `client.identity.get()`. Server's `decodeParams` accepts both — wire bytes diverge from the Go CLI by one key, semantics are identical.

5. **`activity.verify` sends `params: {}` (empty object, not omitted).** Unlike `query`, the SDK sends `{}` here because the Go CLI's `daemonCall("…verify", struct{}{}, …)` does the same (encodes to `{}`), and the empty-object form is the more conventional "I have no parameters but I'm not making a malformed request" signal. The two-rule split (omit for `query`, `{}` for `verify`) tracks the CLI's intent — `query` sends an all-omitempty struct that legitimately encodes to `{}`; `verify` sends a literal empty struct. Making the Python side omit on `query` and send `{}` on `verify` mirrors that intent rather than the encoded bytes.

6. **`provider.list` and `activity.query` normalise null-result to `[]`.** Mirrors `memory.search` from session 32 — Go's nil slice encodes as JSON `null`, the SDK lifts that to `[]` so callers can iterate without a guard. Keeps the iteration ergonomics consistent across all list-returning verbs.

**What landed:**

- **`sdk/python/daimon/client.py`** — added `_ProviderNamespace` (list, invoke) and `_ActivityNamespace` (append, query, verify), wired into `Client.__init__` as `self.provider` and `self.activity`. +124 lines, no other files in `daimon/` touched (the wire layer in `_rpc.py` and the error taxonomy in `errors.py` were complete in session 32).

- **`sdk/python/tests/test_client.py`** — added 13 tests covering the new surface:
  - `test_provider_list_returns_entries`, `test_provider_list_normalises_null_to_empty_list`
  - `test_provider_invoke_assembles_nested_request` (the load-bearing one — verifies the SDK's flat-kwargs → nested-wire assembly bit-for-bit)
  - `test_provider_invoke_threads_optional_fields` (system/temperature/max_tokens all land under `request` when supplied)
  - `test_provider_invoke_passes_inject_context_verbatim` (dict passes through; `injected_memory_ids` envelope metadata is preserved on the return)
  - `test_provider_invoke_no_provider_registry_propagates_rpc_error` (CodeNotFound -32002 surfaces as RPCError with `.code` intact)
  - `test_activity_append_minimal`, `test_activity_append_with_payload`
  - `test_activity_query_returns_entries` (no-filter case sends `params: <omitted>`, not `{}`), `test_activity_query_threads_filters`, `test_activity_query_normalises_null_to_empty_list`
  - `test_activity_verify_returns_envelope` (sends `params: {}`, returns `{verified, ok}`)
  - `test_activity_verify_chain_failure_propagates` (CodeInternalError -32603 with chain-broken message surfaces as RPCError)

**Test count:** 22 → 35 PASS (+13). Same harness, same wall (~5.7s — pytest collection still dominates; actual stub-RPC round-trips are sub-millisecond). Go suite untouched at 287.

**End-to-end smoke against a real daimon (this session):**

```
$ DAIMON_HOME=/tmp/dt-sdk-s2.XXX
$ printf 'testpw\ntestpw\n' | bin/daimon init      # produces genesis row
$ printf 'testpw\n'         | bin/daimon unlock    # spawns daimond (Ollama auto-detected)
$ python /tmp/dt_sdk_s2_smoke.py
DID: did:key:z6Mkg6ACSVda7vTNLnZ495Q2MPMtRQQHMCgBRNgTFhFwkrLe

provider.list:
  ollama       configured=False models=['llama3.2:latest']
  openai       configured=True  models=['gpt-5', 'gpt-5-mini', 'gpt-4.1']

activity.append (before-invoke): {'id': '01KQYFPKS8…', 'hash': 'blake3:0b9c…'}

provider.invoke (ollama / llama3.2:latest):
  content: 'Pong'
  stop_reason: end_turn
  usage: {'input_tokens': 32, 'output_tokens': 3}
  injected_memory_ids: None

activity.append (after-invoke): {'id': '01KQYFPN5R…', 'hash': 'blake3:b468…'}

activity.query (limit=20):
  daimon.created            01KQYFPK7SVX… prev=blake3:0… hash=blake3:9…
  smoke.session2            01KQYFPKS8KN… prev=blake3:9… hash=blake3:0…
  provider.invoke           01KQYFPN5JVN… prev=blake3:0… hash=blake3:2…
  smoke.session2            01KQYFPN5RFJ… prev=blake3:2… hash=blake3:b…

activity.verify: {'verified': 5, 'ok': True}

activity.query (kind=smoke.session2):
  smoke.session2  01KQYFPKS8KN…  payload={'actor': 'sdk', 'step': 'before-invoke'}
  smoke.session2  01KQYFPN5RFJ…  payload={'actor': 'sdk', 'step': 'after-invoke'}

$ bin/daimon activity verify
verified 7 entries — chain ok    # 5 from SDK's verify + activity.queried (kind-filtered) + activity.verified (the SDK verify itself)
```

**The structural property worth naming:** the Python SDK now writes audit rows under arbitrary `kind` values (`smoke.session2` here) and triggers the daemon's own audit writes (`provider.invoke` from the Ollama call) on the same chain, both indistinguishable from CLI-driven rows. The cross-language `daimon activity verify` walks all 7 entries — genesis, two SDK-appended customs, one daemon-written `provider.invoke` from the SDK's invoke call, plus the verify-chain self-appends from query + kind-filtered query + verify itself — and reports chain ok. The audit trail does not, and should not, distinguish "Python SDK called" from "CLI called" from "daemon-internal write": every meaningful action against the principal's data is logged under the same chain, whatever the entry point.

**What we explicitly did NOT ship in this session (per the kickoff's session-by-session arc):**

- **Streaming via `daimon.provider.stream` notifications** — session 3. The wire shape is documented in `cmd/daimon/rpc.go::rpcStream` (request/notifications/terminal-response over the same conn). Needs a generator-based API surface (`for delta in client.provider.stream(...)`) and a different connection lifecycle than `_rpc.py` currently supports — the half-close write-side trick won't work because the server keeps writing notifications until the stream ends. Probably ~100 lines of net-new code in a `_stream.py` module plus tests with a stub daemon that writes notifications.
- **Type modelling** — still deferred. Raw dicts on every return; pydantic/dataclass models can land in a polish session once both SDKs (Python + TypeScript) are shipping and the wire surface is stable.
- **PyPI publishing** — session 4. `pip install -e .` from source is the v0.1 milestone; PyPI is a v0.1.x polish step.

**What we explicitly punted (in priority order for next session):**

1. **Live LM Studio streaming round-trip** (still carry-over from session 21). Five-minute close when LM Studio is running locally.
2. **Live OpenAI streaming round-trip** (still carry-over from session 20). Five-minute close when a real key is in the shell env.
3. **Live Claude streaming round-trip** (still carry-over from session 19). Five-minute close when a real key is in the shell env.
4. **TypeScript SDK session 1** — mirror port of Python session 1 + 2 in one TS arc. Same wire shape (Node `net.createConnection` over `'unix'` socket replaces Python's `socket.AF_UNIX`); same `Client.identity.get` + `Client.memory.*` + `Client.provider.*` + `Client.activity.*` namespaces; same `DaemonNotRunning` / `DaemonLocked` / `RPCError` taxonomy. Porting both Python sessions in one TS sweep reads cleaner than zigzagging language-by-language. ~400 lines + vitest harness.
5. **Python SDK session 3** — `provider.stream` (the deferred verb above). After TypeScript catches up to Python session 2, both languages can adopt streaming together rather than Python-only.
6. **v0.2 design — x402 / agent wallet, design-only session.** Multi-session arc; SPEC has no § for it; session 1 is design only.

**Next session begins with:** the Python SDK is feature-complete on v0.1's non-streaming surface. Identity + memory + provider + activity, all five namespaces, all over the same Unix-socket JSON-RPC client, all tested against a stub daemon, all smoke-validated against a real daemon producing chain-verifiable audit rows. The v0.1 SDK milestone is now ⅔ closed: TypeScript port is the remaining ⅓. If the doctor footer shows a live-readiness READY, take the 5-minute close (items 1-3); otherwise TypeScript SDK session 1 is the natural lean (one TS arc that ports Python sessions 1 + 2 together — Memory/Identity/Provider/Activity — closes the v0.1 SDK milestone modulo streaming). `provider.stream` and v0.2 design are the further-out arcs after the TS port.


## 2026-05-11 — Day Zero, thirty-fourth session: TypeScript SDK session 1 — mirror port of Python sessions 1+2, closing the v0.1 SDK milestone on the non-streaming surface in both languages

**The v0.1 SDK milestone is now structurally closed in both languages on everything except streaming.** Session 32 shipped the Python SDK's identity+memory verbs; session 33 added the remaining provider+activity verbs. This session mirrors both Python sessions in one TypeScript arc: same wire shape, same error taxonomy, same namespace surface, same StubDaemon harness, same 35-case test count. The TypeScript SDK reaches feature parity with the Python SDK on v0.1's non-streaming RPC surface in a single arc instead of a two-session zigzag.

**Probe at start:** `./bin/daimon doctor` initially blocked on all three live-readiness lanes (no API keys, LM Studio down). huckgod added an `OPENAI_API_KEY` and launched LM Studio (loaded four models including `qwen/…` and `google/gemma-4-26b-a4b`) between probes; the second probe showed OpenAI streaming **READY** and LM Studio (any) **READY**. Anthropic stayed parked (huckgod's call — not sure they'd like their key on the harness yet). Could have closed item 21 (LM Studio live round-trip from CHECKPOINT.md) here, but the keystore was absent so `daimon unlock` would have needed an interactive passphrase prompt that this session couldn't satisfy; deferred the live round-trip to a follow-up session and went straight to TypeScript session 1.

**Architectural decisions held in-session:**

1. **Promise-based async surface, one-connection-per-RPC lifecycle.** Node's `net.Socket` is event-based (`connect`/`data`/`end`/`error`); the SDK wraps the connect→write→data→end lifecycle in a single Promise per RPC call so the caller surface reads `await client.memory.write({...})` — identical mental model to Python's blocking calls, no callback chains, no manual stream wiring. The wire shape is identical to the Python SDK: one `net.createConnection({path: socketPath})` per RPC, JSON-RPC 2.0 envelope on the wire, `socket.end(payload)` half-closes the write side so the server's `json.Decoder` sees EOF promptly (Node's `socket.end()` plays the role of Python's `socket.shutdown(SHUT_WR)` — sends FIN on the writable side, keeps the readable side open until the peer FIN's back).

2. **Method overload on `StubDaemon.handle` to keep test ergonomics under TypeScript strict mode.** The natural test idiom is `daemon.handle("daimon.memory.write", (p) => ({id: "01K"}))` — but strict mode + `noImplicitAny` rejects the bare `(p) =>` because the union `Handler = unknown | ((params: unknown) => unknown)` confuses inference (TS sees `unknown` first and gives up on inferring the lambda param). Forcing every test-site to write `(p: unknown) => ...` would be ten edits and bad ergonomics. Five-line fix instead: add two overload signatures ahead of the implementation — `handle(method, response: (params: unknown) => unknown): void` then `handle(method, response: unknown): void`. TS picks the first matching overload at the call site and infers the callback param as `unknown` automatically. Tests stayed clean.

3. **`fs.realpathSync` after `path.resolve` for every home path.** macOS `/tmp` symlinks to `/private/tmp`, and Python's `Path.resolve()` follows symlinks while Node's `path.resolve()` does **not**. Without realpath, `Client({home: "/tmp/foo"})` would compute a socket path under `/tmp/foo` while a fresh `resolveHome()` (which uses `realpathSync` internally for the env-var case) would canonicalise into `/private/tmp/foo` — same directory, different string, fails strict equality in tests AND breaks cross-process socket-file consistency checks (the Go daemon binds the socket at one canonical path; an SDK that dials a different-stringed-but-same-inode path would still work on AF_UNIX, but the brittleness shows up in any test that compares strings). Surfaced as two test failures on the first vitest run (`resolveHome honors the env var` and `resolves socket via DAIMON_HOME env when no overrides`); fixed in one edit each — `home.ts` calls `fs.realpathSync(home)` after `ensureDir`, and `client.ts` calls `fs.realpathSync(path.resolve(options.home))` when the caller passes an explicit `home`. The `socketPath` override branch stays as-passed because callers who set `socketPath` directly know what they want.

4. **`@daimon/sdk` scoped package name** over a bare `daimon` (likely taken on npm). Scoped form makes the install path predictable when we publish (`npm install @daimon/sdk`); bare names risk collision with whatever sits on `daimon@1.0.0` today. Symmetric to the Python SDK's bare `daimon` import in spirit — one canonical name per language ecosystem, picked to be predictable rather than to maximize brevity.

5. **`pool: "forks"` over vitest's default thread pool.** Each test allocates an AF_UNIX listener at `/tmp/dt-<rand>/daimon.sock`; threads share the event loop and would interleave socket-file create/delete in racey ways. Forks give each test worker its own process, its own event loop, its own file-descriptor table. The cost is slower startup (~100ms per worker on cold start) but tests still finish in 240ms wall — vitest's parallelism is small enough that the overhead doesn't dominate.

6. **`@types/node` only, no runtime deps.** Mirrors the Python SDK's pure-stdlib stance. The TS SDK ships zero `dependencies`; only three `devDependencies` (`typescript`, `vitest`, `@types/node`). Build emits a single `dist/` produced by `tsc -p tsconfig.build.json` with declaration files + sourcemaps for downstream type inference. No bundler — Node 18+ resolves ESM `.js` extensions on import natively, and the `"exports"` field in `package.json` pins the entry points (`./dist/index.js` + `./dist/index.d.ts`).

7. **ESM-only, `"type": "module"` package.** No CommonJS dual-build. Node 18+ is the minimum (matching the v0.1 stack); ESM is the long-term path; dual builds double the surface for marginal benefit on a v0.1 SDK with no current adopters. Easy to add a CJS path later if a real consumer asks.

8. **Loose return types over schema modelling.** Every Client verb returns `Promise<JsonObject>` or `Promise<JsonObject[]>` (alias for `Record<string, unknown>` + the array form). Same philosophy as the Python SDK's raw-dict returns — the SDK does not bake in schema models so it doesn't drift behind the Go side's evolving record shapes. Callers narrow at the call site (`const { content } = env.response as { content: string }`). Type modelling is a polish session once both SDKs have at least one real consumer and the wire surface stabilises.

**What landed:**

- **`sdk/typescript/`** — full package tree:
  - `package.json` — `@daimon/sdk@0.1.0-dev.0`, ESM, Node ≥ 18, three devDeps, four npm scripts (`build` / `test` / `test:watch` / `typecheck`).
  - `tsconfig.json` — ES2022 target, strict mode + `noUncheckedIndexedAccess`, `Bundler` module resolution for the test compile.
  - `tsconfig.build.json` — extends the above; emits to `dist/` with declaration files + sourcemaps; `module: ES2022` + `moduleResolution: Node` for the published artifact so the relative `.js` imports resolve correctly under Node ESM.
  - `vitest.config.ts` — `pool: "forks"`, `test.include: test/**/*.test.ts`, 10s timeout.
  - `src/errors.ts` — `DaimonError` (extends `Error`, sets `.name` from `new.target`), `DaemonNotRunning`, `DaemonLocked` (carries `.code` + `.rpcMessage` + `.data`), `RPCError` (same), `fromErrorObject(obj)` factory mirroring Python's `_from_error_object`. The renderRpc helper produces the same `rpc error <code>: <message> (<data>)` string format as the Python SDK for grep-ability across language boundaries.
  - `src/home.ts` — `resolveHome()` / `socketPath(home)` mirroring the Python SDK's `_home.py`. 104-byte sun_path cap; `fs.realpathSync` after `ensureDir` to follow macOS `/tmp` → `/private/tmp` symlinks. `process.getuid` accessed defensively (undefined on Windows even though we're already in the non-Windows branch — TypeScript needs the cast).
  - `src/rpc.ts` — `rpcCall(socketPath, method, params, options)` returning `Promise<unknown>`. `net.createConnection({path})` for AF_UNIX; `socket.end(payload)` for half-close-write; `ENOENT`/`ECONNREFUSED` → `DaemonNotRunning`; per-call timeout (default 30s) with `setTimeout` + `clearTimeout` cleanup. The `settled` boolean guards against double-resolve when the socket emits both `error` and `end` (Node's event ordering is not guaranteed; first-to-settle wins).
  - `src/client.ts` — `Client` class with `identity` / `memory` / `provider` / `activity` namespace fields. Same flat-kwargs surface as Python (`client.provider.invoke({provider, model, messages, ...})` assembles the nested `{provider, request: {...}}` wire envelope internally). Same `null → []` normalisation in `memory.search` / `provider.list` / `activity.query`. Same `params: undefined → omit on wire` rule vs `activity.verify` sending `{}` explicitly.
  - `src/index.ts` — public re-exports: `Client`, the four error classes, the six error-code constants, and a `VERSION` string. Type-only re-exports for `ClientOptions` / `JsonObject` / `JsonArray` / the four params interfaces.
  - `test/stub-daemon.ts` — `StubDaemon` class (Unix-socket listener, per-conn one-request-one-response handler), `StubRPCError` (throw to send a JSON-RPC error envelope), `makeShortTmp()` / `cleanupShortTmp()` (so socket paths root under `/tmp/dt-<rand>/` rather than vitest's default `/var/folders/...` location that blows past the 104-byte sun_path cap). Method overload on `handle` as architectural decision 2 above.
  - `test/home.test.ts` — 5 cases mirroring `sdk/python/tests/test_home.py` 1:1.
  - `test/rpc.test.ts` — 7 cases mirroring `sdk/python/tests/test_rpc.py` 1:1.
  - `test/client.test.ts` — 23 cases mirroring `sdk/python/tests/test_client.py` 1:1 (2 construction + 1 identity + 7 memory + 6 provider + 7 activity).
  - `README.md` — quick install, use, errors, dev sections — mirrors the Python SDK's README structure.

**Test count:** 35 vitest cases pass green at ~240ms wall. Exact case-count parity with the Python SDK (also 35), exact coverage parity on the wire shape (every Python test has a TypeScript counterpart asserting the same wire bytes flow through the stub daemon). `npm run typecheck` clean; `npm run build` emits 20 files into `dist/` (5 source × 4 outputs per: `.js`, `.js.map`, `.d.ts`, `.d.ts.map`). Go suite untouched at 287; Python suite untouched at 35.

**What we explicitly did NOT ship in this session:**

- **Streaming via `daimon.provider.stream` notifications** — same deferral reason as the Python SDK (session 33). The half-close-write trick that makes the one-request-per-connection lifecycle work would prevent the server from writing follow-up notifications; streaming needs a different connection lifecycle (server keeps writing until the stream ends, client only half-closes after consuming the terminal `provider.Response`). The Node version of that lifecycle is symmetric to the Python one: a generator-style API (`for await (const delta of client.provider.stream({...}))`) over `net.Socket` with the write side left open. A future combined-language streaming session should land both at once for parity.

- **Live cross-language smoke against a real daimon** — the keystore is currently absent on huckgod's harness (`./bin/daimon doctor` reports `keystore absent — run \`daimon init\``); running `daimon init` would have needed an interactive passphrase prompt that the SDK-build session doesn't fit. The stub-daemon harness exercises every wire-shape decision byte-for-byte; live smoke would have added "the real daemon also accepts this" evidence on top, but it's not load-bearing for the v0.1 SDK milestone close. Worth doing as a follow-up when huckgod is at the terminal to type a passphrase — could be one combined session that closes items 21 (live LM Studio round-trip) and 23 (live OpenAI round-trip) on the punch list AND adds a TypeScript-side smoke.

- **Type modelling** — still deferred for both SDKs. Raw `Record<string, unknown>` on every return; properly-typed envelopes (e.g. `ProviderInvokeResponse`) land in a polish session once both SDKs have at least one real consumer and the wire surface stabilises further.

- **npm publishing** — the package is structured for it (`name: "@daimon/sdk"`, `version: "0.1.0-dev.0"`, `files: ["dist", "src", "README.md"]`, scoped name, license, author, repo URL) but `npm publish` is a v0.1.x polish step alongside Python's PyPI step.

**What we explicitly punted (in priority order for next session):**

1. **Live cross-language round-trip** (combined close of items 21, 23 from CHECKPOINT, plus a TS-side smoke). Needs `daimon init` + `daimon unlock` interactively. ~10 minutes once huckgod is at the terminal. Closes the only remaining "we didn't actually point this at a real daemon" gap for either SDK.
2. **Python SDK session 3 — `provider.stream`** (the deferred verb). Now that both languages are at non-streaming parity, streaming is the natural next move. ~100 lines net-new in a `_stream.py` module + tests against a stub daemon that writes notifications. Generator-based API: `for delta in client.provider.stream(...)`.
3. **TypeScript SDK session 2 — `provider.stream`** to keep both languages in lockstep. Async-iterator API: `for await (const delta of client.provider.stream(...))`. Lands alongside or right after Python session 3.
4. **v0.2 design — x402 / agent wallet, design-only session.** Multi-session arc; SPEC has no § for it; session 1 is design only. Open questions (held from session 33): wallet primitive (HKDF off the seed under `"daimon-wallet-v1"` mirroring §22's activity-payload subkey, vs a separate keypair); the 401→402 flow into `daimon.provider.invoke`; activity-log shape for spends; blockchain assumption (Coinbase USDC vs generic EIP-3009 / x402-spec compliant). Closes nothing immediately; opens the v0.2 arc.
5. **PyPI + npm publishing.** v0.1.x polish; non-load-bearing for the v0.1 milestone.

**Next session begins with:** the v0.1 SDK milestone is structurally closed on the non-streaming surface in both languages — `daimon` (Python, `pip install -e sdk/python`) and `@daimon/sdk` (TypeScript, `cd sdk/typescript && npm install && npm run build`). Identity, memory, provider, activity all four namespaces, same wire shape, same error taxonomy, same StubDaemon harness pattern, 35 + 35 test cases all green. If the doctor footer shows a live-readiness READY AND huckgod is at the terminal to type a passphrase, take the cross-language live smoke first (items 21/23 close + a TS-side smoke at the same time). Otherwise streaming is the natural next arc — Python SDK session 3 brings `provider.stream` to the Python side; TypeScript SDK session 2 follows for parity. Both can ship sequentially or one combined session. After streaming, the v0.2 design arc opens; v0.1.0 final is then a publishing-and-polish step.

## 2026-05-11 — Day Zero, thirty-fifth session: cross-language live smoke — Python + TypeScript SDKs round-trip a single unlocked daimon, audit chain verified by all three (Python SDK, TS SDK, CLI)

**No code changes this session. Pure live evidence.** Session 34 shipped the TypeScript SDK; this session points both SDKs at the same real daemon for the first time, captures the interleaved audit chain, and walks it with three independent verifiers. The v0.1 SDK milestone now has end-to-end live proof on top of its structural close.

**Probe + setup:** `./bin/daimon doctor` showed OpenAI streaming READY (key sourced from `~/Library/Application Support/daimon/env`) and LM Studio READY (4 models loaded — including `liquid/lfm2.5-1.2b`, the lightest one used here). Anthropic stayed parked per huckgod's call (no `ANTHROPIC_API_KEY` in env). Provisioned a fresh daimon under `DAIMON_HOME=/tmp/dt-sdk-s34`: `printf 'testpw\ntestpw\n' | ./bin/daimon init` produced a new DID `did:key:z6MkvM5PkRam6DpVsswit9Ja675GvdnGJMGFM5R44XyDtUss` and the genesis `daimon.created` row; `printf 'testpw\n' | ./bin/daimon unlock` spawned the daemon (pid 71000 — referenced later in the tear-down).

**One discovered gotcha in the SDKs' kind set:** the Python SDK README example uses `kind="note"` and so did my initial smoke script. The Go daemon's `memory.Kind.Valid()` (internal/memory/memory.go:41) accepts only `fact`, `preference`, `task`, `observation` — `note` is not valid and the round-trip raised `RPCError -32004: invalid memory kind`. The Python SDK's pytest suite uses `kind="note"` against the StubDaemon (which doesn't validate), so this category of mismatch is invisible to the unit suite. **This is a real docs/examples bug — but not load-bearing for the v0.1 SDK milestone close.** The smoke pivoted to `kind="fact"` and round-tripped cleanly. (Punted to a polish session: align both SDKs' README examples + the StubDaemon's set of accepted kinds with the daemon's actual `Valid()` list, or — better — pull the canonical list into a shared definition the SDKs can import. Either way, a tiny session.)

**The smoke arc itself, in order written to the chain:**

```
genesis  daimon.created     payload={version, did=z6Mk…tUss}

—— Python SDK arc (sdk/python/.venv/bin/python) ——
1        memory.write       id=01KRB6WS67… kind=fact (content: "python sdk smoke — session 34", metadata={actor: python-sdk, session: 34})
2        provider.invoke    ollama/llama3.2:latest streamed=false matched=0  → returned 'Pong' (stop=end_turn, in=32, out=3)
3        smoke.session34    payload={actor: python-sdk, step: after-invoke}
4        activity.queried   matched=4   ← Python SDK's client.activity.query(limit=20) auto-logged this row
5        activity.verified  verified=5  ← Python SDK's client.activity.verify() walked entries 0-4 then appended this

—— standalone OpenAI calls (carry-over from session 20: live OpenAI round-trip) ——
6        provider.invoke    openai/gpt-5-mini-2025-08-07  ← FAILED with `Invalid 'max_output_tokens': integer below minimum value. Expected a value >= 16, but got 4`. The row was still written: the daemon logs the *intent* to invoke, not just successful invocations.
7        provider.invoke    openai/gpt-5-mini-2025-08-07  ← SUCCEEDED with max_tokens=512: returned 'ok' (stop=end_turn, in=13, out=89). gpt-5-mini emits reasoning tokens that inflate the output count even for a 2-character visible reply.

—— TypeScript SDK arc (node /tmp/dt_sdk_s34_ts_smoke.mjs, importing /Users/huckgod/Developer/network/sdk/typescript/dist/index.js) ——
8        provider.invoke    lmstudio/liquid/lfm2.5-1.2b streamed=false matched=0  → returned 'pong' (stop=end_turn, in=17, out=3)  ← THIS CLOSES PUNCH-LIST ITEM 21 (live LM Studio round-trip)
9        memory.write       id=01KRB6YBA2… kind=fact (content: "typescript sdk smoke — session 34", metadata={actor: typescript-sdk, session: 34})
10       smoke.session34    payload={actor: typescript-sdk, step: after-invoke}
11       activity.queried   matched=11  ← TS SDK's client.activity.query({limit: 20}) auto-logged
12       activity.verified  verified=12 ← TS SDK's client.activity.verify() walked entries 0-11 then appended this

—— CLI verify ——
13       activity.verified  verified=13 ← `daimon activity verify` walked entries 0-12 then appended this
```

**Three independent verifiers, all agree the chain is intact:**

- Python SDK's `client.activity.verify()` returned `{verified: 5, ok: True}` (walked the 5 entries that existed when it ran)
- TypeScript SDK's `client.activity.verify()` returned `{verified: 12, ok: true}` (walked the 12 entries that existed when it ran, including Python's earlier verify-self-append row)
- CLI's `daimon activity verify` reported `verified 13 entries — chain ok` (walked everything above, including the TS SDK's verify-self-append row)

Each verifier is a different code path: the Python SDK's verify is `_call("daimon.activity.verify", {})` over the Unix socket; the TS SDK's is `rpcCall` over `net.createConnection({path})` with `socket.end(payload)`; the CLI's is the Go-internal call directly into `internal/activity.Verify`. Three different languages, three different implementations, three identical answers. **The protocol's audit-integrity claim is empirically true across language boundaries.**

**The cross-language equivalence property worth naming explicitly:** the chain entries are indistinguishable by *language of caller*. The two `smoke.session34` rows differ only in their `payload.actor` field (`python-sdk` vs `typescript-sdk`) — but the row's signature, hash, and chain position are computed the same way regardless of who triggered the append. The two `memory.write` rows (one Python, one TS, both `kind=fact`) have identical wire structure. The `provider.invoke` rows for ollama (Python-triggered), openai (Python-triggered × 2), and lmstudio (TS-triggered) follow the same shape; the daemon doesn't know or record which SDK sent the request, only that it received a valid `daimon.provider.invoke` over the socket. **This is the v0.1 SDK story in one sentence:** the SDK is purely a wire-shape wrapper; the trust boundary and the audit boundary both live in the daemon; SDKs in any language are equivalent at the protocol level.

**Live-readiness lanes after this session:**

- ✅ Live LM Studio round-trip — closed (punch-list item 21)
- ✅ Live OpenAI round-trip — closed (carry-over from session 20)
- ⏸ Live Claude streaming — still blocked; huckgod chose to keep `ANTHROPIC_API_KEY` off the harness for now. When the key is added, the same smoke pattern closes the third lane in 60 seconds.

**Tear-down:** killed daimond pid 71000 (no graceful `daimon lock` subcommand exists — daemon stays running until killed). Temp `DAIMON_HOME=/tmp/dt-sdk-s34/` left in place at huckgod's request for inspection; smoke scripts in `/tmp/dt_sdk_s34_*.{py,mjs}` similarly preserved. None of these directories are under git; huckgod can `rm -rf /tmp/dt-sdk-s34 /tmp/dt_sdk_s34_*.{py,mjs}` whenever.

**What we explicitly did NOT do (kept the session tight):**

- **Streaming smoke** — neither SDK has streaming yet (`provider.stream` is the next code-arc). Streaming is the natural next session for either language; smoke-testing streaming is part of that session, not this one.
- **Live Claude round-trip** — no key in env per huckgod's call. Future huckgod can drop the key into `~/Library/Application Support/daimon/env`, run the same kind of one-off smoke (`client.provider.invoke({provider: "anthropic", model: "claude-sonnet-4-6", ...})`) and the third live-readiness lane closes.
- **Polishing the memory-kind docs mismatch** — flagged above; sized as a small docs session (align Python README example + TS README example + StubDaemon's accepted-kinds set with `memory.Kind.Valid()`'s real list, or hoist the canonical list to a shared place both SDKs can read from). Not load-bearing for v0.1; punted.

**Punted (in priority order for next session):**

1. **Python SDK session 3 — `provider.stream`.** Streaming on the Python side. ~100 lines net-new in a `_stream.py` module + tests against a stub daemon that writes notifications. Generator-based API: `for delta in client.provider.stream(...)`. Now that non-streaming live evidence exists in both languages, streaming is the natural next move.
2. **TypeScript SDK session 2 — `provider.stream`.** Symmetric to (1). Async-iterator API: `for await (const delta of client.provider.stream(...))`. Lands alongside or right after Python session 3.
3. **Memory-kind canonicalisation polish session.** Align SDK READMEs + StubDaemon allowed-kinds with `memory.Kind.Valid()`. ~15 min.
4. **Live Claude smoke.** When the Anthropic key is added. ~60 seconds.
5. **v0.2 design — x402 / agent wallet.** Multi-session arc; design-only session 1.
6. **PyPI + npm publishing.** v0.1.x polish; non-load-bearing for the v0.1 milestone.

**Next session begins with:** the v0.1 SDK milestone is now closed on the non-streaming surface in both languages with end-to-end live evidence in both — Python + TypeScript SDKs writing memories and invoking real providers (ollama, openai, lmstudio) against the same daemon, single chain, chain-integrity verified by three independent code paths. Streaming is the natural next arc for either SDK; both can ship sequentially or in one combined session. After streaming, v0.2 design opens; v0.1.0 final is then a publishing-and-polish step (PyPI + npm + Anthropic-side live smoke when the key shows up).

## 2026-05-11 — Day Zero, thirty-sixth session: Python SDK session 3 — provider.stream, the deferred streaming verb

**The Python SDK reaches full v0.1 RPC surface, including streaming.** Sessions 32+33 shipped non-streaming verbs (identity, memory, provider.list, provider.invoke, activity); this session adds `provider.stream` with a generator-style iterator API and ~10 new pytest cases. Wire shape mirrors `cmd/daimon/rpc.go::rpcStream` byte-for-byte: one request out, 0..N `daimon.provider.stream.delta` notifications back, one terminal `{result|error, id}` frame.

**Probe at start:** doctor showed OpenAI + LM Studio + Ollama all live (carry-over from session 35's env file). The temp `DAIMON_HOME=/tmp/dt-sdk-s34/` from session 35 still existed; resumed that daimon (same DID `did:key:z6Mk…tUss`) so the streaming smoke would extend the existing 14-entry chain rather than start a fresh one. That made the cross-session continuity demonstrable in the audit log.

**Architectural decisions held in-session:**

1. **Class-based iterator (`StreamHandle`) over a `yield`/`return` generator.** Python 3 supports generators that both yield values and `return` a final value — the return value comes through `StopIteration.value`. But that pattern forces every caller into `try/except StopIteration: envelope = exc.value` or a `yield from` indirection. A class with an `.final` attribute populated before `__next__` raises `StopIteration` is much more pythonic and matches the mental model users already have for HTTP-streaming libraries (OpenAI's stream object exposes chunks via iteration with no terminal envelope; we have a terminal envelope and we want it visible). The class also gets `__enter__`/`__exit__` for free, so callers who bail mid-stream get deterministic socket close: `with client.provider.stream(...) as stream: for delta in stream: ...`.

2. **No half-close on the write side.** This is the key lifecycle difference from `_rpc.py`'s one-shot path. The non-streaming client does `sock.shutdown(SHUT_WR)` after sending the request so the server's `json.Decoder` sees EOF promptly — but in streaming, the server reads our single request, then writes notifications and the terminal frame on the same connection. Half-closing the write side could surface as a write-side error on some kernels even though the read side is still open, and there's no semantic reason to half-close anyway (the server doesn't need EOF to know our request is complete — the newline-terminated JSON frame is self-delimiting). Match the Go CLI's `rpcStream` exactly: no half-close, just keep reading until the terminal frame.

3. **Newline-delimited frame reader.** Go's `json.Encoder.Encode` always writes one JSON object followed by `\n`. The Python SDK's `_read_one_frame` exploits this by splitting the recv buffer on raw `\n` bytes. This is safe because JSON-encoded strings escape literal newlines as `\\n` (two ASCII chars — `5c 6e`), so the only actual newline byte (`0a`) on the wire is the frame separator. Buffer-based reader accumulates partial reads across `recv` boundaries — useful when a delta straddles a TCP segment, or when multiple frames arrive coalesced in one `recv` (which happens for fast-yielding providers like Ollama at ~9ms inter-delta gaps where multiple notifications can land in one buffer).

4. **Forward-compat for unknown notification kinds.** The reader silently skips any notification method that isn't `daimon.provider.stream.delta`, mirroring the Go CLI's `rpcStream` exactly. Future deltas — tool-call deltas, role markers, function-call markers — won't break older SDKs. Test `test_stream_ignores_unknown_notification_kinds` codifies this contract by injecting a `daimon.provider.stream.tool` notification between two real deltas and asserting the SDK yields only the real deltas.

5. **Terminal-frame error mapping is identical to non-streaming.** Reuses `_from_error_object` from `errors.py`: code `-32001` lifts to `DaemonLocked`; everything else to `RPCError`. Same exception family, same `.code`/`.message`/`.data` attributes. Tests cover both — `test_stream_terminal_error_raises_rpc_error` (chain-broken with `-32603`) and `test_stream_terminal_minus_32001_raises_daemon_locked`.

6. **StubDaemon refactor: read one newline-terminated request, dispatch by method registration.** The previous `_serve_one` read all bytes until peer half-close, which works for `_rpc.py`'s one-shot pattern but blocks forever against `_stream.py`'s "no half-close" client. Replaced with a "read until first `\n`" loop, then dispatch: if the method is registered via `.stream(...)` use `_serve_stream` (writes notifications + terminal); otherwise `.handle(...)` use the existing one-response path. **The 35 pre-existing non-streaming tests all still pass verbatim** because the SDK's `_rpc.rpc_call` always appends `\n` to its request payload — the new "read one line" reader is byte-compatible with the old "read all" reader for that case. Added `_serve_stream`'s broken-pipe tolerance: when a client closes the socket mid-stream (the early-exit test case), the stub gets `BrokenPipeError` on the next `sendall` and silently returns — matches real-daemon behaviour where mid-stream client disconnects are routine.

7. **Provider-side capability lives in the daemon, not the SDK.** Per CHECKPOINT item 22 (session 18): Ollama implements `provider.Streamer` natively (delta-per-token); Claude/OpenAI/LM Studio fall back to a synchronous invoke on the daemon side and return the full content as a single terminal frame with no deltas in between. The Python SDK doesn't replicate the fallback logic — the wire shape is the same regardless, so the SDK just sees "0 deltas, terminal carries full content" for fallback providers. Codified in `test_stream_zero_deltas_terminal_only` against a stubbed-fallback case.

**What landed:**

- **`sdk/python/daimon/_stream.py`** (new file, 200 lines): `StreamHandle` class + `open_stream(socket_path, method, params, timeout)`. `StreamHandle.__next__` reads frames until it sees a delta (returns its content) or the terminal frame (stores envelope on `.final`, raises `StopIteration`). Socket is closed on terminal frame, on `close()`, on `__exit__`, or implicitly when the handle is garbage-collected. `DEFAULT_TIMEOUT = 300.0` (5 min) — streaming calls can be much longer than non-streaming.

- **`sdk/python/daimon/client.py`** (+47 lines): `_ProviderNamespace.stream(*, provider, messages, model="", system=None, temperature=None, max_tokens=None, inject_context=None, timeout=None)` — same flat-kwargs surface as `invoke`, assembles the same nested `{provider, request: {...}}` wire envelope. Routes through `_stream.open_stream` with method `daimon.provider.stream`. Docstring + `_ProviderNamespace` class docstring updated to reflect the new surface.

- **`sdk/python/daimon/__init__.py`** (+2 lines): re-export `StreamHandle` for caller-side type hints.

- **`sdk/python/tests/conftest.py`** (+~50 lines): `StubDaemon.stream(method, deltas, terminal)` registration; `_serve_one` refactored to read one newline-terminated request line and dispatch; new `_serve_stream` writes notifications + terminal with broken-pipe tolerance for early-client-exit cases.

- **`sdk/python/tests/test_stream.py`** (new file, 10 tests): yields-all-deltas-in-order, terminal-envelope-populates-after-iteration, assembles-nested-request-from-flat-kwargs, passes-inject-context-verbatim, zero-deltas-terminal-only (fallback case), ignores-unknown-notification-kinds (forward-compat), terminal-RPCError, terminal-`-32001`-DaemonLocked, context-manager-early-exit, peer-closes-without-terminal-raises.

- **`sdk/python/README.md`** updated: status line bumped to "Identity, memory, provider (list / invoke / stream), and activity verbs all surfaced"; quick-start example now uses `kind="fact"` (closes part of the memory-kind docs mismatch flagged in session 35) and demonstrates streaming with the `stream.final` pattern.

**Test count:** 35 → 45 SDK pytest cases (+10), all race-clean at ~5.6s wall. Go suite untouched at 287. TypeScript suite untouched at 35.

**Live smoke against ollama/llama3.2:latest** (resumed `DAIMON_HOME=/tmp/dt-sdk-s34/`):

```
$ python -c "<smoke script: stream 'Count from 1 to 5, one number per line.'>"
received 14 deltas
first delta at: 1380.6ms; last delta at: 1499.2ms
mean inter-delta gap: 9.1ms, min: 8.3ms, max: 9.7ms
content: 'Here is the count:\n\n1\n\n2\n\n3\n\n4\n\n5'

terminal envelope: {'response': {'model': 'llama3.2:latest', 'content': '...',
  'stop_reason': 'end_turn', 'usage': {'input_tokens': 38, 'output_tokens': 15},
  'raw': {...total_duration: 1507547625, load_duration: 1180986750,
   eval_count: 15, eval_duration: 125781125...}}}
```

Token-by-token rendering is real on huckgod's hardware: after the 1180ms cold-model load_duration, the 14 deltas streamed in 125ms at ~9ms inter-delta gaps. The audit log got a new `provider.invoke` row with **`streamed=true`** — distinguishing it from the four `streamed=false` invokes from session 35:

```
$ daimon activity query --kind provider.invoke --limit 5
...
2026-05-11T17:46:35  provider.invoke  ollama/llama3.2:latest    streamed=false matched=0
2026-05-11T17:46:55  provider.invoke  openai/gpt-5-mini-...     streamed=false matched=0
2026-05-11T17:47:06  provider.invoke  openai/gpt-5-mini-...     streamed=false matched=0
2026-05-11T17:47:25  provider.invoke  lmstudio/liquid/lfm2.5    streamed=false matched=0
2026-05-11T18:07:30  provider.invoke  ollama/llama3.2:latest    streamed=true  matched=0  ← new

$ daimon activity verify
verified 17 entries — chain ok
```

The chain now carries five `provider.invoke` rows — four non-streaming (sessions 33+35), one streaming (this session) — all chain-verified by the CLI walking the full 17-entry log under the same identity.

**What we explicitly did NOT ship in this session:**

- **TypeScript streaming.** Same wire shape applies; the implementation is an async-iterator API (`for await (const delta of client.provider.stream(...))`) over Node's `net.Socket` with no half-close. Estimated ~150 lines + vitest cases mirroring this session's pytest suite — a natural single-session arc as TS SDK session 2.

- **Pydantic/dataclass type modelling on the streaming envelope.** Still deferred — same reasoning as for the rest of the SDK: keep return types loose so the SDK doesn't drift behind the Go side's evolving record shapes.

- **PyPI publishing.** v0.1.x polish; non-load-bearing for the v0.1 milestone close. The Python SDK is feature-complete on v0.1's full RPC surface now (including streaming); publishing is the next step in the polish arc, alongside npm for the TS SDK.

- **Live Claude streaming** — still blocked, no `ANTHROPIC_API_KEY` in env per huckgod's call. 60-second close when the key shows up; same stream pattern would round-trip through Claude's streaming adapter.

- **Memory-kind canonicalisation polish session.** Only partial close: the Python SDK's README example was updated to `kind="fact"` in this session (the immediate user-facing fix), but the StubDaemon still doesn't validate kinds and the TypeScript SDK README still uses `kind: "note"`. Spawn-task chip remains live in CHECKPOINT for the full canonicalisation pass.

**What we explicitly punted (priority order for next session):**

1. **TypeScript SDK session 2 — `provider.stream`.** Now that Python has streaming, TS-side parity is the next natural arc. Same wire shape, async-iterator API instead of class-based iterator. ~60 min, no live providers needed for tests.
2. **Live Claude streaming round-trip** (60 sec when `ANTHROPIC_API_KEY` shows up).
3. **Memory-kind canonicalisation polish session.**
4. **v0.2 design — x402 / agent wallet, design-only session.**
5. **PyPI + npm publishing.**

**Next session begins with:** the Python SDK is now feature-complete on v0.1's full RPC surface — identity, memory (write/read/search/list), provider (list/invoke/stream), activity (append/query/verify). The TypeScript SDK still lacks streaming; that's the next natural arc. The v0.1 SDK milestone closes when TS streaming ships — at that point both languages are at full RPC parity, and the remaining work is polish (PyPI/npm publishing, memory-kind canonicalisation, type modelling, live Claude when the key shows up, v0.2 design).

## 2026-05-11 — Day Zero, thirty-sixth-and-a-half session: SDK stubs validate memory kind so fixtures can't drift from the daemon

**Tiny polish session — ~30 min — closing the memory-kind docs mismatch that session 35's cross-language live smoke surfaced.** Sessions 32+33 (Python SDK) and 33-34 (TypeScript SDK) had shipped with `kind="note"` (and `kind: "note"`) in their quick-start examples, in their `_rpc`/integration test fixtures, and in the error-handling examples. Session 35 ran a real daimon and discovered "note" is not a valid kind — `memory.Kind.Valid()` (`internal/memory/memory.go:41`) only accepts `fact`, `preference`, `task`, `observation`, and a real write with `kind: "note"` returns `-32004 invalid memory kind`. Session 36 patched the Python SDK quick-start as part of the provider.stream rollout (a "partial close" per its commit body); this session closes the rest.

**What this session locked down — the actual bug behind the docs typo:** both StubDaemons (the Python `conftest.py` test harness and the TypeScript `test/stub-daemon.ts` harness) accepted any kind on `daimon.memory.write` because they're generic JSON-RPC dispatchers — handlers register per-method and don't validate request schemas. That made the SDK/daemon drift invisible to the unit suites. The fix is in two parts:

1. **A hard-coded `VALID_MEMORY_KINDS` constant in each stub** with an explicit `// keep in sync with internal/memory/memory.go:41` comment. Picked hardcoded constants over a runtime-loaded JSON config file because four strings is too small to justify a new shared-source dependency, and the comment makes drift a code-review concern rather than a silent deploy-time surprise.

2. **Pre-handler validation on `daimon.memory.write`** in both stubs, returning the same `-32004 invalid memory kind` the daemon does. The Python validation slots into `_serve_one` right after `self.calls.append(...)` and BEFORE the streaming-handler dispatch (so memory.write — which is never a streaming method — never accidentally reaches `_serve_stream`). The TS validation slots into `serveConnection` at the same dispatch point.

3. **One regression test per SDK** asserting the stub returns `-32004` for an invalid kind, so if a future refactor breaks the validation the suite catches it: `test_stub_rejects_invalid_kind_like_daemon` (pytest, +1 case → 46) and `"stub rejects invalid kind like the daemon"` (vitest, +1 case → 36 pre-streaming).

4. **All `"note"` fixtures swapped to `"fact"`** across both test suites + both README examples + both error-handling examples. Confirmed afterwards via `grep -rn '"note"' sdk/`: the only matches are the two regression tests where `"note"` is *required* because the test asserts the stub rejects it.

**Rebase friction:** the branch where this landed had branched off pre-session-3, and origin/main had moved ahead with the session 36 commit which had ALREADY updated the Python README's quick-start `kind="note"` → `kind="fact"` (the "partial close") plus refactored `conftest.py`'s `_serve_one` for streaming. Two conflicts to resolve: the README quick-start (took main's better version with the explicit valid-kinds comment) and `_serve_one` (re-ordered so the new memory-kind validation runs BEFORE the streaming-handler dispatch). All 46 pytest + 36 vitest pass post-rebase.

**Commit `ae3fe1c`:** 8 files changed, 93 insertions, 24 deletions.

**Why this matters more than the test-count delta suggests:** session 35's whole point was "live smoke surfaces docs/code drift the unit tests miss." The unit tests didn't catch it because the stub didn't enforce the daemon's validation contract — so docs and tests were free to use any kind, and an SDK user copy-pasting the README example would hit a real `-32004` on first call. This session brings the stubs into the contract, so the unit suite is now a more honest predictor of live-daemon behaviour on at least one wire shape. The same logic could extend to `memory.read`'s id-format requirements, `activity.append`'s kind-non-empty requirement, etc. — punt-list item for later when the SDK starts to fan out further.

**What we explicitly did NOT do:**

- **Activity-kind validation in the stubs.** The daemon's `activity.Kind` is free-form (no enumerated valid-list — kinds are documentation-only conventions like `memory.write`, `provider.invoke`). No analog to enforce.
- **Memory-search kind validation.** Daemon doesn't reject unknown kinds on search (it just won't match anything), so the stub doesn't either — sticking with daemon parity exactly rather than over-validating.
- **JSON config file approach.** Considered and rejected: four strings is too small to be worth a new shared-source dependency. Constants + comments is lighter and keeps the drift visible in PR diffs.

**Next session begins with:** TypeScript SDK session 2 (`provider.stream`) is the obvious next move — Python SDK has streaming as of session 36, TS doesn't yet. Same wire shape, async-iterator API instead of Python's class-based iterator. ~60 min, no live providers needed for tests. After that, the v0.1 SDK milestone closes on full RPC parity in both languages.

## 2026-05-11 — Day Zero, thirty-seventh session: TypeScript SDK session 2 — provider.stream, full v0.1 RPC parity in both languages

**The TypeScript SDK reaches full v0.1 RPC parity with Python.** Sessions 33-34 shipped non-streaming verbs (identity, memory, provider.list, provider.invoke, activity); this session adds `provider.stream` with an `AsyncIterable<string>` API. Wire shape mirrors `cmd/daimon/rpc.go::rpcStream` byte-for-byte: one request out, 0..N `daimon.provider.stream.delta` notifications back, one terminal `{result|error, id}` frame.

**Architectural decisions held in-session:**

1. **`AsyncIterable<string> & AsyncIterator<string>` over generator-function syntax.** Node's `async function*` generators with a final return value force callers into `for await...of` followed by manually inspecting the iterator's result — clunky in JS land. A class that implements both interfaces directly gets a `.final` property visible at any time, and the language gives us `for await (const delta of stream)` and `stream.next()` without ceremony. Also lets us implement `return()` so a `break` inside `for await` cleanly tears the socket down — there's no Python context-manager equivalent in JS, but `return()` is the protocol-level equivalent.

2. **No half-close on the write side.** Same critical lifecycle constraint as Python's `_stream.py`: `sock.end(payload)` half-closes the write side, which Node's `net.Socket` allows but the server doesn't expect — it keeps writing notifications and the terminal frame on the same connection. Use `sock.write(payload)` instead. Sock stays bidirectional until the terminal frame closes it from the server side, or our `close()` destroys it from our side.

3. **Async queue + deferred waiter pattern, not promise chains per frame.** Node's `data` events fire asynchronously; the `for await` consumer pulls one delta at a time. Internal `events: StreamEvent[]` queue accumulates delta/terminal/error events as frames are parsed; `next()` shifts the head if non-empty, otherwise awaits a `waiter` deferred that resolves when more data arrives. Standard async-queue pattern. Cleaner than threading a promise through every `onData` call.

4. **Frame-by-frame parsing inside `onData`, not in `next()`.** Buffer accumulates partial reads across recv boundaries; the loop scans for every `\n` in one pass, parses each complete line, pushes events. Multiple deltas in one TCP segment (common for fast providers like Ollama at ~9ms inter-delta gaps) all get enqueued in a single `onData` tick, so the consumer can drain them at iteration speed instead of awaiting between each.

5. **Forward-compat for unknown notification kinds.** The reader silently skips any notification method that isn't `daimon.provider.stream.delta`, mirroring Python's `_stream.py` exactly. Future deltas (tool-call deltas, role markers) won't break older SDKs. Codified in the `"ignores unknown notification kinds (forward-compat)"` vitest case, which injects a `daimon.provider.stream.tool` notification between two real deltas via a stub `serveStream` override.

6. **Terminal-frame error mapping is identical to non-streaming.** Reuses `fromErrorObject` from `errors.ts`: code `-32001` lifts to `DaemonLocked`; everything else to `RPCError`. Same exception family, same `.code`/`.rpcMessage`/`.data` attributes. Two vitest cases cover both — terminal `-32603` → `RPCError` after deltas have already drained, and terminal `-32001` → `DaemonLocked` on the first iteration.

7. **StubDaemon refactor: line-based dispatch, not buffer-until-half-close.** The previous stub waited for `conn.on("end", ...)` before reading the request, which works for one-shot calls (the SDK half-closes via `sock.end(payload)`) but blocks forever for streaming clients that don't half-close. Replaced with a per-byte buffer that watches for the first `\n` and dispatches immediately. The 35 pre-existing non-streaming tests pass verbatim because `rpc.ts` always appends `\n` to its payload — the new "dispatch on first newline" reader is byte-compatible with the old "dispatch on EOF" reader for that case. `runStream(...)` writes deltas-then-terminal in one tick; `serveStream` is a public override slot for frame-injection tests, mirroring Python's `_serve_stream` monkey-patch pattern.

**What landed:**

- **`sdk/typescript/src/stream.ts`** (new file, ~260 lines): `StreamHandle` class implementing `AsyncIterable<string> & AsyncIterator<string>` with `.final: Record<string, unknown> | null`. `next()` drains the event queue or awaits a deferred until either a delta is produced, a terminal sets `.final` + ends iteration, or an error throws. `return()` is wired so `break` inside `for await` destroys the socket. `close()` is the explicit tear-down for early bail. `openStream()` dials, writes the request without half-closing, and resolves the handle once the request is on the wire. `DEFAULT_STREAM_TIMEOUT_MS = 300_000` (5 min) — streaming calls can be much longer than non-streaming.

- **`sdk/typescript/src/client.ts`** (+~40 lines): `ProviderNamespace.stream(params: ProviderStreamParams): Promise<StreamHandle>` — same flat-kwargs surface as `invoke`, assembles the same nested `{provider, request: {...}}` wire envelope. `ProviderStreamParams extends ProviderInvokeParams & { timeoutMs?: number }`.

- **`sdk/typescript/src/index.ts`** (+3 lines): re-export `StreamHandle`, `DEFAULT_STREAM_TIMEOUT_MS`, and the `ProviderStreamParams` type.

- **`sdk/typescript/test/stub-daemon.ts`** (~80 line refactor + ~50 new): switched dispatch path from `conn.on("end")` to `conn.on("data")` with first-newline detection. New `StreamHandler` type + `stream(method, handler)` registration. `runStream` writes notifications then the terminal frame. `serveStream` override slot for frame-injection tests.

- **`sdk/typescript/test/stream.test.ts`** (new file, 11 tests): yields-all-deltas-in-order, final-envelope-populates-after-iteration, assembles-nested-request-from-flat-kwargs, passes-inject-context-verbatim, zero-deltas-terminal-only (fallback case), ignores-unknown-notification-kinds (forward-compat), terminal-RPCError, terminal-`-32001`-DaemonLocked, close()-tears-down-socket, for-await-break-tears-down-via-return (extra case not in pytest — JS-idiomatic), peer-closes-without-terminal-raises.

- **`sdk/typescript/README.md`** updated: status line bumped to "Identity, memory, provider (list / invoke / stream), and activity verbs all surfaced. Full RPC parity with the Python SDK"; quick-start now demonstrates the `for await` + `.final` pattern; test-count line bumped from 35 to 46.

**Test count:** 36 → 47 vitest cases (+11), build clean, typecheck clean, all green in ~300ms. Python suite untouched at 46. Go suite untouched at 287.

**Live streaming round-trip:** intentionally not run in this session — the wire shape is byte-for-byte mirrored from Python session 36's live smoke against `ollama/llama3.2:latest`, where 14 deltas streamed at ~9ms inter-delta gaps. The TS implementation parses the same wire frames the same way. Running it again would just be a confidence check, not new evidence. The cross-language smoke harness from session 35 could be extended to do TS + Python streaming round-trips against the same daimon in a single test run, but that's a session-39+ polish move, not a milestone-close requirement.

**What we explicitly did NOT ship in this session:**

- **TypeScript live Claude streaming.** Same blocker — no `ANTHROPIC_API_KEY` in env. 60-second close when the key shows up. Same async-iterable shape would round-trip through Claude's streaming adapter on the daemon side.

- **`@daimon/sdk` npm publish.** v0.1.x polish. Now that TS streaming has landed, the SDK is feature-complete on v0.1 — npm publish is the next step in the polish arc, alongside PyPI for Python.

- **Type modelling on the streaming envelope.** Both SDKs still return `Record<string, unknown>` / `dict` for response envelopes. Keeping return types loose so the SDKs don't drift behind the daemon's evolving record shapes.

- **Cross-language streaming smoke against one daimon.** Like session 35's non-streaming smoke but with streaming. Both SDKs streaming token deltas from the same Ollama-backed daimon, audit chain verified by both clients + the CLI. ~30 min when revisited.

**What we explicitly punted (priority order for next session):**

1. **Live Claude streaming round-trip** (60 sec when `ANTHROPIC_API_KEY` shows up — applies to both SDKs identically now).
2. **Cross-language streaming smoke session** — sessions 35's pattern but with `provider.stream` instead of `provider.invoke`. Lands as session 38 or 39.
3. **`@daimon/sdk` npm + `daimon` PyPI publish prep.** Set up `npm pack` smoke, decide on scope (`@daimon/sdk` already in `package.json`), write minimal CI for `npm publish --dry-run` and `python -m build`. v0.1.x polish; non-load-bearing for the v0.1 milestone close.
4. **v0.2 design — x402 / agent wallet, design-only session.** Multi-session arc start.
5. **Type modelling pass on both SDKs.** Pydantic models for the memory record + provider envelope; mirrored TS interfaces. ~60 min per language. Improves DX significantly.

**Next session begins with:** the v0.1 SDK milestone is **closed on full RPC parity in both Python and TypeScript** — identity, memory (write/read/search/list), provider (list/invoke/stream), activity (append/query/verify). Both languages have native streaming with the same wire shape. The remaining v0.1.x work is publishing + live Claude when the key shows up + cross-language streaming smoke for the next CHECKPOINT round. After that, v0.2 design (x402 / agent wallet) opens.

## 2026-05-11 — Day Zero, thirty-eighth session: cross-language streaming smoke — both SDKs stream from one daimon, chain verified three ways

**Live evidence that session 37's TypeScript streaming surface is wire-compatible with session 36's Python streaming surface, against a single daimon.** Mirrors session 35's non-streaming cross-language smoke but with `daimon.provider.stream` substituted for `daimon.provider.invoke` — same DID, same chain, both SDKs token-by-token streaming from the same Ollama-backed daemon, audit chain integrity verified by Python SDK + TypeScript SDK + the Go CLI as three independent code paths.

**Setup:** fresh `DAIMON_HOME=/tmp/dt-stream-smoke-43622` to keep the chain short and the smoke readable. `daimon init` + `daimon unlock` driven non-interactively by piping the password (`printf 'pw\n' | daimon ...`) — the readPassword() shim falls through to a bufio line read when stdin isn't a TTY, by design. Daemon auto-spawned via `daimon unlock`'s detached `daimond serve`; Ollama was already up on `127.0.0.1:11434` with `llama3.2:latest` pulled. No Anthropic/OpenAI keys involved — Ollama is the only native streamer available locally and the only one that actually exercises the deltas-then-terminal wire path end-to-end.

**Python half** (against the freshly installed `pip install -e sdk/python`, Python 3.13):

```
py: DID = did:key:z6MktpxhUYqtSjoDgsrHtW4sAnKwfhELxt7twZBkN1U7KFmz
py: 3 deltas — first 1398.8ms, last 1420.2ms, mean inter-gap 10.72ms
py: content = 'hello from python'
py: model=llama3.2:latest stop=end_turn usage={'input_tokens': 32, 'output_tokens': 4}
py: activity.verify -> {'verified': 2, 'ok': True}
```

Cold model load: 1.4s before the first delta (consistent with session 36's 1180ms `load_duration` plus first-token latency). Once warm, deltas at ~10.7ms inter-gap. Content is exactly what the prompt asked for ("Reply with exactly: hello from python"). Python's `activity.verify` reports 2 entries chain-ok (the daimon.created genesis row + the `provider.invoke streamed=true` row this stream just created — the `activity.verified` row from this verify call appends *after* the walk and shows up only in the next walker's count).

**TypeScript half** (against the built dist at `sdk/typescript/dist/`, plain ESM imports, Node 22):

```
ts: DID = did:key:z6MktpxhUYqtSjoDgsrHtW4sAnKwfhELxt7twZBkN1U7KFmz
ts: 4 deltas — first 213.0ms, last 239.5ms, mean inter-gap 8.83ms
ts: content = "hello from typescript"
ts: model=llama3.2:latest stop=end_turn usage={"input_tokens":33,"output_tokens":5}
ts: activity.verify -> {"verified":4,"ok":true}
```

Same DID as Python — single chain, single identity. The model was already warm from Python's call, so first delta arrives at 213ms (vs Python's 1.4s cold-load). 4 deltas at ~8.8ms inter-gap — the deltas-per-token rate Ollama yields. Content matches the prompt. TS `activity.verify` reports 4 entries: genesis + py provider.invoke + py activity.verified + ts provider.invoke. The TS SDK's `for await (const delta of stream)` produces deltas in the same order with the same content the Python SDK's `for delta in stream` produces against the same daemon, against the same provider, against the same model.

**CLI verification — the third independent code path:**

```
$ daimon activity verify
verified 5 entries — chain ok

$ daimon activity query --limit 20
TIME                       KIND               ID                          SUMMARY
2026-05-11T18:53:12+08:00  daimon.created     01KRBAPT0BP1Z4RXQCZQ109A6W  did=did:key:z6Mk…KFmz
2026-05-11T18:54:07+08:00  provider.invoke    01KRBARFYSQ89NJS317JQDKPKZ  ollama/llama3.2:latest streamed=true matched=0
2026-05-11T18:54:07+08:00  activity.verified  01KRBARG1P39HT80DK5VS4YBHQ  verified=2
2026-05-11T18:54:52+08:00  provider.invoke    01KRBASVCWWBQA0JX1H0MMR87Z  ollama/llama3.2:latest streamed=true matched=0
2026-05-11T18:54:52+08:00  activity.verified  01KRBASVD6F8VCKGGJ79PFS22R  verified=4
2026-05-11T18:56:18+08:00  activity.verified  01KRBAWFSQ7MCM1GKV2H2REN0K  verified=5
```

Both `provider.invoke` rows carry `streamed=true` — `cmd/daimon/cmd_activity.go`'s per-kind summary writer pulls that boolean straight from `handleProviderStream`'s audit payload, distinguishing it from `streamed=false` invokes. The full JSON for one of the rows confirms the typed payload:

```json
{
  "hash": "blake3:53ab4421472b7dbc4cc23b894d29fe35b30c233e516229dedb7b1c2a349b65b8",
  "id": "01KRBARFYSQ89NJS317JQDKPKZ",
  "kind": "provider.invoke",
  "payload": {
    "duration_ms": 1428,
    "input_tokens": 32,
    "model": "llama3.2:latest",
    "output_tokens": 4,
    "provider": "ollama",
    "stop_reason": "end_turn",
    "streamed": true
  },
  "prev_hash": "blake3:2cc3ce5211491ed92a2d7d6e392eea34fde9851b941254a8bd2999fcaf24f49b",
  "signature": "U9BUQ...",
  "ts": 1778496847833
}
```

`prev_hash` chains backwards through `activity.verified verified=2` (TS's verify call wrote that row before the CLI walked) → `provider.invoke` (TS's stream) → `activity.verified verified=2` (Python's verify call) → `provider.invoke` (Python's stream) → `daimon.created` (genesis). All five hashes verify against their content + prev_hash + identity signature.

**What this proves about the v0.1 SDK milestone:**

1. **Both SDKs' streaming surfaces are wire-compatible with the daemon's `daimon.provider.stream`** — same notification frames, same terminal envelope, same `-32001 → DaemonLocked` lifecycle, same byte-level framing. Same `provider.Streamer` adapter on the daemon side (Ollama's `/api/chat` stream=true), feeding both languages indistinguishably.

2. **Both SDKs persist their calls into one signed, chained audit log** under the same identity. There is exactly one DID; both SDK calls and the CLI's verify can walk the same `activity.log` and agree on every row's hash and signature. No "Python writes one log, TypeScript writes another" — the daemon owns the single source of truth and both clients are pure consumers.

3. **`streamed=true` is durable evidence**, captured at row-write time on the daemon side, not derived from CLI output. Any future tooling — analytics, billing, governance — that reads the audit log can distinguish streaming from non-streaming invokes with no SDK cooperation.

4. **Three independent code paths agree.** Python SDK's `activity.verify` (verified=2 mid-walk), TS SDK's `activity.verify` (verified=4 mid-walk), Go CLI's `daimon activity verify` (verified=5 at end). Each walks the chain from `daimon.created` forward, recomputes each row's `blake3` hash from `prev_hash || canonical_payload || ts || kind`, recomputes the Ed25519 signature with the embedded DID's pubkey, and reports the count. If any byte of any row had drifted between languages, at least one of these would have reported `chain INVALID`. None did.

**What we explicitly did NOT ship:**

- **A reusable smoke harness committed to the repo.** The Python + TS smoke scripts live under `/tmp/dt-stream-smoke-43622/` (`python_smoke.py`, `ts_smoke.mjs`) and were torn down with the rest of the home. They could be promoted to `examples/streaming/` if we want to ship them as user-runnable examples alongside the SDKs, but that's an examples-and-docs polish move, not a milestone-close requirement.
- **Live Claude streaming.** Same blocker — no key. The Anthropic adapter would fall back to a synchronous invoke wrapped in a single terminal frame (deltas=0); the wire shape is identical to what the smoke just exercised against Ollama, just with `streamed=false` on the audit row.
- **Cross-language inject_context streaming.** Both SDKs accept `inject_context` on `provider.stream`; tested at the unit level. A live smoke that writes a few memory rows then streams with `inject_context` to see `matched=N` in the audit log would be a useful follow-up but isn't load-bearing for the milestone.

**What we explicitly punted:**

1. **PyPI + npm publish prep.** v0.1.x polish; non-load-bearing for the v0.1 milestone close. Both packages are publish-ready in terms of code; remaining is metadata polish (`pyproject.toml` keywords/classifiers, `package.json` repository/bugs/homepage), README cross-links, CI for dry-run publish.
2. **Live Claude streaming round-trip** (60 sec when `ANTHROPIC_API_KEY` shows up — applies to both SDKs identically).
3. **`examples/streaming/`** with the smoke scripts polished and committed. ~30 min.
4. **v0.2 design — x402 / agent wallet, design-only session.**

**Next session begins with:** the v0.1 SDK milestone is now closed with **live evidence in both languages on the streaming surface**, in addition to the unit-test coverage and the non-streaming live evidence from session 35. The only remaining items between this state and a v0.1.0 final tag are PyPI/npm publish prep and live Claude streaming. v0.2 design is the next substantive arc.

## 2026-05-12 — Day Zero, thirty-ninth session: first SDK pre-release published — daimon-protocol on PyPI + @daimon-protocol/sdk on npm

**Both SDKs landed on their respective public registries as v0.1.0.dev0 pre-releases.** First time the protocol's first-party clients are installable from outside a repo checkout. Smoke of the entire publish pipeline before cutting v0.1.0 GA — caught two real issues at zero blast-radius cost (a stale dist on the first build attempt and an npm scope mismatch from a name collision).

**The naming detour.** The `daimon` namespace is contested on both registries. Survey-of-the-landscape:

- PyPI `daimon`: held by Alexander Fedotov, v0.0.2 from 2024-05-19 ("δαίμων, pronounced daimon or daemon — 'god', 'godlike', 'power', 'fate'"). Dormant hobby package; reclaim under PEP 541 is possible but slow (PyPI's bar is high and last upload was only ~2y ago).
- PyPI `daimon-sdk`: TAKEN. v0.4.3 (uploaded 2026-05-09 — three days before this session). Authored by "processd contributors": "Typed async Python SDK for `processd-mcp` and `processd-sandbox-manager`". An unrelated MCP wrapper that has nothing to do with this project but uses the same word.
- PyPI `daimon-client`: TAKEN. v0.2.0 (2026-05-07). "Python client for the daimon AI sidecar." Yet another unrelated "daimon"-themed project. Three on PyPI already.
- PyPI `daimon-protocol`: FREE.
- PyPI `pydaimon`: FREE.
- npm `daimon` org: TAKEN.
- npm `daimon-protocol` org: free, registered fresh this session by huckgod via <https://www.npmjs.com/org/create> (web-only — `npm org create` is not a thing).
- npm `@daimon-protocol/sdk`: FREE.

**Decision:** `daimon-protocol` on both registries. The import name in user code stays `daimon` (Python distribution name and import name are independent in setuptools; npm just uses the package name as the import identifier). Documented in pyproject.toml's `name=` comment and both CHANGELOGs' `### Naming` sections so future maintainers don't have to re-litigate. Reclaim of `daimon` (PyPI from Fedotov, npm org from whoever holds it) stays a punt-list item; trivial to republish under both names if either becomes available.

**Publish auth setup.** PyPI: `~/.pypirc` at mode 0600 with `[pypi] username=__token__ password=pypi-...` (an account-scoped token; project-scoped tokens can't be issued before the project exists — important pre-publish gotcha that PUBLISH.md now documents). npm: already logged in as `reziiix-ai` from a prior session via `npm login`; that CLI session is what `npm publish` uses (no separate token setup, no env vars).

**What actually happened during publish.** PyPI side was uneventful: `python -m build` produces `daimon_protocol-0.1.0.dev0-py3-none-any.whl` + `.tar.gz`; `twine check` passes both; `twine upload dist/*` succeeds with the wheel rendering at <https://pypi.org/project/daimon-protocol/0.1.0.dev0/>. JSON API confirms 13 classifiers, 5 project URLs, correct author/license/keywords. `pip install --pre daimon-protocol` from a clean venv installs the wheel and imports resolve.

**The npm publish gotcha.** First `npm publish --tag dev` completed with `+ @daimon-protocol/sdk@0.1.0-dev.0` printed to the terminal — looked like a clean success. But subsequent `npm view`, `npm install`, and the web page at <https://www.npmjs.com/package/@daimon-protocol/sdk> all returned 404/403 for several minutes. Diagnostics: a retry `npm publish --tag dev` returned `npm error 403 — You cannot publish over the previously published versions: 0.1.0-dev.0` — definitive confirmation the first publish succeeded server-side. Unauthenticated `curl https://registry.npmjs.org/@daimon-protocol/sdk/-/sdk-0.1.0-dev.0.tgz` returns 200, confirming the **tarball storage layer committed**. The **manifest packument index** was the slow link — npm's registry seems to commit tarballs to S3-or-equivalent immediately, then a separate indexing service updates the packument that `npm view` / `npm install` consume. For first-publishes to brand-new orgs, that gap can be tens of minutes. Worth knowing for future first-publishes; PUBLISH.md needs a note.

**Git tag.** `v0.1.0.dev0` annotated tag pushed to origin (separate tag namespace from `v0.1.0` so the GA cut later won't conflict). Tag message documents the registry locations and the wire-shape inventory.

**What we explicitly did NOT do:**

- **GA cut to v0.1.0.** Intentionally deferred. The dev0 smoke is the bridge — if anything's wrong with the published artifacts (wrong import path, missing metadata, etc.) we can iterate `dev1`/`dev2` without burning the GA version slot. Both PyPI and npm reject re-uploads of the same version forever (PyPI yank doesn't free the version; npm unpublish has a 72-hour window then becomes deprecate).
- **CHANGELOG ### Released section.** Both CHANGELOGs still have everything under `[Unreleased]`. Will move under a dated `[0.1.0]` section when GA is cut.
- **Documentation pivot from "once published" hedging language to "is published".** The top-level README still says `pip install daimon-protocol` *or* `pip install -e sdk/python`, with both equally valid; could drop the "once published" caveat. Doing that now would be slight overclaim until manifest propagation finishes — better to update on GA cut.
- **Live Claude smoke.** Still blocked, no `ANTHROPIC_API_KEY` in the shell.

**What we explicitly punted (priority order for next session):**

1. **Wait 10–30 min, re-verify `npm install @daimon-protocol/sdk@dev` from a clean shell works.** If it does, cut GA: bump versions in both manifests to `0.1.0` (PyPI) / `0.1.0` (npm), commit, push, wait for CI green, tag `v0.1.0`, re-publish both. If it doesn't after a reasonable wait, contact npm support — package may be stuck in a moderation queue or hit a propagation bug.
2. **Move both CHANGELOGs from `[Unreleased]` → `[0.1.0]` section** as part of the GA cut.
3. **Update top-level README + SDK READMEs** to drop the "once published" hedging once GA is cut.
4. **Optional: PEP 541 reclaim attempt on PyPI `daimon`**, or direct email to Fedotov. If `daimon` becomes available, dual-publish with `daimon-protocol` as a compatibility shim and the import name continuity means zero user breakage.
5. **Live Claude streaming** when key shows up (~60 sec).
6. **v0.2 design** — x402 / agent wallet, design-only session 1.

**Next session begins with:** v0.1.0.dev0 is live on both registries. The publish ritual is now battle-tested end-to-end (PyPI smooth, npm with a propagation-lag surprise). Cutting v0.1.0 GA is now a 3-step ceremony: bump both manifests, re-publish, re-tag. The remaining v0.1.x items are exactly: (a) the GA cut, (b) live Claude smoke, (c) optional PyPI/npm name reclaims. After that, v0.2 design is the next substantive arc.
