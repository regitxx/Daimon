/**
 * Daimon — TypeScript SDK x402 cross-language smoke (TS half).
 *
 * Sibling of python_smoke.py — pays the same mock x402 endpoint
 * through the TS SDK's client.payment.pay surface against the same
 * running daimon. Together they prove the wire-shape parity between
 * the two SDKs' EIP-3009 encoders.
 *
 * Imports from the locally-built dist so this works against the
 * repo checkout without needing the package published to npm. If
 * you've installed @daimon-protocol/sdk into your own project,
 * replace the import URL with the bare module specifier.
 *
 * Run from the repo root:
 *
 *   node examples/x402-smoke/typescript_smoke.mjs
 */

import { fileURLToPath } from "node:url";
import * as path from "node:path";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SDK_DIST = path.resolve(HERE, "../../sdk/typescript/dist/index.js");

const { Client, DaemonNotRunning, RPCError } = await import(SDK_DIST);

const URL_TARGET = process.env.X402_URL ?? "http://127.0.0.1:8402/r";
const CEILING = process.env.X402_CEILING_SMALLEST ?? "100000";

async function main() {
  let client;
  try {
    client = new Client();
  } catch (e) {
    console.error(`ts: failed to construct client — ${e.message}`);
    return 1;
  }

  let me;
  try {
    me = await client.identity.get();
  } catch (e) {
    if (e instanceof DaemonNotRunning) {
      console.error(`ts: daemon not running — ${e.message}`);
      return 1;
    }
    throw e;
  }
  console.log(`ts: DID = ${me.did}`);

  const wallets = await client.wallet.list();
  if (wallets.length === 0) {
    console.error("ts: no wallets in keystore — run `daimon wallet create --chain evm:base` first");
    return 1;
  }
  console.log(`ts: wallets in keystore = ${JSON.stringify(wallets.map((w) => w.chain))}`);
  const evm = wallets.find((w) => w.chain.startsWith("evm:"));
  if (!evm) {
    console.error("ts: no EVM wallet in keystore");
    return 1;
  }
  console.log(`ts: paying from ${evm.address} on ${evm.chain}`);

  // Cross-language wire-shape check: derive at index 0 should match the
  // existing wallet's address (both go through the same daemon-side BIP-44
  // derivation pipeline). Sibling assertion to python_smoke.py — together
  // they catch any drift between the two SDKs' derive wrappers AND between
  // either wrapper and the daemon's daimon.wallet.derive handler.
  const derived = await client.wallet.derive({ chain: evm.chain, index: 0 });
  if (derived.address !== evm.address) {
    console.error(
      `ts: derive index 0 returned ${JSON.stringify(derived.address)} ` +
        `but wallet.list reports ${JSON.stringify(evm.address)}`,
    );
    return 2;
  }
  if (derived.path !== "m/44'/60'/0'/0/0") {
    console.error(`ts: derive index 0 returned path ${JSON.stringify(derived.path)}, want m/44'/60'/0'/0/0`);
    return 2;
  }
  console.log(`ts: derive index 0 matches wallet.list — wire-shape parity ✓`);

  const t0 = process.hrtime.bigint();
  let result;
  try {
    result = await client.payment.pay({
      url: URL_TARGET,
      ceilingSmallestUnit: BigInt(CEILING),
    });
  } catch (e) {
    if (e instanceof RPCError) {
      console.error(`ts: payment failed (rpc code ${e.code}): ${e.rpcMessage}`);
      return 2;
    }
    throw e;
  }
  const elapsedMs = Number(process.hrtime.bigint() - t0) / 1e6;

  console.log(`ts: HTTP ${result.statusCode} in ${elapsedMs.toFixed(1)}ms`);
  const bodyText = Buffer.from(result.body).toString("utf-8").trimEnd();
  console.log(`ts: body = ${JSON.stringify(bodyText)}`);
  if (result.paymentResponse) {
    const pr = result.paymentResponse;
    console.log(
      `ts: PAYMENT-RESPONSE success=${pr.success} tx=${pr.transaction} payer=${pr.payer}`,
    );
  }

  const summary = await client.activity.verify();
  console.log(`ts: activity.verify -> ${JSON.stringify(summary)}`);
  return 0;
}

main()
  .then((code) => process.exit(code ?? 0))
  .catch((e) => {
    console.error("ts: error", e);
    process.exit(1);
  });
