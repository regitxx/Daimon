# daimon — Python SDK

Thin Python client over the Daimon daemon's Unix-socket JSON-RPC surface
(SPEC §6.1). Mirrors the Go `cmd/daimon` CLI's wire-level behaviour: one
connection per RPC, no pipelining, JSON-RPC 2.0.

> Status: v0.1.0 GA on `latest` (identity / memory / provider /
> activity verbs). v0.2.0-dev.0 pre-release on the `--pre` channel
> adds wallet + x402 payment verbs.

## Install

Default install — v0.1.0 GA:

```
pip install daimon-protocol
```

Pre-release install — v0.2.0-dev.0 with wallet + x402 payments:

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

Available in `0.2.0.dev0` (`pip install --pre daimon-protocol`). The
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

Typed RPC error codes for the payment surface propagate via
`RPCError.code`:

- `-32006` — payment exceeds local ceiling. The daimon refused to
  sign; no on-the-wire signature was emitted.
- `-32007` — no wallet in the keystore matches the resource's
  PaymentRequirements (chain not in registry, or wallet for that
  chain not yet created).

See [`examples/x402-smoke`](../../examples/x402-smoke) for an
end-to-end runnable example against a local mock x402 server.

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
