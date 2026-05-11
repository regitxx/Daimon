import * as fs from "node:fs";
import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Client } from "../src/client.js";
import { DaemonLocked, RPCError } from "../src/errors.js";
import { ENV_VAR } from "../src/home.js";
import {
  StubDaemon,
  StubRPCError,
  cleanupShortTmp,
  makeShortTmp,
  startStubDaemon,
} from "./stub-daemon.js";

describe("Client construction", () => {
  let tmp: string;
  let originalHome: string | undefined;

  beforeEach(() => {
    tmp = makeShortTmp();
    originalHome = process.env[ENV_VAR];
  });

  afterEach(() => {
    if (originalHome === undefined) delete process.env[ENV_VAR];
    else process.env[ENV_VAR] = originalHome;
    cleanupShortTmp(tmp);
  });

  it("resolves socket via DAIMON_HOME env when no overrides", () => {
    const home = path.join(tmp, "home");
    fs.mkdirSync(home, { mode: 0o700 });
    process.env[ENV_VAR] = home;
    const client = new Client();
    // macOS resolves /tmp -> /private/tmp; compare via realpathSync.
    expect(client.socketPath).toBe(
      path.join(fs.realpathSync(home), "daimon.sock"),
    );
  });

  it("socketPath override wins", async () => {
    const { daemon, tmp: stubTmp } = await startStubDaemon();
    try {
      daemon.handle("daimon.identity.get", { did: "did:key:zABC" });
      const client = new Client({ socketPath: daemon.socketPath });
      expect(await client.identity.get()).toEqual({ did: "did:key:zABC" });
    } finally {
      await daemon.stop();
      cleanupShortTmp(stubTmp);
    }
  });
});

