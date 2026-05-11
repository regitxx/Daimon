/**
 * Pytest's conftest.StubDaemon equivalent: a tiny Unix-socket JSON-RPC
 * server for SDK tests. Each connection is one request, one response,
 * then close — exactly matching cmd/daimon/rpc.go's per-call lifecycle.
 *
 * Handlers can be either:
 *   - a plain value: sent back as the `result` field
 *   - a callback `(params) => value`: return value goes into `result`;
 *     throw StubRPCError to send a JSON-RPC error envelope.
 */

import * as fs from "node:fs";
import * as net from "node:net";
import * as path from "node:path";

// Valid memory kinds per SPEC §5.2. Keep in sync with
// internal/memory/memory.go:41 — the daemon rejects writes with any other
// kind via -32004 invalid memory kind, and the stub mirrors that so test
// fixtures can't silently drift from production validation.
const VALID_MEMORY_KINDS = new Set([
  "fact",
  "preference",
  "task",
  "observation",
]);

export type Handler = unknown | ((params: unknown) => unknown);

/**
 * Streaming handler shape.
 *
 *   `deltas` — either a list of strings (sent verbatim as
 *   `daimon.provider.stream.delta` notifications, in order) or a callable
 *   `(params) => string[]` evaluated lazily against the inbound request.
 *
 *   `terminal` — the final response: a plain object (sent as `result`), a
 *   `StubRPCError` instance (sent as `error`), or a callable returning
 *   either.
 */
export type StreamHandler = {
  deltas: string[] | ((params: unknown) => string[]);
  terminal:
    | Record<string, unknown>
    | StubRPCError
    | ((params: unknown) => Record<string, unknown> | StubRPCError);
};

export class StubRPCError extends Error {
  constructor(
    readonly code: number,
    message: string,
    readonly data: unknown = undefined,
  ) {
    super(`${code}: ${message}`);
    this.rpcMessage = message;
  }
  readonly rpcMessage: string;
}

interface RecordedCall {
  method: string;
  params: unknown;
}

export class StubDaemon {
  readonly calls: RecordedCall[] = [];
  private readonly handlers = new Map<string, Handler>();
  private readonly streamHandlers = new Map<string, StreamHandler>();
  /** Optional override for custom stream serving (used by frame-injection tests). */
  serveStream:
    | ((conn: net.Socket, handler: StreamHandler, params: unknown, reqId: number | string) => void)
    | null = null;
  private server: net.Server | null = null;

  constructor(readonly socketPath: string) {}

  // Overloads so TypeScript infers `params` as `unknown` (not `any`) for
  // callback handlers without the call site having to annotate.
  handle(method: string, response: (params: unknown) => unknown): void;
  handle(method: string, response: unknown): void;
  handle(method: string, response: Handler): void {
    this.handlers.set(method, response);
  }

  /**
   * Register a streaming handler. The stub will send each entry of
   * `deltas` as a `daimon.provider.stream.delta` notification, then the
   * `terminal` frame carrying either result or error.
   *
   * Does not require the client to half-close the write side — the stub
   * dispatches on the first newline-terminated frame on the wire.
   */
  stream(method: string, handler: StreamHandler): void {
    this.streamHandlers.set(method, handler);
  }

  start(): Promise<void> {
    if (fs.existsSync(this.socketPath)) {
      fs.unlinkSync(this.socketPath);
    }
    this.server = net.createServer((conn) => this.serveConnection(conn));
    return new Promise((resolve, reject) => {
      this.server!.once("error", reject);
      this.server!.listen(this.socketPath, () => {
        this.server!.off("error", reject);
        resolve();
      });
    });
  }

  async stop(): Promise<void> {
    if (this.server) {
      await new Promise<void>((resolve) => {
        this.server!.close(() => resolve());
      });
      this.server = null;
    }
    try {
      if (fs.existsSync(this.socketPath)) fs.unlinkSync(this.socketPath);
    } catch {
      // ignore
    }
  }

