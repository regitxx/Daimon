/**
 * Daimon TypeScript SDK — thin client over the local daimon daemon.
 *
 * Public surface:
 *
 *   import { Client, DaemonNotRunning, DaemonLocked, RPCError } from "@daimon-protocol/sdk";
 *
 * The package mirrors the Go cmd/daimon CLI's wire-level behaviour: one
 * Unix-socket connection per RPC, JSON-RPC 2.0 envelope, no pipelining.
 * See SPEC §6.1 for the canonical method list.
 */

export { Client } from "./client.js";
export type {
  ClientOptions,
  JsonObject,
  JsonArray,
  MemoryWriteParams,
  MemorySearchParams,
  ProviderInvokeParams,
  ProviderStreamParams,
  ActivityQueryParams,
  ActivityAppendParams,
  // v0.2 surface
  Wallet,
  WalletCreateParams,
  WalletAddressParams,
  WalletSignParams,
  WalletShowMnemonicParams,
  PaymentPayParams,
  PaymentPayResult,
  PaymentResponse,
} from "./client.js";
export { StreamHandle, DEFAULT_STREAM_TIMEOUT_MS } from "./stream.js";
export {
  DaimonError,
  DaemonNotRunning,
  DaemonLocked,
  RPCError,
  CODE_INVALID_REQUEST,
  CODE_METHOD_NOT_FOUND,
  CODE_INVALID_PARAMS,
  CODE_INTERNAL_ERROR,
  CODE_IDENTITY_LOCKED,
  CODE_NOT_FOUND,
} from "./errors.js";

// VERSION is auto-generated from package.json#version by
// scripts/gen-version.mjs (chained ahead of `npm run build` and
// `prepublishOnly`). Edit package.json to bump versions; the constant
// follows. See sdk/typescript/scripts/gen-version.mjs for details.
export { VERSION } from "./version.js";
