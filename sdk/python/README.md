# daimon — Python SDK

Thin Python client over the Daimon daemon's Unix-socket JSON-RPC surface
(SPEC §6.1). Mirrors the Go `cmd/daimon` CLI's wire-level behaviour: one
connection per RPC, no pipelining, JSON-RPC 2.0.

> Status: v0.1.0.dev0 — alpha. Identity + memory verbs only. Provider /
> activity / streaming come in subsequent SDK sessions.

## Install

From a checkout of the Daimon repo:

```
pip install -e sdk/python
```

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

mid = client.memory.write(kind="note", content="hello world")
print(mid)                                         # {"id": "01K..."}

mem = client.memory.read(mid["id"])
hits = client.memory.search("hello")               # [{...mem, "score": 0.7}, ...]
all_mems = client.memory.list()                    # search with empty query
```

## Errors

```python
from daimon import DaemonNotRunning, DaemonLocked, RPCError

try:
    client.memory.write(kind="note", content="x")
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
