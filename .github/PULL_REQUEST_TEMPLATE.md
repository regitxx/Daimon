<!--
Thanks for opening a PR. The checklist below tracks what we care
about; not every PR will need every item. Strike through anything
N/A so reviewers can see you considered it.

Larger / SPEC-changing PRs: please open an issue first so the
direction can be debated before you wrote the code. See
CONTRIBUTING.md for the full rationale.
-->

## What does this PR do?

<!-- One paragraph: what changes, why, who benefits. -->

## How was it tested?

<!--
- [ ] `make test` green locally (or `go test -race ./... && (cd sdk/python && pytest -q) && (cd sdk/typescript && npm test)`)
- [ ] End-to-end smoke if the surface touched user-facing CLI / RPC
- [ ] `bash examples/x402-smoke/run.sh` if the wire shape changed
-->

## Checklist

- [ ] CI matrix is green (10 shards). Failures explained below.
- [ ] If this changes the RPC wire shape (new verb, new param, new error code): SPEC.md updated.
- [ ] If this touches a cryptographic layer (keystore, EIP-712/3009, audit chain hash chain): the change has been discussed in an issue first.
- [ ] If this changes the public SDK surface: both `sdk/python` and `sdk/typescript` are at parity, and the cross-language `examples/x402-smoke/run.sh` still passes.
- [ ] If this is on the SDK publish path: SDK `CHANGELOG.md` entries under `[Unreleased]` describe what changed.
- [ ] If this introduces a new dependency: it was reviewed for supply-chain risk and minimal scope. (Daimon's posture is "fewer deps better"; new transitive trees need justification.)

## Related issues / proposals

<!-- Link to any issue this PR resolves or design doc this PR implements. -->

## Anything reviewers should focus on?

<!-- "I'd especially like a second pair of eyes on the foo handler's
locking pattern" / "The new SQL migration is the riskiest part." -->
