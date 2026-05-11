import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { DaemonLocked, DaemonNotRunning, RPCError } from "../src/errors.js";
import { rpcCall } from "../src/rpc.js";
import {
  StubDaemon,
  StubRPCError,
  cleanupShortTmp,
  startStubDaemon,
} from "./stub-daemon.js";

describe("rpc", () => {
  let daemon: StubDaemon;
  let tmp: string;

  beforeEach(async () => {
    ({ daemon, tmp } = await startStubDaemon());
  });

  afterEach(async () => {
    await daemon.stop();
    cleanupShortTmp(tmp);
  });

  it("returns the decoded result", async () => {
    daemon.handle("daimon.identity.get", { did: "did:key:zABC" });
    const result = await rpcCall(daemon.socketPath, "daimon.identity.get", undefined);
    expect(result).toEqual({ did: "did:key:zABC" });
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.identity.get");
    // Mirror Go's json.Marshal: when params is undefined we omit the
    // params key entirely so the server sees no params field at all.
    expect(last.params).toBeUndefined();
  });

  it("sends params when given", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.memory.write", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return { id: "01K" };
    });
    await rpcCall(daemon.socketPath, "daimon.memory.write", {
      kind: "fact",
      content: "hi",
    });
    expect(received).toEqual({ kind: "fact", content: "hi" });
  });

  it("raises DaemonNotRunning when socket absent", async () => {
    const missing = path.join(tmp, "nope.sock");
    await expect(rpcCall(missing, "daimon.identity.get", undefined)).rejects.toBeInstanceOf(
      DaemonNotRunning,
    );
  });

  it("raises DaemonLocked on code -32001", async () => {
    daemon.handle("daimon.identity.get", () => {
      throw new StubRPCError(-32001, "unlock failed", "wrong password");
    });
    const promise = rpcCall(daemon.socketPath, "daimon.identity.get", undefined);
    await expect(promise).rejects.toBeInstanceOf(DaemonLocked);
    try {
      await promise;
    } catch (e) {
      const err = e as DaemonLocked;
      expect(err.code).toBe(-32001);
      expect(err.rpcMessage).toBe("unlock failed");
    }
  });

  it("raises RPCError for other codes", async () => {
    daemon.handle("daimon.memory.read", () => {
      throw new StubRPCError(-32602, "id is required");
    });
    const promise = rpcCall(daemon.socketPath, "daimon.memory.read", { id: "" });
    await expect(promise).rejects.toBeInstanceOf(RPCError);
    try {
      await promise;
    } catch (e) {
      const err = e as RPCError;
      expect(err.code).toBe(-32602);
      expect(err.rpcMessage).toContain("id is required");
    }
  });

  it("raises RPCError on unknown method", async () => {
    const promise = rpcCall(daemon.socketPath, "daimon.bogus", undefined);
    await expect(promise).rejects.toBeInstanceOf(RPCError);
    try {
      await promise;
    } catch (e) {
      const err = e as RPCError;
      expect(err.code).toBe(-32601);
      expect(err.rpcMessage).toContain("method not found");
    }
  });

  it("returns null when result omitted (SPEC §6.1 silent success)", async () => {
    daemon.handle("daimon.silent", () => null);
    const out = await rpcCall(daemon.socketPath, "daimon.silent", undefined);
    expect(out).toBeNull();
  });
});
