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

