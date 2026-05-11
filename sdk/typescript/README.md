# daimon — TypeScript SDK

Thin TypeScript client over the Daimon daemon's Unix-socket JSON-RPC
surface (SPEC §6.1). Mirrors the Go `cmd/daimon` CLI and the Python SDK's
wire-level behaviour: one connection per RPC, no pipelining, JSON-RPC 2.0.

> Status: v0.1.0-dev.0 — alpha. Identity, memory, provider, and activity
> verbs implemented (non-streaming surface). Provider streaming
> (`daimon.provider.stream`) lands in a later session.

## Install

From a checkout of the Daimon repo:

```
cd sdk/typescript
npm install
npm run build
```

## Use

The SDK assumes a running daimon daemon on the local machine, reachable
via the same socket path the Go CLI uses (`$DAIMON_HOME/daimon.sock`,
with the same long-path fallback rules). Start the daemon first:

```
daimon unlock
```

Then:

```ts
import { Client } from "@daimon/sdk";

const client = new Client();                                // resolves $DAIMON_HOME
console.log(await client.identity.get());                   // { did: "did:key:..." }

const { id } = await client.memory.write({
  kind: "fact",
  content: "hello world",
});
const mem = await client.memory.read(id as string);
const hits = await client.memory.search("hello");           // [{...mem, score}, ...]
const all = await client.memory.list();                     // search with empty query

const providers = await client.provider.list();
const env = await client.provider.invoke({
  provider: "ollama",
  model: "llama3.2",
  messages: [{ role: "user", content: "hi" }],
});

await client.activity.append({ kind: "custom.event", payload: { n: 1 } });
const entries = await client.activity.query({ limit: 20 });
const { verified, ok } = await client.activity.verify() as {
  verified: number;
  ok: boolean;
};
```

## Errors

```ts
import { DaemonNotRunning, DaemonLocked, RPCError } from "@daimon/sdk";

try {
  await client.memory.write({ kind: "fact", content: "x" });
} catch (e) {
  if (e instanceof DaemonNotRunning) {
    // daimon binary isn't serving on this $DAIMON_HOME
  } else if (e instanceof DaemonLocked) {
    // daemon is running but `daimon unlock` hasn't been called
  } else if (e instanceof RPCError) {
    console.error(e.code, e.rpcMessage, e.data);
  }
}
```

## Development

```
cd sdk/typescript
npm install
npm test         # vitest
npm run typecheck
npm run build
```

The test suite uses a stub Unix-socket daemon (no real keys, no real
storage); 35 cases mirror the Python SDK's pytest coverage byte-for-byte
on the wire shape.
