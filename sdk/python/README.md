# daimon — Python SDK

Thin Python client over the Daimon daemon's Unix-socket JSON-RPC surface
(SPEC §6.1). Mirrors the Go `cmd/daimon` CLI's wire-level behaviour: one
connection per RPC, no pipelining, JSON-RPC 2.0.

> Status: v0.1.0 GA on `latest` (identity / memory / provider /
> activity verbs). v0.2.0-dev.2 pre-release on the `--pre` channel
> adds wallet + x402 payment verbs.

## Install

Default install — v0.1.0 GA:

```
pip install daimon-protocol
```

Pre-release install — v0.2.0-dev.2 with wallet + x402 payments:

```
pip install --pre daimon-protocol
```

From a checkout of the Daimon repo:

```
pip install -e sdk/python
```

> The PyPI distribution name is `daimon-protocol` because the
> unqualified `daimon` name on PyPI is held by an unrelated dormant
> package, and `daimon-sdk` / `daimon-client` belong to other active
> projects. The **import name remains `daimon`** — `from daimon import
> Client` works regardless of how you installed it.

## Use

The SDK assumes a running daimon daemon on the local machine, reachable via
the same socket path the Go CLI uses (`$DAIMON_HOME/daimon.sock`, with the
same long-path fallback rules). Start the daemon first:

```
daimon unlock
```

Then:

```python
from daimon import Client

client = Client()                                  # resolves $DAIMON_HOME
print(client.identity.get())                       # {"did": "did:key:..."}

# memory.Kind.Valid() accepts: "fact", "preference", "task", "observation"
mid = client.memory.write(kind="fact", content="the sky is blue")
print(mid)                                         # {"id": "01K..."}

mem = client.memory.read(mid["id"])
hits = client.memory.search("sky")                 # [{...mem, "score": 0.7}, ...]
all_mems = client.memory.list()                    # search with empty query

# provider verbs
providers = client.provider.list()
env = client.provider.invoke(
    provider="ollama",
    model="llama3.2:latest",
    messages=[{"role": "user", "content": "hi"}],
)
print(env["response"]["content"])

# streaming — yields delta strings; final envelope on .final
stream = client.provider.stream(
    provider="ollama",
    model="llama3.2:latest",
    messages=[{"role": "user", "content": "count to 3"}],
)
for delta in stream:
    print(delta, end="", flush=True)
print()
print("usage:", stream.final["response"]["usage"])

# activity verbs (audit trail)
client.activity.append(kind="custom.event", payload={"n": 1})
entries = client.activity.query(limit=20)
result = client.activity.verify()                  # {"verified": N, "ok": True}
```

## Wallet + payments (v0.2 pre-release)

Available in `0.2.0.dev2` (`pip install --pre daimon-protocol`). The
wallet keystore is auto-created by the daemon on first `daimon unlock`
— the 24-word BIP-39 mnemonic is surfaced exactly once in that
unlock's RPC response. Wallets are MetaMask-compatible: importing the
mnemonic into MetaMask reproduces the same address the daimon derived.

```python
# Derive a fresh EVM wallet for Base mainnet
w = client.wallet.create(chain="evm:base")
print(w["address"])  # 0x... (EIP-55 checksummed)

# List wallets in the keystore
for w in client.wallet.list():
    print(f"{w['chain']:20s} {w['address']}")

# Quick lookup by chain
addr = client.wallet.address(chain="evm:base")

# Compute the address that WOULD be derived at a given HD index
# WITHOUT persisting anything. Handy for verifying a recovered seed:
# `client.wallet.derive(chain="evm:base", index=0)["address"]`
# should match what MetaMask / Phantom shows for the same seed at
# the same path. No audit row, no wallet-list mutation.
predicted = client.wallet.derive(chain="evm:base", index=0)
print(predicted["address"], "at", predicted["path"])

# Re-display the BIP-39 mnemonic, password-gated. Returns the
# 24-word list. Useful for verifying the backup was written down
# correctly, or exporting the seed to MetaMask / Phantom / Rabby.
# Wrong password raises RPCError with code -32008 (CodeWrongPassword),
# distinct from -32001 (CodeIdentityLocked) — the daemon IS unlocked,
# the password check is a separate attestation.
words = client.wallet.show_mnemonic(password="my-unlock-password")
assert len(words) == 24

# Pay an x402-protected URL end-to-end. The daimon parses the
# 402's PAYMENT-REQUIRED header, signs EIP-3009 transferWithAuthorization
# with the matching wallet, and retries with PAYMENT-SIGNATURE.
# ceiling_smallest_unit caps the payment in USDC smallest-unit (6 dec):
# 100000 == $0.10.
resp = client.payment.pay(
    url="https://protected.example.com/api/data",
    method="POST",
    body=b'{"prompt": "hi"}',
    ceiling_smallest_unit=100_000,
)
print(resp["status_code"], resp["body"])
if resp["payment_response"]:
    pr = resp["payment_response"]
    print(f"settled: tx={pr['transaction']} payer={pr['payer']}")
```