describe("Client verbs", () => {
  let daemon: StubDaemon;
  let tmp: string;
  let client: Client;

  beforeEach(async () => {
    ({ daemon, tmp } = await startStubDaemon());
    client = new Client({ socketPath: daemon.socketPath });
  });

  afterEach(async () => {
    await daemon.stop();
    cleanupShortTmp(tmp);
  });

  // --- identity ------------------------------------------------------------

  it("identity.get round-trips", async () => {
    daemon.handle("daimon.identity.get", { did: "did:key:zXYZ" });
    expect(await client.identity.get()).toEqual({ did: "did:key:zXYZ" });
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.identity.get");
    expect(last.params).toBeUndefined();
  });

  // --- memory --------------------------------------------------------------

  it("memory.write minimal", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.memory.write", (p) => {
      Object.assign(received, p as Record<string, unknown>);
      return { id: "01K7Q" };
    });
    const out = await client.memory.write({ kind: "note", content: "hello" });
    expect(out).toEqual({ id: "01K7Q" });
    expect(received).toEqual({ kind: "note", content: "hello" });
  });

  it("memory.write threads metadata and source", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.memory.write", (p) => {
      Object.assign(received, p as Record<string, unknown>);
      return { id: "01K" };
    });
    await client.memory.write({
      kind: "note",
      content: "x",
      metadata: { tag: "draft" },
      source: "cli",
    });
    expect(received["metadata"]).toEqual({ tag: "draft" });
    expect(received["source"]).toBe("cli");
  });

  it("memory.read round-trips", async () => {
    daemon.handle("daimon.memory.read", (p) => ({
      id: (p as { id: string }).id,
      kind: "note",
      content: "the content",
      metadata: {},
      created_at: 1_700_000_000,
    }));
    const mem = await client.memory.read("01K");
    expect(mem["id"]).toBe("01K");
    expect(mem["content"]).toBe("the content");
  });

  it("memory.search returns a list", async () => {
    daemon.handle("daimon.memory.search", () => [
      { id: "01A", kind: "note", content: "alpha", score: 0.9 },
      { id: "01B", kind: "note", content: "alpha-ish", score: 0.7 },
    ]);
    const hits = await client.memory.search("alpha", { limit: 5, kind: "note" });
    expect(hits.length).toBe(2);
    expect((hits[0] as { score: number }).score).toBe(0.9);
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.memory.search");
    expect(last.params).toEqual({ query: "alpha", limit: 5, kind: "note" });
  });

  it("memory.search normalises null to []", async () => {
    daemon.handle("daimon.memory.search", () => null);
    expect(await client.memory.search("nothingever")).toEqual([]);
  });

  it("memory.list uses empty query", async () => {
    daemon.handle("daimon.memory.search", () => []);
    await client.memory.list();
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.memory.search");
    expect(last.params).toEqual({ query: "" });
  });

  it("locked daemon propagates as DaemonLocked", async () => {
    daemon.handle("daimon.memory.write", () => {
      throw new StubRPCError(-32001, "daemon is locked");
    });
    await expect(
      client.memory.write({ kind: "note", content: "x" }),
    ).rejects.toBeInstanceOf(DaemonLocked);
  });

  // --- provider ------------------------------------------------------------

  it("provider.list returns entries", async () => {
    daemon.handle("daimon.provider.list", [
      {
        name: "ollama",
        models: [{ id: "llama3.2:latest" }],
        configured: true,
      },
      { name: "anthropic", models: [], configured: false },
    ]);
    const out = await client.provider.list();
    expect(out.length).toBe(2);
    expect((out[0] as { name: string }).name).toBe("ollama");
    expect((out[0] as { configured: boolean }).configured).toBe(true);
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.provider.list");
    expect(last.params).toBeUndefined();
  });

  it("provider.list normalises null to []", async () => {
    daemon.handle("daimon.provider.list", () => null);
    expect(await client.provider.list()).toEqual([]);
  });

  it("provider.invoke assembles nested request", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.provider.invoke", (p) => {
      Object.assign(received, p as Record<string, unknown>);
      return {
        response: {
          model: "llama3.2",
          content: "hello back",
          stop_reason: "end_turn",
          usage: { input_tokens: 4, output_tokens: 2 },
        },
      };
    });
    const env = await client.provider.invoke({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
    });
    expect((env["response"] as { content: string }).content).toBe("hello back");
    expect(received["provider"]).toBe("ollama");
    const req = received["request"] as Record<string, unknown>;
    expect(req["model"]).toBe("llama3.2");
    expect(req["messages"]).toEqual([{ role: "user", content: "hi" }]);
    expect("system" in req).toBe(false);
    expect("temperature" in req).toBe(false);
    expect("max_tokens" in req).toBe(false);
    expect("inject_context" in received).toBe(false);
  });

  it("provider.invoke threads optional fields", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.provider.invoke", (p) => {
      Object.assign(received, p as Record<string, unknown>);
      return {
        response: {
          model: "m",
          content: "",
          stop_reason: "",
          usage: { input_tokens: 0, output_tokens: 0 },
        },
      };
    });
    await client.provider.invoke({
      provider: "anthropic",
      model: "claude-sonnet-4-6",
      messages: [{ role: "user", content: "x" }],
      system: "be terse",
      temperature: 0.5,
      max_tokens: 128,
    });
    const req = received["request"] as Record<string, unknown>;
    expect(req["system"]).toBe("be terse");
    expect(req["temperature"]).toBe(0.5);
    expect(req["max_tokens"]).toBe(128);
  });

  it("provider.invoke passes inject_context verbatim", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.provider.invoke", (p) => {
      Object.assign(received, p as Record<string, unknown>);
      return {
        response: {
          model: "m",
          content: "ok",
          stop_reason: "end",
          usage: { input_tokens: 0, output_tokens: 0 },
        },
        injected_memory_ids: ["01ABC", "01DEF"],
      };
    });
    const env = await client.provider.invoke({
      provider: "ollama",
      model: "llama3.2",
      messages: [{ role: "user", content: "hi" }],
      inject_context: { query: "todo", max_tokens: 256, kinds: ["fact"] },
    });
    expect(received["inject_context"]).toEqual({
      query: "todo",
      max_tokens: 256,
      kinds: ["fact"],
    });
    expect(env["injected_memory_ids"]).toEqual(["01ABC", "01DEF"]);
  });

  it("provider.invoke propagates RPC errors", async () => {
    daemon.handle("daimon.provider.invoke", () => {
      throw new StubRPCError(-32002, "no provider registry attached to this daimon");
    });
    const promise = client.provider.invoke({
      provider: "anthropic",
      messages: [{ role: "user", content: "x" }],
    });
    await expect(promise).rejects.toBeInstanceOf(RPCError);
    try {
      await promise;
    } catch (e) {
      expect((e as RPCError).code).toBe(-32002);
    }
  });

  // --- activity ------------------------------------------------------------

  it("activity.append minimal", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.activity.append", (p) => {
      Object.assign(received, p as Record<string, unknown>);
      return { id: "01KQ", hash: "abc123" };
    });
    const out = await client.activity.append({ kind: "custom.event" });
    expect(out).toEqual({ id: "01KQ", hash: "abc123" });
    expect(received).toEqual({ kind: "custom.event" });
  });

  it("activity.append with payload", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.activity.append", (p) => {
      Object.assign(received, p as Record<string, unknown>);
      return { id: "01K", hash: "h" };
    });
    await client.activity.append({
      kind: "custom.event",
      payload: { actor: "huckgod", n: 42 },
    });
    expect(received["payload"]).toEqual({ actor: "huckgod", n: 42 });
  });

  it("activity.query returns entries", async () => {
    daemon.handle("daimon.activity.query", () => [
      {
        id: "01A",
        ts: 1_700_000_000_000,
        kind: "memory.write",
        payload: { id: "01M", kind: "fact" },
        prev_hash: "00",
        hash: "11",
      },
    ]);
    const entries = await client.activity.query();
    expect(entries.length).toBe(1);
    expect((entries[0] as { kind: string }).kind).toBe("memory.write");
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.activity.query");
    // No filters → params omitted entirely.
    expect(last.params).toBeUndefined();
  });

  it("activity.query threads filters", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.activity.query", (p) => {
      Object.assign(received, (p ?? {}) as Record<string, unknown>);
      return [];
    });
    await client.activity.query({
      since: 1_700_000_000_000,
      until: 1_800_000_000_000,
      kind: "memory.write",
      limit: 20,
    });
    expect(received).toEqual({
      since: 1_700_000_000_000,
      until: 1_800_000_000_000,
      kind: "memory.write",
      limit: 20,
    });
  });

  it("activity.query normalises null to []", async () => {
    daemon.handle("daimon.activity.query", () => null);
    expect(await client.activity.query()).toEqual([]);
  });

  it("activity.verify returns envelope with {} params", async () => {
    daemon.handle("daimon.activity.verify", { verified: 7, ok: true });
    const out = await client.activity.verify();
    expect(out).toEqual({ verified: 7, ok: true });
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.activity.verify");
    expect(last.params).toEqual({});
  });

  it("activity.verify chain failure propagates", async () => {
    daemon.handle("daimon.activity.verify", () => {
      throw new StubRPCError(-32603, "verify: chain broken at entry 3");
    });
    const promise = client.activity.verify();
    await expect(promise).rejects.toBeInstanceOf(RPCError);
    try {
      await promise;
    } catch (e) {
      const err = e as RPCError;
      expect(err.code).toBe(-32603);
      expect(err.rpcMessage).toContain("chain broken");
    }
  });
});
