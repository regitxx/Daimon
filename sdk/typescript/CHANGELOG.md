# Changelog

All notable changes to the Daimon TypeScript SDK will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
