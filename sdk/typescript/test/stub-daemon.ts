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

export type Handler = unknown | ((params: unknown) => unknown);

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
  private server: net.Server | null = null;

  constructor(readonly socketPath: string) {}

  // Overloads so TypeScript infers `params` as `unknown` (not `any`) for
  // callback handlers without the call site having to annotate.
  handle(method: string, response: (params: unknown) => unknown): void;
  handle(method: string, response: unknown): void;
  handle(method: string, response: Handler): void {
    this.handlers.set(method, response);
  }

  start(): Promise<void> {
    if (fs.existsSync(this.socketPath)) {
      fs.unlinkSync(this.socketPath);
    }
    this.server = net.createServer((conn) => this.serveOne(conn));
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

  private serveOne(conn: net.Socket): void {
    const chunks: Buffer[] = [];
    conn.on("data", (chunk: Buffer) => chunks.push(chunk));
    conn.on("end", () => {
      const data = Buffer.concat(chunks).toString("utf8");
      if (!data) {
        conn.end();
        return;
      }
      let req: {
        method?: string;
        params?: unknown;
        id?: number | string | null;
      };
      try {
        req = JSON.parse(data);
      } catch {
        conn.end(JSON.stringify(errResponse(null, -32700, "parse error")) + "\n");
        return;
      }
      const method = req.method ?? "";
      const params = req.params;
      const reqId = req.id ?? 1;
      this.calls.push({ method, params });

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
    });
    conn.on("error", () => {
      // ignore — peer hang-ups happen in tests
    });
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

