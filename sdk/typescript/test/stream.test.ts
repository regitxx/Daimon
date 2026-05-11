/**
 * Streaming surface: Client.provider.stream over notification frames + terminal envelope.
 *
 * Mirrors sdk/python/tests/test_stream.py byte-for-byte on the wire shape.
 */

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Client } from "../src/client.js";
import { DaemonLocked, RPCError } from "../src/errors.js";
import {
  StubDaemon,
  StubRPCError,
  cleanupShortTmp,
  startStubDaemon,
} from "./stub-daemon.js";

describe("Client.provider.stream", () => {
  let daemon: StubDaemon;
  let tmp: string;
  let client: Client;

  beforeEach(async () => {
    const started = await startStubDaemon();
    daemon = started.daemon;
    tmp = started.tmp;
    client = new Client({ socketPath: daemon.socketPath });
  });

  afterEach(async () => {
    await daemon.stop();
    cleanupShortTmp(tmp);
  });

  async function drain(stream: AsyncIterable<string>): Promise<string[]> {
    const out: string[] = [];
    for await (const d of stream) out.push(d);
    return out;
  }

  it("yields all deltas in order", async () => {
    daemon.stream("daimon.provider.stream", {
      deltas: ["Hel", "lo, ", "wor", "ld"],
      terminal: {
        response: {
          model: "llama3.2",
          content: "Hello, world",
          stop_reason: "end_turn",
          usage: { input_tokens: 5, output_tokens: 4 },
        },
      },
    });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
    });
    const chunks = await drain(stream);
    expect(chunks).toEqual(["Hel", "lo, ", "wor", "ld"]);
  });

  it("final envelope populates after iteration", async () => {
    const terminal = {
      response: {
        model: "llama3.2",
        content: "Pong",
        stop_reason: "end_turn",
        usage: { input_tokens: 32, output_tokens: 3 },
      },
      injected_memory_ids: ["01ABC", "01DEF"],
    };
    daemon.stream("daimon.provider.stream", { deltas: ["Pong"], terminal });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "say pong" }],
      inject_context: { query: "ping", max_tokens: 64 },
    });
    expect(stream.final).toBeNull();
    for await (const _ of stream) {
      // drain
    }
    expect(stream.final).toEqual(terminal);
    expect((stream.final as { injected_memory_ids: string[] }).injected_memory_ids).toEqual([
      "01ABC",
      "01DEF",
    ]);
  });

  it("assembles nested request from flat kwargs", async () => {
    const received: Record<string, unknown> = {};
    daemon.stream("daimon.provider.stream", {
      deltas: (p) => {
        Object.assign(received, p as Record<string, unknown>);
        return ["x"];
      },
      terminal: {
        response: {
          model: "m",
          content: "x",
          stop_reason: "end",
          usage: { input_tokens: 0, output_tokens: 1 },
        },
      },
    });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
      system: "be terse",
      temperature: 0.5,
      max_tokens: 64,
    });
    await drain(stream);
    expect(received["provider"]).toBe("ollama");
    const req = received["request"] as Record<string, unknown>;
    expect(req["model"]).toBe("llama3.2");
    expect(req["messages"]).toEqual([{ role: "user", content: "hi" }]);
    expect(req["system"]).toBe("be terse");
    expect(req["temperature"]).toBe(0.5);
    expect(req["max_tokens"]).toBe(64);
    expect("inject_context" in received).toBe(false);
  });

  it("passes inject_context verbatim", async () => {
    const received: Record<string, unknown> = {};
    daemon.stream("daimon.provider.stream", {
      deltas: (p) => {
        Object.assign(received, p as Record<string, unknown>);
        return [];
      },
      terminal: {
        response: {
          model: "m",
          content: "",
          stop_reason: "end",
          usage: { input_tokens: 0, output_tokens: 0 },
        },
      },
    });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
      inject_context: { query: "todo", max_tokens: 256, kinds: ["fact"] },
    });
    await drain(stream);
    expect(received["inject_context"]).toEqual({
      query: "todo",
      max_tokens: 256,
      kinds: ["fact"],
    });
  });

  it("zero deltas — terminal-only fallback case", async () => {
    daemon.stream("daimon.provider.stream", {
      deltas: [],
      terminal: {
        response: {
          model: "m",
          content: "all at once",
          stop_reason: "end",
          usage: { input_tokens: 1, output_tokens: 3 },
        },
      },
    });
    const stream = await client.provider.stream({
      provider: "anthropic",
      model: "claude-sonnet-4-6",
      messages: [{ role: "user", content: "hi" }],
    });
    const chunks = await drain(stream);
    expect(chunks).toEqual([]);
    expect(
      (stream.final as { response: { content: string } }).response.content,
    ).toBe("all at once");
  });

  it("ignores unknown notification kinds (forward-compat)", async () => {
    // Hand-craft frames: real delta, unknown notification kind, real delta, terminal.
    daemon.stream("daimon.provider.stream", { deltas: [], terminal: {} });
    daemon.serveStream = (conn, _h, _params, reqId) => {
      conn.write(
        JSON.stringify({
          jsonrpc: "2.0",
          method: "daimon.provider.stream.delta",
          params: { content: "A" },
        }) + "\n",
      );
      conn.write(
        JSON.stringify({
          jsonrpc: "2.0",
          method: "daimon.provider.stream.tool",
          params: { name: "future" },
        }) + "\n",
      );
      conn.write(
        JSON.stringify({
          jsonrpc: "2.0",
          method: "daimon.provider.stream.delta",
          params: { content: "B" },
        }) + "\n",
      );
      conn.end(
        JSON.stringify({
          jsonrpc: "2.0",
          result: {
            response: {
              model: "m",
              content: "AB",
              stop_reason: "end",
              usage: { input_tokens: 0, output_tokens: 0 },
            },
          },
          id: reqId,
        }) + "\n",
      );
    };
    try {
      const stream = await client.provider.stream({
        provider: "ollama",
        model: "llama3.2",
        messages: [{ role: "user", content: "hi" }],
      });
      const chunks = await drain(stream);
      expect(chunks).toEqual(["A", "B"]);
      expect(
        (stream.final as { response: { content: string } }).response.content,
      ).toBe("AB");
    } finally {
      daemon.serveStream = null;
    }
  });

  it("terminal error raises RPCError after deltas drain", async () => {
    daemon.stream("daimon.provider.stream", {
      deltas: ["partial "],
      terminal: new StubRPCError(-32603, "provider.ollama.stream: connection reset"),
    });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
    });
    const first = await stream.next();
    expect(first.done).toBe(false);
    expect(first.value).toBe("partial ");
    try {
      await stream.next();
      throw new Error("expected RPCError");
    } catch (e) {
      expect(e).toBeInstanceOf(RPCError);
      expect((e as RPCError).code).toBe(-32603);
      expect((e as RPCError).rpcMessage).toContain("connection reset");
    }
  });

  it("terminal -32001 raises DaemonLocked", async () => {
    daemon.stream("daimon.provider.stream", {
      deltas: [],
      terminal: new StubRPCError(-32001, "identity is locked"),
    });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
    });
    try {
      await stream.next();
      throw new Error("expected DaemonLocked");
    } catch (e) {
      expect(e).toBeInstanceOf(DaemonLocked);
    }
  });

  it("close() tears down socket on early exit, second call still works", async () => {
    daemon.stream("daimon.provider.stream", {
      deltas: ["a", "b", "c", "d"],
      terminal: {
        response: {
          model: "m",
          content: "abcd",
          stop_reason: "end",
          usage: { input_tokens: 0, output_tokens: 4 },
        },
      },
    });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
    });
    const first = await stream.next();
    expect(first.value).toBe("a");
    stream.close();

    // A second call should still work — the stub spawns one connection
    // per accept; closing one doesn't affect the listener.
    const stream2 = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
    });
    const chunks = await drain(stream2);
    expect(chunks).toEqual(["a", "b", "c", "d"]);
  });

  it("for-await break tears down the socket via return()", async () => {
    daemon.stream("daimon.provider.stream", {
      deltas: ["a", "b", "c", "d"],
      terminal: {
        response: {
          model: "m",
          content: "abcd",
          stop_reason: "end",
          usage: { input_tokens: 0, output_tokens: 4 },
        },
      },
    });
    const stream = await client.provider.stream({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
    });
    const seen: string[] = [];
    for await (const d of stream) {
      seen.push(d);
      if (seen.length === 1) break;
    }
    expect(seen).toEqual(["a"]);
    // Iteration broke before terminal — final stays null, socket is torn down.
    expect(stream.final).toBeNull();
  });

  it("peer closes without terminal raises RPCError", async () => {
    daemon.stream("daimon.provider.stream", { deltas: [], terminal: {} });
    daemon.serveStream = (conn) => {
      conn.write(
        JSON.stringify({
          jsonrpc: "2.0",
          method: "daimon.provider.stream.delta",
          params: { content: "X" },
        }) + "\n",
      );
      conn.write(
        JSON.stringify({
          jsonrpc: "2.0",
          method: "daimon.provider.stream.delta",
          params: { content: "Y" },
        }) + "\n",
      );
      // No terminal — close abruptly.
      conn.end();
    };
    try {
      const stream = await client.provider.stream({
        provider: "ollama",
        model: "llama3.2",
        messages: [{ role: "user", content: "hi" }],
      });
      const a = await stream.next();
      expect(a.value).toBe("X");
      const b = await stream.next();
      expect(b.value).toBe("Y");
      try {
        await stream.next();
        throw new Error("expected RPCError");
      } catch (e) {
        expect(e).toBeInstanceOf(RPCError);
        expect((e as RPCError).rpcMessage).toContain("without terminal response");
      }
    } finally {
      daemon.serveStream = null;
    }
  });
});
