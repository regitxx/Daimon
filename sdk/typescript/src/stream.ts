/**
 * Streaming support for `daimon.provider.stream`.
 *
 * Wire shape (mirrors cmd/daimon/rpc.go::rpcStream and the server's
 * handleProviderStream):
 *
 *   1. Client sends one JSON-RPC request envelope.
 *   2. Server sends 0..N `daimon.provider.stream.delta` notifications
 *      (no `id` field per JSON-RPC 2.0 §4.1, params `{content: "..."}`).
 *   3. Server sends ONE terminal response frame carrying the request `id`
 *      and either `result` (the full `{response, injected_memory_ids?}`
 *      envelope) or `error`.
 *
 * Each frame is JSON-encoded and newline-terminated — Go's
 * json.Encoder.Encode always appends `\n`. Splitting the read buffer on
 * raw `\n` bytes is safe because JSON-encoded strings escape literal
 * newlines as `\\n`, so the only `\n` byte on the wire is the frame
 * separator.
 *
 * The connection lifecycle is different from the one-request-per-call
 * path in `./rpc.ts`: we do NOT half-close the write side after sending
 * the request because the server keeps writing until the stream
 * completes. We close fully after the terminal frame (or on early exit
 * via close()).
 */

import * as net from "node:net";

import { DaemonNotRunning, RPCError, fromErrorObject } from "./errors.js";

export const DEFAULT_STREAM_TIMEOUT_MS = 300_000;

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

type StreamEvent =
  | { kind: "delta"; content: string }
  | { kind: "terminal"; envelope: Record<string, unknown> | null }
  | { kind: "error"; err: Error };

/**
 * Async-iterable of delta strings, with the terminal envelope on `.final`.
 *
 * Usage:
 *
 *   const stream = client.provider.stream({
 *     provider: "ollama",
 *     model: "llama3.2:latest",
 *     messages: [{ role: "user", content: "say hi" }],
 *   });
 *   for await (const delta of stream) {
 *     process.stdout.write(delta);
 *   }
 *   // After iteration: stream.final is the wrapping envelope
 *   // {response: {...}, injected_memory_ids?: [...]}
 *   const usage = (stream.final?.response as { usage: unknown }).usage;
 *
 * Calling close() tears down the socket; safe to call from a finally
 * block to guarantee resource release on early exit.
 */
export class StreamHandle implements AsyncIterable<string>, AsyncIterator<string> {
  final: Record<string, unknown> | null = null;

  private readonly events: StreamEvent[] = [];
  private waiter: Deferred<void> | null = null;
  private done = false;
  private buf = "";
  private readonly sock: net.Socket;

  constructor(sock: net.Socket) {
    this.sock = sock;

    sock.on("data", (chunk: Buffer) => this.onData(chunk));
    sock.on("end", () => this.onEnd());
    sock.on("error", (err: Error) => this.onSocketError(err));
    sock.on("close", () => {
      // If the socket closes without 'end' having been received (e.g.
      // peer killed the connection), surface that as EOF so iteration
      // can terminate. onEnd is idempotent via the done flag.
      this.onEnd();
    });
  }

  [Symbol.asyncIterator](): AsyncIterator<string> {
    return this;
  }

  async next(): Promise<IteratorResult<string>> {
    while (true) {
      const event = this.events.shift();
      if (event !== undefined) {
        if (event.kind === "delta") {
          return { value: event.content, done: false };
        }
        if (event.kind === "terminal") {
          this.final = event.envelope;
          this.cleanup();
          return { value: undefined, done: true };
        }
        // error
        this.cleanup();
        throw event.err;
      }
      if (this.done) {
        // Peer closed without sending a terminal response.
        this.cleanup();
        throw new RPCError(0, "stream ended without terminal response");
      }
      // Wait for the next data event to deliver more frames.
      this.waiter = deferred<void>();
      await this.waiter.promise;
      this.waiter = null;
    }
  }

  /** Implement AsyncIterator.return() so for-await break/return tears down the socket. */
  async return(): Promise<IteratorResult<string>> {
    this.close();
    return { value: undefined, done: true };
  }

  /** Tear down the underlying socket. Idempotent. */
  close(): void {
    if (!this.sock.destroyed) {
      this.sock.destroy();
    }
    this.cleanup();
  }

  // --- internal -----------------------------------------------------------

