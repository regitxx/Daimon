# Security model, cryptographic choices, and known limitations

This document is the honest version. The policy for *reporting* a vulnerability lives at [/SECURITY.md](../SECURITY.md); the policy for *reasoning* about the system's security properties is here. The authoritative formal spec is [SPEC.md §9](../SPEC.md).

## 1. Threat model

### 1.1 In scope

Daimon defends against the following classes of attacker.

**(T1) Provider surveillance and lock-in.** No single provider sees the principal's full context. Memory stays local; identity is self-sovereign (`did:key`); conversation routing goes through the daemon, not the provider. The provider sees only the portion of the context that was injected for a given call.

**(T2) Physical / backup theft of files at rest.** An attacker who obtains `$DAIMON_HOME` without the password gets authenticated ciphertext only. All sensitive fields in the memory database, all activity log payloads, both the identity keystore and the wallet keystore, and the provider credentials are AEAD-encrypted. The password is the single secret; it is Argon2id-stretched (memory ≥ 64 MiB, iterations ≥ 3, parallelism ≥ 4) before being used as the KEK, which puts offline guessing in the "very expensive per guess" regime rather than the "free" one. A cold laptop with no password is safe; a warm laptop with the daemon unlocked is not (see §1.2, T7).

**(T3) Tampering with memory or with the activity log.** Every memory row is signed by the identity Ed25519 key over `(id ‖ content ‖ metadata)`. Every activity entry is hash-chained with BLAKE3 and Ed25519-signed, with the hash committing to the plaintext canonical form of the entry. Three tamper modes are detected deterministically by `daimon activity verify` (`ErrInvalidCiphertext` on AEAD failure, `ErrHashMismatch` on rehash divergence, `ErrChainBroken` on `prev_hash` divergence). A silent rewrite of the log is not possible without either the identity private key or a hash-collision in BLAKE3 or Ed25519.

**(T4) Unauthorized RPC access.** The Unix socket is mode 0600, owner-only. State-modifying verbs are gated behind `identity.unlock`; a `CodeIdentityLocked` error surfaces to clients that attempt them before unlock. The unlock gate is a single choke point (`internal/server/jsonrpc.go`) rather than a scattered check.

**(T5) Payment ceiling bypass.** A per-call `ceiling_smallest_unit` is enforced *before* EIP-712 signing. If the manifest amount exceeds the ceiling, the daemon returns a typed `-32006 CodePaymentCeiling` error and no signature is produced on the wire. The invariant stated in positive form: `activity.log` can never contain a `payment.signed` row whose amount exceeds the ceiling that was in effect for that call.

**(T6) EIP-712 domain substitution.** The daemon signs against pinned domain and `transferWithAuthorization` typehashes that are part of the published protocol surface and are asserted in `internal/payment/eip712_test.go`. Substituting a different chain, contract, or verifying address changes the typed data hash and the signature does not verify against the intended network.

**(T7) SDK wire divergence corrupting the audit chain.** The two first-party SDKs are kept at full RPC parity by a cross-language smoke test (`examples/x402-smoke/run.sh`) that runs on every CI push. The mock facilitator in that smoke cryptographically verifies the EIP-3009 signature recovers to the wallet address; a drift between the Python and TypeScript EIP-712 encoders fails immediately.

**(T8) CI / supply-chain attacks on the release pipeline.** Published binaries carry a SHA-256 in `checksums.txt` on the GitHub Release; `install.sh` verifies the checksum before execution. The release workflow injects version via ldflags, and the `install-script` CI shard runs the installer against the published release on both Ubuntu and macOS on every push, so a broken release artefact or a broken installer surfaces within minutes.

### 1.2 Explicitly out of scope

Three shaped categories of risk are out of scope. Being explicit is the point.

**(T9) In-process compromise of the daemon.** If an attacker can execute code inside `daimond`, the unlocked identity private key, the memory subkey, the activity subkey, and the wallet mnemonic are all exposed. Daimon relies on OS-layer process isolation (process credentials, kernel permission enforcement) and does not try to defend against an in-process attacker. TPM-backed attestation is a possible v0.2+ hardening; it is not in the current build.

