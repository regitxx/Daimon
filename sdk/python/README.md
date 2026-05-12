# daimon — Python SDK

Thin Python client over the Daimon daemon's Unix-socket JSON-RPC surface
(SPEC §6.1). Mirrors the Go `cmd/daimon` CLI's wire-level behaviour: one
connection per RPC, no pipelining, JSON-RPC 2.0.

> Status: v0.1.0 — first GA release. Identity, memory, provider (list /
> invoke / stream), and activity verbs all surfaced.

## Install

From PyPI:

```
pip install daimon-protocol
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
