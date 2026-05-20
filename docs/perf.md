# Performance baselines

This document records measured performance baselines for the
CPU-bound hot paths in Daimon's reference implementation. Use it as
a regression catch — if a future refactor changes one of these
numbers by >2×, that's a signal worth investigating.

**Regenerate this document via:**

```sh
make bench
```

…then update the "Measured baselines" section below by hand with the
new numbers + hardware notes. The benchmarks themselves live in
`internal/{identity,secretbox,wallet,payment}/bench_test.go`.

## What's measured

Five operations dominate user-visible latency in v0.1 + v0.2:

| Operation | Where | When user pays this cost |
|---|---|---|
| Argon2id KDF + AES-GCM decrypt | `identity.LoadFromKeystore` | Every `daimon unlock` (cold start) |
| 3× Argon2id (load + reseal + verify) | `identity.RotatePassword` | Every `daimon rotate-password` |
| AES-256-GCM seal (256 B + AAD) | `secretbox.SealAAD` | Every `memory.write`, every activity-log row |
| AES-256-GCM open (256 B + AAD) | `secretbox.OpenAAD` | Every `memory.read`, every `activity.verify` walk |
| BIP-32 + secp256k1 + Keccak + EIP-55 | `wallet.DeriveAddress` | Every `wallet.create` + every `wallet.derive` |
| EIP-712 typed-data digest | `payment.EIP3009Digest` | Every `payment.pay` |

The benchmarks exclude IO + socket round-trip + JSON parse, which
are negligible in the steady state but variable across machines.
The full `daimon unlock` latency (cold-start, including daemon
spawn) lands at ~30ms KDF + ~5ms spawn + ~5ms socket dial ≈ 40ms
end-to-end on the reference hardware.

## Measured baselines

**Measured:** 2026-05-20
**Hardware:** Apple M5 Pro (16-core), macOS, Go 1.25
**Daimon version:** `v0.2.0-dev.3-19-g422cbf5-dirty`
**Command:** `make bench` (equivalent to `go test -bench=. -benchtime=2s -run=^$ ./internal/{identity,secretbox,wallet,payment}/`)

| Benchmark | ns/op | Throughput | Notes |
|---|---:|---:|---|
| `identity.LoadFromKeystore` | 33,103,672 | — | One Argon2id (64 MiB, 3 iter, 4 lanes) + AES-GCM open. Dominant cost of `daimon unlock`. |
| `identity.RotatePassword` | 95,415,382 | — | ~3× LoadFromKeystore (verify-old + reseal + paranoia-verify-new). Cost of `daimon rotate-password` for identity keystore alone; wallet rotation adds another ~95 ms. |
| `secretbox.SealAAD` (256 B) | 647 | 395 MB/s | One memory-row encrypt. |
| `secretbox.OpenAAD` (256 B) | 305 | 840 MB/s | One memory-row decrypt. Faster than seal because seal generates a fresh nonce via `rand.Read`. |
| `wallet.DeriveAddress` (index 0) | 5,463,203 | — | BIP-32 walk + secp256k1 scalar mult + Keccak256 + EIP-55 checksumming. |
| `wallet.DeriveAddress` (index 100) | 5,206,916 | — | Same shape — BIP-32 cost is constant in path depth, not index value. |
| `payment.EIP3009Digest` | 2,186 | — | Domain separator + struct hash + final EIP-712 keccak. |
| `payment.DomainSeparator` | 1,108 | — | Just the domain separator step — about half of EIP3009Digest's total cost. |

## What these numbers mean for users

**`daimon unlock`** spends about 33 ms in Argon2id. That's
deliberate — Argon2id at our parameters (64 MiB / 3 iter / 4 lanes)
is calibrated to take ~30 ms on a modern CPU, which is the right
trade-off between "imperceptible to the user" and "expensive enough
to defeat a brute-force attacker." Faster would weaken the KDF;
slower would frustrate the user. If you see this number drop to
single-digit ms after a refactor, **the KDF parameters were
accidentally weakened**.

**`daimon rotate-password`** is ~95 ms for identity alone, ~190 ms
when wallet keystore also exists. Acceptable for an interactive
"rotate my password, prompt me twice for confirmation" flow — the
human-perceived cost is dominated by the typing, not the KDF.

**`memory.write` / `memory.read`** spend sub-microsecond CPU time
on the AEAD layer. At 256 bytes/row, you'd need ~1.5 million
operations per second to saturate the encryption layer. The actual
ceiling is the SQLite write throughput (much lower) + the
embedding-model call latency (much, much lower). Not a bottleneck
at v0.2 scale.

**`wallet.create` / `wallet.derive`** spend ~5 ms per call. For
`payment.pay` (one derivation per signing call), this is invisible.
For someone doing bulk derivation — say, computing addresses for
1000 indices to find one with a vanity prefix — it's 5 seconds.
Acceptable but worth knowing.

**`payment.pay`** spends ~2 µs on the EIP-712 digest. The actual
payment latency is dominated by the two HTTP round-trips
(probe + signed retry) — typically 100-500 ms against a real
endpoint. The crypto is noise.

## Limitations

- **Single-machine baselines.** Run on one piece of hardware in one
  thermal envelope. Reproducible on the same machine; comparable to
  other machines only in relative terms ("Argon2id is the dominant
  unlock cost everywhere").
- **No I/O.** `daimon unlock`'s real cost includes spawning daimond,
  opening the Unix socket, JSON-RPC parsing, and an unlock call.
  Those are all sub-millisecond in the steady state but vary widely.
- **No tracking over time.** This is a one-shot snapshot. Future
  contributors should re-run `make bench` and update the table when
  a meaningful change lands; CI doesn't enforce regressions.
- **No memory profiling.** `b.ReportAllocs` could be added for the
  AEAD path if alloc count starts mattering, but for v0.2 the
  numbers above are throughput-focused.

## What's NOT yet benchmarked

- HTTP client overhead in `payment.Client.Do` (would need a mock
  facilitator)
- SQLite memory-row read/write throughput (would need a real
  populated store)
- Activity-log append + verify-chain walk at scale (only meaningful
  with thousands of entries)
- LM Studio / Ollama / Claude / OpenAI streaming adapter throughput
  (provider-side bottleneck dominates)

If you hit a real-world perf issue in one of these paths, the right
move is to add a focused benchmark first, then optimise — not to
guess what's slow. The five operations above were chosen because
they're CPU-bound, predictable, and impossible to escape from
without rearchitecting; the unmeasured paths are I/O-bound or
provider-bound and benchmarking them in isolation tells you less
than profiling a real workload.
