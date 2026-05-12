# Changelog

All notable changes to the Daimon Python SDK will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Preparing the first published `0.1.0` release. The pre-release tag in
`pyproject.toml` is currently `0.1.0.dev0`.

### Naming

- **PyPI distribution name** is `daimon-protocol`. The unqualified
  `daimon` name on PyPI belongs to an unrelated dormant hobby
  package (Alexander Fedotov, v0.0.2 from 2024-05); `daimon-sdk` and
  `daimon-client` are both held by other active unrelated projects
  (an MCP wrapper for `processd-mcp`, and a different "AI sidecar"
  client, respectively). `daimon-protocol` is uncontested, accurately
  describes what the package is, and aligns with the project's own
  framing in `SPEC.md`.
- **Import name** stays `daimon` — `from daimon import Client` works
  unchanged regardless of distribution name. This means the SDK can
  be republished under the bare `daimon` name in the future without
  user-facing breakage if the namespace becomes available.

### Added

- **Streaming surface (`client.provider.stream`)** — `StreamHandle` class
  iterable as delta strings with the terminal envelope on `.final`.
  Context-manager protocol for deterministic socket close on early
  exit. Mirrors the daemon's `daimon.provider.stream` notification +
  terminal-frame wire shape exactly. 10 pytest cases including
  forward-compat skipping of unknown notification kinds. Verified live
  against `ollama/llama3.2:latest`.
- **Provider + activity verbs** — `client.provider.list`,
  `client.provider.invoke`, `client.activity.append`,
  `client.activity.query`, `client.activity.verify`. `invoke` assembles
  the canonical `{provider, request: {...}}` envelope from flat kwargs
  and threads `inject_context` through unchanged.
- **Identity + memory verbs** — `client.identity.get`,
  `client.memory.write`, `client.memory.read`, `client.memory.search`,
  `client.memory.list`.
- **Typed exception hierarchy** — `DaimonError` parent;
  `DaemonNotRunning` for socket absence/refusal; `DaemonLocked` for the
  typed `-32001` JSON-RPC error; `RPCError` for everything else, all
  exposing `.code`, `.message`, `.data`.
- **`$DAIMON_HOME` resolution** mirrors the Go CLI's
  `internal/daimonhome` package: explicit env var → `os.UserConfigDir() / "daimon"`
  fallback, with the same sun_path-overflow fallback to
  `$TMPDIR/daimon-$uid.sock` when the resolved path exceeds 104 bytes.
- **Stub Unix-socket test harness** under `tests/conftest.py`:
  byte-for-byte the daemon's dispatcher, including streaming
  notifications and `-32004 invalid memory kind` validation parity
  against `internal/memory/memory.go:41`.

### Notes

- Cross-language live smoke (sessions 35, 38) verified this SDK
  round-trips a daimon with the TypeScript SDK against the same DID,
  audit chain verified by both SDKs and the Go CLI.
- See [JOURNAL.md](../../JOURNAL.md) for the per-session build log.