  private cleanup(): void {
    this.done = true;
    if (this.waiter) {
      const w = this.waiter;
      this.waiter = null;
      w.resolve();
    }
  }

  private wake(): void {
    if (this.waiter) {
      const w = this.waiter;
      this.waiter = null;
      w.resolve();
    }
  }

  private onData(chunk: Buffer): void {
    this.buf += chunk.toString("utf8");
    let nl = this.buf.indexOf("\n");
    while (nl >= 0) {
      const line = this.buf.slice(0, nl);
      this.buf = this.buf.slice(nl + 1);
      if (line.trim().length > 0) {
        this.handleFrame(line);
      }
      nl = this.buf.indexOf("\n");
    }
    this.wake();
  }

  private onEnd(): void {
    if (this.done) return;
    // Drain any trailing unterminated frame, mirroring the Python reader.
    const tail = this.buf.trim();
    if (tail.length > 0) {
      this.handleFrame(tail);
      this.buf = "";
    }
    this.done = true;
    this.wake();
  }

  private onSocketError(err: Error): void {
    if (this.done) return;
    this.events.push({ kind: "error", err });
    this.done = true;
    this.wake();
  }

  private handleFrame(line: string): void {
    let frame: Record<string, unknown>;
    try {
      const parsed: unknown = JSON.parse(line);
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        return;
      }
      frame = parsed as Record<string, unknown>;
    } catch (e) {
      this.events.push({
        kind: "error",
        err: new RPCError(0, `malformed stream frame: ${(e as Error).message}`),
      });
      return;
    }

    // Notification frame: no `id`, has a `method`. JSON-RPC 2.0 §4.1.
    if (!("id" in frame) && typeof frame["method"] === "string") {
      const method = frame["method"] as string;
      if (method === "daimon.provider.stream.delta") {
        const params = frame["params"];
        if (params !== null && typeof params === "object") {
          const content = (params as { content?: unknown }).content;
          if (typeof content === "string" && content.length > 0) {
            this.events.push({ kind: "delta", content });
          }
        }
      }
      // Unknown notification kinds (future tool-call deltas, role markers,
      // etc.) — ignore, forward-compat with the daemon.
      return;
    }

    // Terminal frame.
    if (frame["error"] !== undefined && frame["error"] !== null) {
      const err = fromErrorObject(frame["error"] as { code?: number; message?: string; data?: unknown });
      this.events.push({ kind: "error", err });
      return;
    }
    const result = frame["result"];
    this.events.push({
      kind: "terminal",
      envelope: (result as Record<string, unknown> | null) ?? null,
    });
  }
}

export interface OpenStreamOptions {
  timeoutMs?: number;
}

/**
 * Open a streaming RPC and return a `StreamHandle`.
 *
 * The handle owns the socket — the caller iterates (or calls close())
 * and the socket is destroyed on terminal frame, on error, or on
 * close().
 */
export function openStream(
  socketPath: string,
  method: string,
  params: unknown,
  options: OpenStreamOptions = {},
): Promise<StreamHandle> {
  const timeoutMs = options.timeoutMs ?? DEFAULT_STREAM_TIMEOUT_MS;

  return new Promise((resolve, reject) => {
    const sock = net.createConnection({ path: socketPath });
    sock.setNoDelay(true);
    let settled = false;
    const settle = (fn: () => void) => {
      if (settled) return;
      settled = true;
      fn();
    };

    sock.setTimeout(timeoutMs);
    sock.once("timeout", () => {
      settle(() => {
        sock.destroy();
        reject(new Error(`stream open timed out after ${timeoutMs}ms`));
      });
    });

    sock.once("error", (err: NodeJS.ErrnoException) => {
      const code = err.code;
      settle(() => {
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

    sock.once("connect", () => {
      const request: Record<string, unknown> = { jsonrpc: "2.0", method, id: 1 };
      if (params !== undefined) request["params"] = params;
      const payload = JSON.stringify(request) + "\n";
      // Critical: do NOT half-close the write side. The server reads our
      // single request, then writes notifications and the terminal frame
      // on the same connection. Use write(), not end().
      sock.write(payload, (err) => {
        if (err) {
          settle(() => {
            sock.destroy();
            reject(err);
          });
          return;
        }
        settle(() => {
          // Clear the connect-phase timeout; iteration has its own
          // lifecycle and may legitimately be idle between deltas.
          sock.setTimeout(0);
          resolve(new StreamHandle(sock));
        });
      });
    });
  });
}
