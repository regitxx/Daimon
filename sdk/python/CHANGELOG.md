# Changelog

All notable changes to the Daimon Python SDK will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`client.wallet.show_mnemonic(password=)`** — re-display the
  daimon's BIP-39 mnemonic. Requires the keystore password as
  re-confirmation (the daemon re-runs the full Argon2id + AES-GCM-
  decrypt against the on-disk file, NOT against in-memory state).
  Wrong password surfaces as `RPCError` with the NEW typed code
  `-32008` (CodeWrongPassword), distinct from `-32001`
  (CodeIdentityLocked) so callers can branch on the code without
  the "daemon is locked" message implying `daimon unlock` is the
  fix when really it's "type the password again."
  Use cases: verify the backup was written down correctly; export
  the seed for import into MetaMask / Phantom / Rabby.

## [0.2.0.dev0] — 2026-05-18

First pre-release of the v0.2 surface — wallet + x402 payments.
Published under the PyPI `--pre` channel (`pip install --pre
daimon-protocol`) so the default `pip install daimon-protocol`
without `--pre` continues to resolve to `0.1.0`. v0.2.0 GA cuts
once phase 40.4 lands (live Base Sepolia settlement against a real
x402-protected endpoint with a real facilitator).

### Added

- **`client.wallet` namespace** (v0.2 phase 40.6 — mirrors phases 40.1+40.2
  on the daemon side). Verbs: `list()`, `create(chain=)`,
  `address(chain=)`, `sign(chain=, digest_hex=)`. Wraps the BIP-39/BIP-32
  HD wallet keystore the daemon auto-creates on first unlock. v0.2 ships
  EVM chains only (`evm:base`, `evm:base-sepolia`, etc.); unsupported
  chains surface as `RPCError` with code `-32602`.
- **`client.payment.pay` verb** (v0.2 phase 40.6 — mirrors phase 40.5a on
  the daemon side). Pays an x402-protected URL end-to-end: parses the
  resource's `PAYMENT-REQUIRED` header, signs an EIP-3009
  `transferWithAuthorization` against the matching wallet, retries with
  `PAYMENT-SIGNATURE`, decodes the response. Accepts `bytes` or `str`
  for the request body (the SDK picks `body_base64` or `body_text`
  internally) and returns a structured `{status_code, response_headers,
  body, payment_response}` dict.
- **Typed RPC error codes for the payment surface**: `-32006`
  ("payment exceeds local ceiling") and `-32007` ("no compatible
  requirement") propagate through `RPCError.code` so callers can
  branch on them without string-matching the message.
- **CI cross-language live smoke** (session 42, parallel to session 38's
  streaming smoke for v0.1) — every push to main runs both SDKs against
  a real-network mock x402 server that cryptographically verifies the
  `PAYMENT-SIGNATURE` header. Any drift between the Python and
  TypeScript EIP-3009 encoders trips a build immediately. Negative-
  path coverage (ceiling rejection) included.

## [0.1.0] — 2026-05-12

First general-availability release. Predecessor `0.1.0.dev0`
(2026-05-12) was a publish-pipeline smoke under the `--pre` channel;
GA promotes the same surface to `latest` on both registries.

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
