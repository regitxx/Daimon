# daimon — TypeScript SDK

Thin TypeScript client over the Daimon daemon's Unix-socket JSON-RPC
surface (SPEC §6.1). Mirrors the Go `cmd/daimon` CLI and the Python SDK's
wire-level behaviour: one connection per RPC, no pipelining, JSON-RPC 2.0.

> Status: v0.1.0 GA on the `latest` tag (identity / memory / provider /
> activity verbs). v0.2.0-dev.0 pre-release on the `dev` tag adds
> wallet + x402 payment methods. Full RPC parity with the Python SDK.

## Install

Default install — v0.1.0 GA:

```
npm install @daimon-protocol/sdk
```

Pre-release install — v0.2.0-dev.0 with wallet + x402 payments:

```
npm install @daimon-protocol/sdk@dev
```

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
import { Client } from "@daimon-protocol/sdk";

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

// Streaming — async-iterable of delta strings; terminal envelope on .final
const stream = await client.provider.stream({
  provider: "ollama",
  model: "llama3.2",
  messages: [{ role: "user", content: "count to 3" }],
});
for await (const delta of stream) {
  process.stdout.write(delta);
}
console.log();
console.log("usage:", (stream.final as { response: { usage: unknown } }).response.usage);

await client.activity.append({ kind: "custom.event", payload: { n: 1 } });
const entries = await client.activity.query({ limit: 20 });
const { verified, ok } = await client.activity.verify() as {
  verified: number;
  ok: boolean;
};
```

## Wallet + payments (v0.2 pre-release)

Available in `0.2.0-dev.0` (`npm install @daimon-protocol/sdk@dev`).
The wallet keystore is auto-created by the daemon on first `daimon
unlock` — the 24-word BIP-39 mnemonic is surfaced exactly once in
that unlock's RPC response. Wallets are MetaMask-compatible:
importing the mnemonic into MetaMask reproduces the same address
the daimon derived.

```ts
// Derive a fresh EVM wallet for Base mainnet
const w = await client.wallet.create({ chain: "evm:base" });
console.log(w.address); // "0x..." (EIP-55 checksummed)

// List wallets in the keystore
for (const w of await client.wallet.list()) {
  console.log(`${w.chain.padEnd(20)} ${w.address}`);
}

// Quick lookup by chain
const addr = await client.wallet.address({ chain: "evm:base" });

// Pay an x402-protected URL end-to-end. The daimon parses the
// 402's PAYMENT-REQUIRED header, signs EIP-3009 transferWithAuthorization
// with the matching wallet, and retries with PAYMENT-SIGNATURE.
// ceilingSmallestUnit caps the payment in USDC smallest-unit (6 dec):
// 100_000n == $0.10.
const resp = await client.payment.pay({
  url: "https://protected.example.com/api/data",
  method: "POST",
  body: new TextEncoder().encode('{"prompt": "hi"}'),
  ceilingSmallestUnit: 100_000n,
});
console.log(resp.statusCode, new TextDecoder().decode(resp.body));
if (resp.paymentResponse) {
  const pr = resp.paymentResponse;
  console.log(`settled: tx=${pr.transaction} payer=${pr.payer}`);
}
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

```ts
import { DaemonNotRunning, DaemonLocked, RPCError } from "@daimon-protocol/sdk";

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
storage); 47 cases mirror the Python SDK's pytest coverage byte-for-byte
on the wire shape, including the streaming notification + terminal-frame
protocol.

## See also

- [Python SDK](../python) — sister SDK, same wire shape, generator-based streaming surface.
- [`examples/streaming`](../../examples/streaming) — cross-language streaming reference: both SDKs round-trip token deltas through one daemon, audit chain verified three ways.
- [`CHANGELOG.md`](./CHANGELOG.md) — release notes per version.
