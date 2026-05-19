# Changelog

All notable changes to the Daimon TypeScript SDK will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0-dev.1] — 2026-05-19

Second pre-release on the v0.2 track. Adds the password-gated
mnemonic re-display verb on the SDK surface (the underlying daemon
RPC + CLI shipped in the Daimon binary on 2026-05-19; this release
makes it a first-class method of `client.wallet` in the SDK as
well, so installers on the `@dev` channel get parity with the
Daimon CLI without falling back to the `client._call(...)` escape
hatch).

### Added

- **`client.wallet.showMnemonic({password})`** — re-display the
  daimon's BIP-39 mnemonic. Requires the keystore password as
  re-confirmation (the daemon re-runs the full Argon2id + AES-GCM-
  decrypt against the on-disk file, NOT against in-memory state).
  Wrong password throws `RPCError` with the NEW typed code `-32008`
  (CodeWrongPassword), distinct from `-32001` (CodeIdentityLocked)
  so callers can branch on the code without the "daemon is locked"
  message implying `daimon unlock` is the fix when really it's
  "type the password again."
  Use cases: verify the backup was written down correctly; export
  the seed for import into MetaMask / Phantom / Rabby.

### Related (Daimon CLI binary, not SDK)

- **`daimon wallet recover`** — offline counterpart to
  `showMnemonic`. Writes a fresh wallet keystore from a
  user-supplied 12- or 24-word BIP-39 phrase, so an external
  backup can be imported into a fresh daimon. CLI-only by design
  (no SDK wrapper): a live seed swap on a running daimon would
  orphan every wallet derived from the previous mnemonic, so
  recovery operates on the keystore file BEFORE the daemon ever
  opens it.

## [0.2.0-dev.0] — 2026-05-18

First pre-release of the v0.2 surface — wallet + x402 payments.
Published under the npm `dev` dist-tag (`npm install
@daimon-protocol/sdk@dev`) so the default `npm install
@daimon-protocol/sdk` against `latest` continues to resolve to
`0.1.0`. v0.2.0 GA cuts once phase 40.4 lands (live Base Sepolia
settlement against a real x402-protected endpoint with a real
facilitator).

### Added

- **`client.wallet` namespace** (v0.2 phase 40.6 — mirrors phases 40.1+40.2
  on the daemon side). Methods: `list()`, `create({chain})`,
  `address({chain})`, `sign({chain, digestHex})`. Wraps the BIP-39/BIP-32
  HD wallet keystore the daemon auto-creates on first unlock. v0.2 ships
  EVM chains only (`evm:base`, `evm:base-sepolia`, etc.); unsupported
  chains throw `RPCError` with code `-32602`.
- **`client.payment.pay` method** (v0.2 phase 40.6 — mirrors phase 40.5a
  on the daemon side). Pays an x402-protected URL end-to-end: parses the
  resource's `PAYMENT-REQUIRED` header, signs an EIP-3009
  `transferWithAuthorization` against the matching wallet, retries with
  `PAYMENT-SIGNATURE`, decodes the response. Accepts `Uint8Array` or
  `string` for the request body and returns a structured
  `{statusCode, responseHeaders, body, paymentResponse}` object.
- **Typed RPC error codes for the payment surface**: `-32006`
  ("payment exceeds local ceiling") and `-32007` ("no compatible
  requirement") propagate through `RPCError.code` so callers can
  branch on them without string-matching.
- **CI cross-language live smoke** (session 42, parallel to session 38's
  streaming smoke for v0.1) — every push to main runs both SDKs against
  a real-network mock x402 server that cryptographically verifies the
  `PAYMENT-SIGNATURE` header. Any drift between the Python and
  TypeScript EIP-3009 encoders trips a build immediately. Negative-
  path coverage (ceiling rejection) included.

### Fixed

- `VERSION` runtime export is now derived from `package.json#version`
  at build time via `scripts/gen-version.mjs`, chained ahead of
  `npm run build`, `npm run typecheck`, and `prepublishOnly`. The
  hardcoded constant that caused the `0.1.0` GA to ship with stale
  `VERSION === "0.1.0-dev.0"` is structurally eliminated — version
  drift between `package.json` and runtime metadata can no longer
  happen.

## [0.1.0] — 2026-05-12

First general-availability release. Predecessor `0.1.0-dev.0`
(2026-05-12) was published under the `dev` dist-tag for
publish-pipeline smoke; GA promotes the same surface to `latest`.

### Known issues

- The `VERSION` runtime export was hardcoded to `"0.1.0-dev.0"` at
  source-code level and not bumped in lockstep with `package.json`
  when cutting GA. Functional surface (`Client`, `StreamHandle`, all
  types and methods) is unaffected. Source is corrected in main;
  next release will return the right string from `VERSION`.

### Naming

- **npm package name** is `@daimon-protocol/sdk`. The bare `daimon`
  org on npm was already claimed by someone else, so we registered
  the `daimon-protocol` org to align with the PyPI distribution name
  (`daimon-protocol`). One brand, parallel scoping on both
  ecosystems.
- `publishConfig.access: public` is set so the first `npm publish`
  lands on the public registry as expected; without it, scoped
  packages default to a private (paid) registry.

### Added

- **Streaming surface (`client.provider.stream`)** — `StreamHandle`
  implements `AsyncIterable<string> & AsyncIterator<string>` so callers
  can `for await (const delta of stream)`, with the terminal envelope
  on `.final`. `return()` is wired so `break` inside `for await` tears
  the socket down cleanly. Mirrors the daemon's
  `daimon.provider.stream` notification + terminal-frame wire shape
  exactly. 11 vitest cases including forward-compat skipping of
  unknown notification kinds and the `for await break` tear-down path.
  Verified live against `ollama/llama3.2:latest`.
- **Provider + activity verbs** — `client.provider.list`,
  `client.provider.invoke`, `client.activity.append`,
  `client.activity.query`, `client.activity.verify`. `invoke`
  assembles the canonical `{provider, request: {...}}` envelope from
  flat params and threads `inject_context` through unchanged.
- **Identity + memory verbs** — `client.identity.get`,
  `client.memory.write`, `client.memory.read`, `client.memory.search`,
  `client.memory.list`.
- **Typed exception hierarchy** — `DaimonError` parent;
  `DaemonNotRunning` for socket absence/refusal; `DaemonLocked` for
  the typed `-32001` JSON-RPC error; `RPCError` for everything else,
  all exposing `.code`, `.rpcMessage`, `.data`.
- **`$DAIMON_HOME` resolution** mirrors the Go CLI's
  `internal/daimonhome` package, including the sun_path-overflow
  fallback to `$TMPDIR/daimon-$uid.sock` when the resolved path
  exceeds 104 bytes.
- **Stub Unix-socket test harness** under `test/stub-daemon.ts`:
  line-based dispatch matching the daemon's per-call lifecycle,
  including streaming notifications and `-32004 invalid memory kind`
  validation parity against `internal/memory/memory.go:41`.

### Notes

- Cross-language live smoke (sessions 35, 38) verified this SDK
  round-trips a daimon with the Python SDK against the same DID, audit
  chain verified by both SDKs and the Go CLI.
- See [JOURNAL.md](../../JOURNAL.md) for the per-session build log.