  /**
   * Read one newline-terminated request line, dispatch to either the
   * streaming handler (multi-frame response) or the single-response
   * handler. Mirrors the Python conftest's `_serve_one` post-refactor.
   */
  private serveConnection(conn: net.Socket): void {
    let buf = "";
    let dispatched = false;

    const dispatch = (line: string): void => {
      dispatched = true;
      if (!line.trim()) {
        conn.end();
        return;
      }
      let req: { method?: string; params?: unknown; id?: number | string | null };
      try {
        req = JSON.parse(line);
      } catch {
        conn.end(JSON.stringify(errResponse(null, -32700, "parse error")) + "\n");
        return;
      }
      const method = req.method ?? "";
      const params = req.params;
      const reqId = req.id ?? 1;
      this.calls.push({ method, params });

      if (
        method === "daimon.memory.write" &&
        params !== null &&
        typeof params === "object"
      ) {
        const kind = (params as { kind?: unknown }).kind;
        if (typeof kind !== "string" || !VALID_MEMORY_KINDS.has(kind)) {
          conn.end(
            JSON.stringify(errResponse(reqId, -32004, "invalid memory kind")) + "\n",
          );
          return;
        }
      }

      const streamHandler = this.streamHandlers.get(method);
      if (streamHandler !== undefined) {
        const customServe = this.serveStream;
        if (customServe !== null) {
          customServe(conn, streamHandler, params, reqId);
          return;
        }
        this.runStream(conn, streamHandler, params, reqId);
        return;
      }

      const handler = this.handlers.get(method);
      if (handler === undefined) {
        conn.end(
          JSON.stringify(errResponse(reqId, -32601, `method not found: ${method}`)) + "\n",
        );
        return;
      }
      try {
        const result = typeof handler === "function"
          ? (handler as (p: unknown) => unknown)(params)
          : handler;
        const resp = { jsonrpc: "2.0" as const, result, id: reqId };
        conn.end(JSON.stringify(resp) + "\n");
      } catch (e) {
        if (e instanceof StubRPCError) {
          conn.end(
            JSON.stringify(errResponse(reqId, e.code, e.rpcMessage, e.data)) + "\n",
          );
        } else {
          conn.end(
            JSON.stringify(errResponse(reqId, -32603, (e as Error).message)) + "\n",
          );
        }
      }
    };

    conn.on("data", (chunk: Buffer) => {
      if (dispatched) return;
      buf += chunk.toString("utf8");
      const nl = buf.indexOf("\n");
      if (nl >= 0) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        dispatch(line);
      }
    });
    conn.on("end", () => {
      if (!dispatched && buf.length > 0) {
        dispatch(buf);
      } else if (!dispatched) {
        conn.end();
      }
    });
    conn.on("error", () => {
      // ignore — peer hang-ups happen in tests
    });
  }

  private runStream(
    conn: net.Socket,
    handler: StreamHandler,
    params: unknown,
    reqId: number | string,
  ): void {
    let deltas: string[];
    try {
      deltas = typeof handler.deltas === "function" ? handler.deltas(params) : handler.deltas.slice();
    } catch (e) {
      conn.end(JSON.stringify(errResponse(reqId, -32603, (e as Error).message)) + "\n");
      return;
    }
    let terminal: Record<string, unknown> | StubRPCError;
    try {
      terminal = typeof handler.terminal === "function" ? handler.terminal(params) : handler.terminal;
    } catch (e) {
      conn.end(JSON.stringify(errResponse(reqId, -32603, (e as Error).message)) + "\n");
      return;
    }

    let broken = false;
    conn.on("error", () => {
      broken = true;
    });

    for (const d of deltas) {
      if (broken || conn.destroyed) return;
      const notif = {
        jsonrpc: "2.0",
        method: "daimon.provider.stream.delta",
        params: { content: d },
      };
      conn.write(JSON.stringify(notif) + "\n");
    }

    if (broken || conn.destroyed) return;
    if (terminal instanceof StubRPCError) {
      conn.end(JSON.stringify(errResponse(reqId, terminal.code, terminal.rpcMessage, terminal.data)) + "\n");
      return;
    }
    const resp = { jsonrpc: "2.0" as const, result: terminal, id: reqId };
    conn.end(JSON.stringify(resp) + "\n");
  }
}

function errResponse(
  id: number | string | null,
  code: number,
  message: string,
  data?: unknown,
): unknown {
  const err: Record<string, unknown> = { code, message };
  if (data !== undefined && data !== null) err["data"] = data;
  return { jsonrpc: "2.0", error: err, id };
}

/**
 * Allocate a short temp dir under /tmp suitable for AF_UNIX sockets.
 * macOS's default os.tmpdir() resolves into /var/folders/.../T/ which
 * frequently exceeds the 104-byte sun_path cap before we even append a
 * filename. Anything that needs to bind a socket must root paths under
 * /tmp/dt-<rand>/ to stay under the cap.
 */
export function makeShortTmp(): string {
  return fs.mkdtempSync(path.join("/tmp", "dt-"));
}

export function cleanupShortTmp(dir: string): void {
  try {
    fs.rmSync(dir, { recursive: true, force: true });
  } catch {
    // ignore
  }
}

/** Returns a started StubDaemon listening on a short temp socket. */
export async function startStubDaemon(): Promise<{
  daemon: StubDaemon;
  tmp: string;
}> {
  const tmp = makeShortTmp();
  const daemon = new StubDaemon(path.join(tmp, "daimon.sock"));
  await daemon.start();
  return { daemon, tmp };
}

