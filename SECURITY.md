# Security Policy

## Reporting a vulnerability

**Do not file a public GitHub issue for a security vulnerability.** Public
issues are indexed by search engines + visible to anyone watching the
repository, which can give an attacker more lead time than the fix
deserves.

Use **[GitHub Private Vulnerability Reporting](https://github.com/regitxx/Daimon/security/advisories/new)**
instead. That channel is encrypted in transit, scoped to maintainers, and
gives us a tracked timeline for acknowledgement → triage → fix → public
disclosure.

If you can't use GitHub for whatever reason, email
[@regitxx](https://github.com/regitxx) directly via the contact
information on his GitHub profile. Acknowledgement within 72 hours under
normal conditions.

## What's in scope

In-scope vulnerabilities include:

- Anything that lets an attacker read or modify a daimon's identity,
  memory, activity log, or wallet keystore — including bypassing the
  Argon2id + AES-256-GCM at-rest encryption, the per-row AAD binding,
  or the Ed25519 signature chain
- Anything that lets an attacker exfiltrate a private key, mnemonic, or
  password from a running daemon
- Anything that lets an attacker forge an activity-log entry (including
  appending an entry that passes `daimon activity verify`) or break
  the hash chain
- Anything that lets an unauthenticated caller invoke RPCs that should
  require unlock (the `CodeIdentityLocked` gate at
  `internal/server/jsonrpc.go`)
- Anything that lets an attacker bypass the per-call `ceiling_smallest_unit`
  on x402 payments and produce a signature that exceeds it
- Anything that lets an attacker substitute the EIP-712 domain for a
  different chain/contract and have the daimon sign a transferWithAuthorization
  the user didn't intend
- Wire-shape divergences between the Python and TypeScript SDKs that
  let a daimon's audit chain be silently corrupted depending on which
  SDK wrote it
- CI / supply-chain attacks on the release pipeline — bad ldflags, bad
  checksums, malicious tags

## What's not in scope (v0.1 + v0.2)

Per [SPEC §9.2](./SPEC.md#92-threats-out-of-scope-v01), the following
are explicitly NOT in our threat model for the current pre-1.0 phase:

- **Compromise of the daemon process itself**. If a malicious actor has
  arbitrary code execution inside `daimond`, the unlocked keys are
  already exposed. We rely on OS-layer process isolation; we don't
  defend against in-process attackers.
- **Malicious LLM providers**. If the user routes invocations through
  Anthropic or OpenAI, those providers see the prompts. We don't try to
  prevent that — daimon's job is to encrypt *at rest*, not to hide
  inference from the inference provider. (Local providers like Ollama
  + LM Studio mitigate this; provider choice is the user's lever.)
- **Loss of the password / mnemonic on the user side**. No recovery path
  exists by design — losing both the keystore file AND the password,
  or both the wallet keystore AND the mnemonic, is unrecoverable.
- **The user's machine being compromised at the OS layer** (kernel
  rootkit, disk encryption disabled, etc.). Daimon is the user-owned
  layer; we don't try to substitute for full-disk encryption or the
  OS security model underneath us.
- **Side-channel attacks** (timing, power, EM) against the daemon's
  cryptographic operations. Mitigating these requires constant-time
  implementations across every dependency we pull in, which we don't
  attempt at the current scale.

Reports of out-of-scope issues will be acknowledged, but we may close
them without a fix if they require changes to the architectural model
described in SPEC §9.

## Response timeline (target)

- **72 hours**: acknowledgement of report receipt
- **2 weeks**: initial triage + severity assessment, scoping conversation
  with the reporter if needed
- **30 days**: fix landed in `main` for high-severity issues, or a clear
  written rationale for why it's not high-severity / not exploitable
- **90 days**: coordinated public disclosure (we publish a GitHub
  Security Advisory + CHANGELOG entry; you get credit in the advisory
  unless you ask not to be named)

The 90-day window is the upper bound, not the goal. For exploitable
high-severity issues we expect to coordinate disclosure on a much
faster timeline.

## Supported versions

| Version | Supported? |
|---|---|
| v0.1.x (GA) | ✅ — security fixes backported, published as patch releases |
| v0.2.0-dev.N | ✅ — fixes land in the dev track + roll into the next pre-release / GA |
| pre-v0.1.0 dev tags | ❌ — upgrade to v0.1.0 or later |

v0.2.0 GA, when it lands, will become a supported line in addition to
v0.1.x. Older lines reach end-of-support at our discretion; the policy
gets formalised at v1.0 alongside the foundation handoff.

## Cryptographic anchors (for verification)

The following constants are part of the protocol's published surface and
should NOT change without a major version bump:

- **BIP-39 / BIP-32 EVM derivation**: the canonical 12-word vector
  `abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about`
  at path `m/44'/60'/0'/0/0` derives to
  `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`. Pinned in
  `internal/wallet/wallet_test.go::TestDerive_EVMAddressMatchesPublishedVector`
  and SPEC §14.5.
- **EIP-712 domain typehash**:
  `0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f`
  (pinned in `internal/payment/eip712.go` and SPEC §15.3).
- **EIP-3009 `transferWithAuthorization` typehash**:
  `0x7c7c6cdb67a18743f49ec6fa9b35f50d52ed05cbed4cc592e13b44501c1a2267`
  (pinned in `internal/payment/eip712.go` and SPEC §15.3).

A vulnerability report that hinges on one of these values being
different from what's documented is an immediate red flag — please
double-check before reporting.

## Acknowledgements

Reporters who help fix security issues will be acknowledged in the
relevant GitHub Security Advisory (with their consent) and in the
release CHANGELOG. Daimon does not currently have a bug-bounty program;
this is a volunteer-led project and we can't fund payouts. We can
offer recognition + a thank-you in the JOURNAL.
