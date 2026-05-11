/**
 * Daimon — TypeScript SDK streaming example (ESM-JS form).
 *
 * Streams a short prompt through the local Ollama backend over a running
 * daimon, prints inter-delta timing, the terminal envelope's usage fields,
 * and an activity.verify summary. Sibling of python_stream.py — both run
 * against the same daemon, producing two streamed=true rows in one signed
 * audit chain that either SDK + the CLI can independently verify.
 *
 * The example imports directly from the built dist so it's runnable
 * without any package manager step beyond `npm run build` inside the
 * SDK. If you've published or linked @daimon/sdk in your own project,
 * replace the import with `import { Client } from "@daimon/sdk"`.
 *
 * Prerequisites:
 *
 *   1. A daimon is running and unlocked (`daimon init && daimon unlock`).
 *   2. Ollama is up with `llama3.2:latest` pulled.
 *   3. The TypeScript SDK is built:
 *        cd sdk/typescript
 *        npm install
 *        npm run build
 *
 * Run from the repo root:
 *
 *   node examples/streaming/typescript_stream.mjs
 */

import { fileURLToPath } from "node:url";
import * as path from "node:path";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SDK_DIST = path.resolve(HERE, "../../sdk/typescript/dist/index.js");

const { Client, DaemonNotRunning } = await import(SDK_DIST);

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

  const stream = await client.provider.stream({
    provider: "ollama",
    model: "llama3.2:latest",
    messages: [
      { role: "user", content: "Reply with exactly: hello from typescript" },
    ],
  });

  const t0 = process.hrtime.bigint();
  const timings = [];
  const deltas = [];
  for await (const d of stream) {
    const ms = Number(process.hrtime.bigint() - t0) / 1e6;
    timings.push(ms);
    deltas.push(d);
    process.stdout.write(d);
  }
  process.stdout.write("\n");

  if (timings.length === 0) {
    console.log("ts: stream emitted 0 deltas (terminal-only fallback)");
  } else {
    const gaps = [];
    for (let i = 1; i < timings.length; i++) {
      gaps.push(timings[i] - timings[i - 1]);
    }
    const meanGap = gaps.length ? gaps.reduce((a, b) => a + b, 0) / gaps.length : 0;
    console.log(
      `ts: ${deltas.length} deltas — first ${timings[0].toFixed(1)}ms, ` +
        `last ${timings[timings.length - 1].toFixed(1)}ms, ` +
        `mean inter-gap ${meanGap.toFixed(2)}ms`,
    );
  }

  const env = stream.final;
  if (env === null) {
    console.log("ts: WARN — no terminal envelope");
  } else {
    const resp = env.response;
    console.log(
      `ts: model=${resp.model} stop=${resp.stop_reason} usage=${JSON.stringify(resp.usage)}`,
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
