/**
 * Unix-socket JSON-RPC 2.0 client.
 *
 * Mirrors cmd/daimon/rpc.go and sdk/python/daimon/_rpc.py:
 *   - one connection per RPC (no pipelining in v0.1)
 *   - request envelope: {jsonrpc:"2.0", method, params?, id:1}
 *   - response envelope: {jsonrpc:"2.0", result|error, id}
 *   - error rewrites: ENOENT/ECONNREFUSED -> DaemonNotRunning,
 *     code -32001 -> DaemonLocked, anything else -> RPCError.
 *
 * Streaming (daimon.provider.stream notifications) is intentionally NOT
 * implemented in this SDK session.
 */

import * as net from "node:net";

import {
  DaemonNotRunning,
  RPCError,
  fromErrorObject,
  type RpcErrorObject,
} from "./errors.js";

export const DEFAULT_TIMEOUT_MS = 30_000;

export interface RpcOptions {
  /** Per-call timeout in milliseconds. */
  timeoutMs?: number;
}

interface JsonRpcRequest {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
  id: number;
}

interface JsonRpcResponse {
  jsonrpc?: "2.0";
  result?: unknown;
  error?: RpcErrorObject | null;
  id?: number | string | null;
}

/**
 * Send a single JSON-RPC request and return the decoded result.
 *
 * On socket connect failure (ENOENT, ECONNREFUSED) raises DaemonNotRunning.
 * On JSON-RPC error, raises DaemonLocked (code -32001) or RPCError.
 */
export function rpcCall(
  socketPath: string,
  method: string,
  params: unknown,
  options: RpcOptions = {},
): Promise<unknown> {
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;

  const request: JsonRpcRequest = {
    jsonrpc: "2.0",
    method,
    id: 1,
  };
  // Mirror Go's json.Marshal + Python's omit-when-None: only include
  // `params` when the caller passed something non-undefined. Explicit null
  // and empty {} are sent verbatim (different meaning to the server).
  if (params !== undefined) {
    request.params = params;
  }
  // Match Go: one object per connection, trailing newline so the server's
  // json.Decoder yields promptly even without seeing EOF.
  const payload = JSON.stringify(request) + "\n";

  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let settled = false;
    const settle = (fn: () => void) => {
      if (settled) return;
      settled = true;
      fn();
    };

    const sock = net.createConnection({ path: socketPath });
    sock.setNoDelay(true);

    const timer = setTimeout(() => {
      settle(() => {
        sock.destroy();
        reject(new Error(`rpc call timed out after ${timeoutMs}ms`));
      });
    }, timeoutMs);

    sock.once("connect", () => {
      // Half-close the write side: send the request + FIN so the server's
      // json.Decoder sees EOF promptly. Node keeps the read side open
      // until the peer also sends FIN — same lifecycle as Python's
      // socket.shutdown(SHUT_WR).
      sock.end(payload);
    });

    sock.on("data", (chunk: Buffer) => {
      chunks.push(chunk);
    });

    sock.on("error", (err: NodeJS.ErrnoException) => {
      const code = err.code;
      settle(() => {
        clearTimeout(timer);
        sock.destroy();
        if (code === "ENOENT" || code === "ECONNREFUSED") {
          reject(
            new DaemonNotRunning(
              `daemon ${
                code === "ENOENT" ? "socket not present" : "not accepting connections"
              } at ${socketPath} — run \`daimon unlock\` first`,
            ),
          );
          return;
        }
        reject(err);
      });
    });

    sock.on("end", () => {
      settle(() => {
        clearTimeout(timer);
        sock.destroy();
        try {
          const body = Buffer.concat(chunks).toString("utf8");
          if (!body) {
            reject(new RPCError(0, "empty response from daemon"));
            return;
          }
          let resp: unknown;
          try {
            resp = JSON.parse(body);
          } catch (e) {
            reject(new RPCError(0, `malformed response: ${(e as Error).message}`));
            return;
          }
          if (typeof resp !== "object" || resp === null || Array.isArray(resp)) {
            reject(new RPCError(0, `unexpected response shape: ${typeof resp}`));
            return;
          }
          const r = resp as JsonRpcResponse;
          if (r.error != null) {
            reject(fromErrorObject(r.error));
            return;
          }
          // SPEC §6.1: result MAY be omitted when the RPC has no return payload.
          resolve(r.result ?? null);
        } catch (e) {
          reject(e);
        }
      });
    });
  });
}
