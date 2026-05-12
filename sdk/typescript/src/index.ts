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

// Keep in sync with package.json#version on each release. Build-time
// inlining would be cleaner; until that lands, bump both fields when
// cutting a version.
export const VERSION = "0.1.0";