The audit log gains `wallet.created`, `payment.signed`, and
`payment.settled` rows automatically — every wallet generation and
every payment chains into the same Ed25519-signed log that carries
the v0.1 memory and provider rows. Walk the whole chain with
`client.activity.verify()`.

Typed RPC error codes for the wallet + payment surface propagate
via `RPCError.code`:

- `-32006` — payment exceeds local ceiling. The daimon refused to
  sign; no on-the-wire signature was emitted.
- `-32007` — no wallet in the keystore matches the resource's
  PaymentRequirements (chain not in registry, or wallet for that
  chain not yet created).
- `-32008` — `show_mnemonic` was called with the wrong password.
  Distinct from `-32001` (CodeIdentityLocked) so callers can
  branch on it without their "daemon is locked, run `daimon
  unlock`" rewrite kicking in when really the user just mistyped
  the password.

See [`examples/x402-smoke`](../../examples/x402-smoke) for an
end-to-end runnable example against a local mock x402 server.

### Recovering a daimon from an existing seed

If you have a 24- (or 12-) word BIP-39 phrase already — a backup
you wrote down, or a seed you'd like to import from MetaMask /
Phantom / Rabby — the CLI `daimon wallet recover` writes a fresh
wallet keystore from that phrase. It's an offline-only operation,
so there's no SDK wrapper: the daimon daemon must be stopped (or
have never run against this `$DAIMON_HOME`) when you run it,
because a live seed swap on a running daemon would orphan every
wallet derived from the previous seed.

```sh
daimon wallet recover
# Recovery phrase: (hidden input, paste your 12 or 24 words)
# Choose a password: (must match your daimon unlock password)
# Confirm password:
# Wallet keystore written.
# Next: `daimon unlock` to bring up the daemon against this seed.
```

After recover, the daemon's next unlock loads the imported
keystore instead of generating a fresh mnemonic, and every wallet
you create with `client.wallet.create(...)` derives from the
imported seed. The canonical `abandon ... about` 12-word vector
produces `0x9858EfFD232B4033E47d90003D41EC34EcaEda94` at
`m/44'/60'/0'/0/0` — the same address every BIP-39 derivation
tool produces for that seed, so cross-wallet recovery is trivially
verifiable.

## Errors

```python
from daimon import DaemonNotRunning, DaemonLocked, RPCError

try:
    client.memory.write(kind="fact", content="x")
except DaemonNotRunning:
    # daimon binary isn't serving on this $DAIMON_HOME
    ...
except DaemonLocked:
    # daemon is running but `daimon unlock` hasn't been called
    ...
except RPCError as e:
    # any other JSON-RPC error from the daemon
    print(e.code, e.message, e.data)
```

## Development

```
cd sdk/python
pip install -e .[dev]
pytest
```

The test suite uses a stub Unix-socket daemon (no real keys, no real
storage) plus optional smoke tests against a live daimon when one is
running.

## See also

- [TypeScript SDK](../typescript) — sister SDK, same wire shape, async-iterator streaming surface.
- [`examples/streaming`](../../examples/streaming) — cross-language streaming reference: both SDKs round-trip token deltas through one daemon, audit chain verified three ways.
- [`CHANGELOG.md`](./CHANGELOG.md) — release notes per version.