**(T10) Malicious providers returning poisoned responses.** The daemon encrypts at rest; it does not try to make inference by a chosen provider private from that provider. A user routing requests through Anthropic or OpenAI tells those providers whatever sits in the prompt. Local providers (Ollama, LM Studio) mitigate this but at the cost of model quality; the mitigation is architectural (provider choice is the user's lever), not cryptographic.

**(T11) Lost password / lost mnemonic.** There is no recovery path by design. The keystore + password pair is the only decryption capability; the wallet keystore + mnemonic is the only wallet recovery capability. A principal who loses both is unrecoverable. The alternative — a key-escrow path — would reintroduce a trusted third party, which is the entire shape of problem Daimon exists to avoid.

**(T12) OS-level compromise.** Daimon is the user-owned layer; it does not substitute for full-disk encryption, a non-malicious kernel, a correct implementation of the OS's filesystem permission enforcement, or the user's choice of screen-lock discipline. The file mode checks the daemon performs on its own state are a defence against trivial misconfiguration, not against a rootkit.

**(T13) Side-channel attacks on the daemon's cryptographic operations.** Constant-time implementations across every transitive dependency are not audited. The daemon's AEAD, Argon2id, Ed25519, and BIP-32 paths go through Go's standard library and well-regarded third-party packages, but a side-channel-aware adversary with physical access to a warm, unlocked machine is outside the model.

**(T14) Transient password exposure over the unlock IPC channel.** The `daimon unlock` CLI sends the keystore password over the Unix socket to `daimond serve`. Although the socket is mode 0600, the password is briefly readable via `/proc/<daimond>/fd` or by a `ptrace`/`strace` on the daemon — which is to say, by anything that has already satisfied T9 above. The planned mitigation is for the CLI to read the password from the controlling terminal and forward an unlock *token* instead of the password itself; this lands in v0.1.x. The present carve-out is documented so a reader can judge for themselves whether it is load-bearing for their deployment.

## 2. Cryptographic choices

Each primitive below is chosen to satisfy one or more of the in-scope threats above. The "why" column answers the obvious follow-up ("why this, not that").

| Purpose | Primitive | Why |
|---|---|---|
| Identity signatures | Ed25519 (`crypto/ed25519`) | Small keys, fast verification, no ambient configuration. The `did:key` method is standardised over this curve with a clean multibase encoding. |
| Content hashing | BLAKE3 (`lukechampine.com/blake3`) | Fast on commodity hardware, extensively analyzed, drop-in for SHA-2. Used for the activity log chain and the memory row identifier. |
| Password → KEK | Argon2id (`golang.org/x/crypto/argon2`) | Memory-hard. Parameters (`memory_kib=65536, iterations=3, parallelism=4`) sit above OWASP's 2024 baseline. Parameter record is stored in the keystore envelope so future hardening can bump the floor. |
| AEAD at rest | AES-256-GCM via `internal/secretbox.NewAEAD` | Standard, fast, hardware-accelerated on virtually every modern CPU. One AEAD module shared across all four at-rest sites (identity keystore, wallet keystore, memory rows, activity payloads). |
| Memory row AAD | `"daimon-memory-row-v1" ‖ 0x00 ‖ row_id ‖ 0x00 ‖ field_name` | Binds each ciphertext to its row + column. A stolen ciphertext cannot be silently moved onto another row or into another field. |
| Activity payload AAD | `"daimon-activity-payload-v1" ‖ 0x00 ‖ entry_id ‖ 0x00 ‖ "payload"` | Same construction, binds each ciphertext to its entry id. A reparented ciphertext fails authentication before the hash check runs. |
| Subkey derivation | HKDF-SHA256 (`golang.org/x/crypto/hkdf`) with domain-separated info labels | Independent subkeys for memory (`daimon-memory-encryption-v1`) and activity (`daimon-activity-encryption-v1`) derive from the same Ed25519 seed. Compromise of one subkey does not weaken the other. |
| Wallet seed | BIP-39 (24 words, 256 bits of entropy) | 256 bits of entropy comfortably exceeds the 128-bit security target. The word list is interoperable with every standard EVM wallet. |
| HD derivation | BIP-32 along `m/44'/60'/0'/0/i` with secp256k1 (`github.com/tyler-smith/go-bip32`) | Standard EVM derivation. A user's mnemonic imports cleanly into MetaMask, Rabby, and the rest; conversely, a MetaMask-generated phrase imports cleanly into the daimon via `daimon wallet recover`. |
| Payment typed-data | EIP-712 for EIP-3009 `transferWithAuthorization` | The x402 facilitator path. Domain and function typehashes are pinned and asserted (§3 below). |
| Federation handshake | Noise IK over TCP (`github.com/flynn/noise`) | Mutual auth with known responder, forward secrecy, 1-RTT. The responder's identity Ed25519 key is converted to X25519 for transport authentication; one keypair authenticates identity, audit log, and transport. |
| Capability delegation | Biscuit v3 (`github.com/biscuit-auth/biscuit-go/v2`) | Offline attenuation (a holder can tighten without issuer contact), Datalog-authorized verification, deterministic revocation at the issuer. The alternative macaroon design would reduce to the same algebra with fewer existing libraries. |

Public constants that are part of the protocol surface and MUST NOT change without a major version bump:

- **BIP-39 / BIP-32 EVM derivation vector.** The canonical phrase

  > `abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about`

  at path `m/44'/60'/0'/0/0` derives to `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`. Pinned in [`internal/wallet/wallet_test.go::TestDerive_EVMAddressMatchesPublishedVector`](../internal/wallet/wallet_test.go) and SPEC §14.5.

- **EIP-712 domain typehash.** `0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f`. Pinned in [`internal/payment/eip712.go`](../internal/payment/eip712.go) and SPEC §15.3.

- **EIP-3009 `transferWithAuthorization` typehash.** `0x7c7c6cdb67a18743f49ec6fa9b35f50d52ed05cbed4cc592e13b44501c1a2267`. Pinned alongside the domain typehash.

A vulnerability report whose premise is one of these values being different from what is documented should be double-checked before being filed; the test asserts the value on every push.

## 3. What anyone can verify from outside

A third party with access to the repository and the running daemon can reproduce the following without the principal's secrets:

- **Build the daemon from source and confirm the binary matches the published release.** `make build` produces `bin/daimond` and `bin/daimon`; the ldflags-injected `version` string matches what `daimon --help` reports. Published checksums are in `checksums.txt` on each GitHub Release, and `install.sh` enforces them on install.
- **Run the full test suite and confirm the pinned cryptographic anchors.** `make ci-local` runs Go + Python + TypeScript + the cross-language x402 smoke. The three anchors in §2 above are asserted by named tests.
- **Walk an activity log without unlock.** The on-disk JSONL has `id`, `ts`, `kind`, `prev_hash`, `hash`, and `signature` in the clear. A verifier with the daimon's public key can confirm every entry's signature is valid and the hash chain is unbroken, without decrypting payloads. `daimon activity verify --no-unlock` (where supported) is the structural check; the payload-level check requires the password.
- **Replay an x402 payment manifest against the EIP-712 encoder.** Given a manifest and the wallet address, the EIP-712 TypedData hash is deterministic. The `examples/x402-smoke` harness performs exactly this check across two languages.

## 4. Known limitations (honest accounting)

Items that are real, documented, and unresolved as of v0.4 pre-release.

1. **Transient password exposure (T14).** Still present. Mitigation deferred to v0.1.x. Documented in §1.2 above and SPEC §9.2.
2. **No formal cryptographic audit.** The construction is composed of widely-analyzed primitives in their intended modes, but no external audit has been commissioned. For a self-funded project at this scale, that is honest about what the testing + CI matrix does and does not substitute for.
3. **`did:ion` is specified but not implemented.** Key rotation under a stable DID requires it; without it, `did:key` rotation is "a new DID is a new identity." Not a cryptographic weakness, a product gap.
4. **`internal/reputation` has no direct test file.** The package is exercised by `internal/server`'s peer.ask tests, including signature verification end-to-end, but a dedicated unit test file on the receipt signing/verification surface would make the coverage discoverable without reading the server tests.
5. **v0.2 GA is gated on an external integration.** The cryptographic surface for x402 payments is self-tested by the CI smoke shard against a mock facilitator; the live on-chain settle step against Base Sepolia with a real facilitator has not been exercised as of the current pre-release. The gap is operational, not cryptographic.
6. **No external supply-chain attestation yet** (SLSA provenance, Sigstore signing). Reproducible-build posture is good — Go binaries + ldflags version injection + CI-published checksums — but the next step is attested provenance per the SLSA framework.
7. **Side-channel resistance is not audited** (T13).
8. **The address-book TOFU pin is first-use.** A pubkey rotation requires the principal to re-pin. An MITM at the *first* dial would pin the attacker's key. In practice daimons exchange DIDs out-of-band (business cards, QR codes, published `did:web` documents); an attacker would have to compromise the out-of-band channel *and* be in the network path on first dial. Still: it is TOFU, not PKI.
9. **No Hardware Security Module backing.** Keys live in process memory (locked under `mlock` where the OS permits). A future v0.x may integrate with the system keychain / TPM for the KEK; today the KEK is derived in-process and zeroed after use, which is the best we can do without an HSM dependency.

## 5. Reporting a vulnerability

Via GitHub Private Vulnerability Reporting at [github.com/regitxx/Daimon/security/advisories/new](https://github.com/regitxx/Daimon/security/advisories/new). The response timeline, scope, and acknowledgement policy are in the root [SECURITY.md](../SECURITY.md).
