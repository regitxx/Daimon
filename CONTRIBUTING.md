# Contributing to Daimon

Thanks for considering a contribution. Daimon is a protocol, a reference
daemon, two SDKs, and a small set of tools. Contributions to any of those
layers are welcome — the only hard requirement is that whatever you propose
keeps the SPEC honest and the test suite green.

This document is short on purpose. The deeper context lives in:

- [`SPEC.md`](./SPEC.md) — the protocol document (what the wire shape is)
- [`CHECKPOINT.md`](./CHECKPOINT.md) — what's in tree right now, decisions taken
- [`JOURNAL.md`](./JOURNAL.md) — chronological log of decisions and why
- [`QUICKSTART.md`](./QUICKSTART.md) — end-to-end walkthrough for users

Read at least CHECKPOINT before opening a non-trivial PR — it'll tell you
what's been decided and what's still open.

## Code of conduct

Be kind. Daimon is built out of love, not for money — we aim to keep the
project a pleasant place to spend time. The short version of the
[Contributor Covenant](https://www.contributor-covenant.org/) applies by
default: assume good faith, be respectful, focus on the work.

## Local setup

```sh
git clone https://github.com/regitxx/Daimon.git
cd Daimon
make build              # produces bin/daimon + bin/daimond
make test               # 356 Go test pass-lines, ~15 seconds
```

For the SDKs:

```sh
# Python SDK
cd sdk/python
pip install -e .[dev]
pytest -q               # 65 cases

# TypeScript SDK
cd sdk/typescript
npm install
npm test                # 65 cases via vitest
```

The full QUICKSTART walks the same flow plus the live x402 + audit chain
verification. End-to-end smoke is `bash examples/x402-smoke/run.sh` — runs
both SDKs against the mock x402 server in ~30 seconds and asserts wire-shape
parity. CI runs it on every push.

## How to propose a change

### Small fixes (typos, single-file improvements, obvious bugs)

Open a PR directly against `main`. Keep the title short, explain the *why*
in the description. The CI matrix runs Go (vet + race tests + build), four
Python versions (3.10–3.13), three Node versions (18, 20, 22), and the x402
cross-language smoke. All 9 shards must pass before merge.

### Larger changes (new RPC verbs, new chains, new SDK surface)

Open an issue first so the SPEC implications can be discussed before the
implementation lands. New wire-shape changes need:

1. A SPEC section (or amendment to an existing one) describing the verb,
   params, return shape, and typed error codes.
2. The Go reference implementation under `internal/server/` + handler tests.
3. Wrappers in BOTH SDKs (Python `sdk/python/daimon/client.py` + TS
   `sdk/typescript/src/client.ts`) with matching pytest + vitest cases
   driving them against the stub-daemon harness. Wire shape parity between
   the two SDKs is non-negotiable — the cross-language smoke catches drift.
4. CHANGELOG `[Unreleased]` entries in both SDKs if the change is on the
   public RPC / SDK surface.

### Cryptographic changes

Anything touching the keystore envelope (`internal/secretbox`,
`internal/identity/keystore.go`, `internal/wallet/keystore.go`), the
EIP-712 / EIP-3009 hashing (`internal/payment/eip712.go`), or the
audit-log chain integrity (`internal/activity/`) needs explicit issue
discussion before a PR. These layers have published test vectors (the
canonical 12-word BIP-39 seed → `0x9858EfFD…` address, the pinned
EIP-712 typehashes) — those tests are part of the protocol's contract,
not the reference impl's behavior.

## Test discipline

PRs that touch code SHOULD add or modify tests. The repo's test density
is intentional — the cross-language SDK suite + the live x402 smoke +
the cryptographic anchor tests are what make changes safe to land
without manual end-to-end verification.

The race detector (`go test -race`) and `go vet` both run in CI. The
TypeScript SDK has a `typecheck` step (`npm run typecheck`) chained
ahead of `prepublishOnly`. The Python SDK has a `gen_version.py` drift
check (so `pyproject.toml` and `daimon/_version.py` can never disagree
on the published version).

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/) if
you want, but it's not enforced. The repo's existing commit history is
prose-style with a clear subject line + a body that explains the *why*.
Match that.

PRs from AI-assisted workflows: surface the assistance honestly. The
reference daemon and SDKs were built with extensive AI pairing; we
don't pretend otherwise. Co-authorship trailers are up to you and your
co-author.

## Releases

Cutting a release is currently a maintainer task — see
[`PUBLISH.md`](./PUBLISH.md) for the exact ritual. The short version:

1. Bump `sdk/python/pyproject.toml` + `sdk/typescript/package.json` (the
   `gen_version.py` / `gen-version.mjs` scripts handle the runtime
   constants).
2. Move the SDK `[Unreleased]` CHANGELOG blocks under a dated section.
3. `npm publish --tag dev` (or `latest` for GA) + `twine upload` (use
   `--repository testpypi` for dry-runs).
4. `git tag vX.Y.Z[-dev.N]` + `git push --tags` — the release workflow
   at `.github/workflows/release.yml` fires on the tag push and produces
   the platform-binary tarballs + `checksums.txt` automatically.

## Governance

Daimon is licensed under [Apache 2.0](./LICENSE) and is structured for
eventual Linux Foundation handoff once adoption justifies it. The protocol
is the public good; anyone can implement it, no party owns it. We are
foundation- and grant-funded by intent, not VC-backed by intent.

## Questions

Open a [discussion](https://github.com/regitxx/Daimon/discussions) if
you have one. For security issues specifically, see [SECURITY.md](./SECURITY.md)
— don't file a public issue for a vulnerability.
