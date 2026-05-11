/**
 * Daimon TypeScript SDK — thin client over the local daimon daemon.
 *
 * Public surface:
 *
 *   import { Client, DaemonNotRunning, DaemonLocked, RPCError } from "@daimon/sdk";
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
  ActivityQueryParams,
  ActivityAppendParams,
} from "./client.js";
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

export const VERSION = "0.1.0-dev.0";
