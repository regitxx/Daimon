# daimon — TypeScript SDK

Thin TypeScript client over the Daimon daemon's Unix-socket JSON-RPC
surface (SPEC §6.1). Mirrors the Go `cmd/daimon` CLI and the Python SDK's
wire-level behaviour: one connection per RPC, no pipelining, JSON-RPC 2.0.

> Status: v0.1.0 GA on the `latest` tag (identity / memory / provider /
> activity verbs). v0.2.0-dev.1 pre-release on the `dev` tag adds
> wallet + x402 payment methods. Full RPC parity with the Python SDK.

## Install

Default install — v0.1.0 GA:

```
npm install @daimon-protocol/sdk
```

Pre-release install — v0.2.0-dev.1 with wallet + x402 payments:

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

Available in `0.2.0-dev.1` (`npm install @daimon-protocol/sdk@dev`).
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

// Compute the address that WOULD be derived at a given HD index
// WITHOUT persisting anything. Handy for verifying a recovered seed:
// `(await client.wallet.derive({chain: "evm:base", index: 0})).address`
// should match what MetaMask / Phantom shows for the same seed at
// the same path. No audit row, no wallet-list mutation.
const predicted = await client.wallet.derive({ chain: "evm:base", index: 0 });
console.log(predicted.address, "at", predicted.path);

// Re-display the BIP-39 mnemonic, password-gated. Returns the
// 24-word list. Useful for verifying the backup was written down
// correctly, or exporting the seed to MetaMask / Phantom / Rabby.
// Wrong password throws RPCError with code -32008 (CodeWrongPassword),
// distinct from -32001 (CodeIdentityLocked) — the daemon IS unlocked,
// the password check is a separate attestation.
const words = await client.wallet.showMnemonic({ password: "my-unlock-password" });
console.log(words.length); // 24

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

Typed RPC error codes for the wallet + payment surface propagate
via `RPCError.code`:

- `-32006` — payment exceeds local ceiling. The daimon refused to
  sign; no on-the-wire signature was emitted.
- `-32007` — no wallet in the keystore matches the resource's
  PaymentRequirements (chain not in registry, or wallet for that
  chain not yet created).
- `-32008` — `showMnemonic` was called with the wrong password.
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
