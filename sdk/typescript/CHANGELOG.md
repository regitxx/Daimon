# Changelog

All notable changes to the Daimon TypeScript SDK will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Preparing the first published `0.1.0` release. The pre-release tag in
`package.json` is currently `0.1.0-dev.0`.

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
