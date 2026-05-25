/**
 * SDK verb tests: client.capability.* and client.reputation.* (v0.4 phase 46).
 *
 * Mirrors sdk/python/tests/test_capability_reputation.py — same test cases,
 * same stub responses, so the two SDKs stay byte-identical on the wire.
 *
 * All tests use StubDaemon; no real daemon required.
 */

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Client } from "../src/client.js";
import { RPCError } from "../src/errors.js";
import {
  StubDaemon,
  StubRPCError,
  cleanupShortTmp,
  startStubDaemon,
} from "./stub-daemon.js";

// ---------------------------------------------------------------------------
// capability.issue
// ---------------------------------------------------------------------------

describe("client.capability.issue", () => {
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

  it("sends a minimal request with only verbs", async () => {
    daemon.handle("daimon.capability.issue", {
      token_id: "01KTOKEN",
      token: "base64urltoken",
      expires_at: "",
    });

    const result = await client.capability.issue({ verbs: ["peer.ask"] });

    expect(result.token_id).toBe("01KTOKEN");
    expect(result.token).toBe("base64urltoken");
    expect(result.expires_at).toBe("");

    const last = daemon.calls[daemon.calls.length - 1];
    expect(last.method).toBe("daimon.capability.issue");
    expect(last.params).toEqual({ verbs: ["peer.ask"] });
  });

  it("forwards every optional constraint with snake_case wire names", async () => {
    daemon.handle("daimon.capability.issue", {
      token_id: "01KTOKEN",
      token: "base64urltoken",
      expires_at: "2027-01-01T00:00:00Z",
    });

    await client.capability.issue({
      verbs: ["peer.ask", "peer.echo"],
      validUntil: "2027-01-01T00:00:00Z",
      maxCalls: 10,
      modelConstraint: "claude-haiku-4-5",
      granteeDID: "did:key:z6MkPeer",
    });

    const last = daemon.calls[daemon.calls.length - 1];
    expect(last.params).toEqual({
      verbs: ["peer.ask", "peer.echo"],
      valid_until: "2027-01-01T00:00:00Z",
      max_calls: 10,
      model_constraint: "claude-haiku-4-5",
      grantee_did: "did:key:z6MkPeer",
    });
  });
});

// ---------------------------------------------------------------------------
// capability.list
// ---------------------------------------------------------------------------

describe("client.capability.list", () => {
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

  it("returns tokens from the wire envelope", async () => {
    daemon.handle("daimon.capability.list", {
      tokens: [
        {
          token_id: "01KTOKEN",
          verbs: ["peer.ask"],
          grantee_did: "did:key:z6MkPeer",
          issued_at: "2026-05-24T20:00:00Z",
          revoked: false,
        },
      ],
    });

    const tokens = await client.capability.list();

    expect(tokens).toHaveLength(1);
    expect(tokens[0].token_id).toBe("01KTOKEN");
    expect(tokens[0].verbs).toEqual(["peer.ask"]);
    expect(tokens[0].revoked).toBe(false);

    const last = daemon.calls[daemon.calls.length - 1];
    expect(last.method).toBe("daimon.capability.list");
    expect(last.params).toEqual({ include_revoked: false });
  });

  it("propagates includeRevoked: true", async () => {
    daemon.handle("daimon.capability.list", { tokens: [] });
    await client.capability.list({ includeRevoked: true });
    const last = daemon.calls[daemon.calls.length - 1];
    expect(last.params).toEqual({ include_revoked: true });
  });

  it("normalises empty/missing response to []", async () => {
    daemon.handle("daimon.capability.list", {});
    const tokens = await client.capability.list();
    expect(tokens).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// capability.revoke
// ---------------------------------------------------------------------------

describe("client.capability.revoke", () => {
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

  it("sends token_id and returns void", async () => {
    daemon.handle("daimon.capability.revoke", {});

    const result = await client.capability.revoke("01KTOKEN");

    expect(result).toBeUndefined();
    const last = daemon.calls[daemon.calls.length - 1];
    expect(last.method).toBe("daimon.capability.revoke");
    expect(last.params).toEqual({ token_id: "01KTOKEN" });
  });
});

// ---------------------------------------------------------------------------
// capability.attenuate
// ---------------------------------------------------------------------------

describe("client.capability.attenuate", () => {
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

  it("returns the new tightened token", async () => {
    daemon.handle("daimon.capability.attenuate", { token: "tightertoken" });

    const out = await client.capability.attenuate("originaltoken", {
      validUntil: "2026-12-31T00:00:00Z",
      maxCalls: 3,
    });

    expect(out).toBe("tightertoken");
    const last = daemon.calls[daemon.calls.length - 1];
    expect(last.method).toBe("daimon.capability.attenuate");
    expect(last.params).toEqual({
      token: "originaltoken",
      valid_until: "2026-12-31T00:00:00Z",
      max_calls: 3,
    });
  });
});

// ---------------------------------------------------------------------------
// reputation.receipts
// ---------------------------------------------------------------------------

describe("client.reputation.receipts", () => {
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

  it("returns receipts with no direction filter", async () => {
    daemon.handle("daimon.reputation.receipts", {
      receipts: [
        {
          receipt_id: "01KRECEIPT",
          direction: "issued",
          served_at: "2026-05-24T20:00:00Z",
          verb: "peer.ask",
          server_did: "did:key:z6MkB",
          caller_did: "did:key:z6MkA",
          provider: "mock",
          model: "mock-1",
          input_tokens: 12,
          output_tokens: 24,
          duration_ms: 350,
          signature: "base64sig",
        },
      ],
    });

    const receipts = await client.reputation.receipts();

    expect(receipts).toHaveLength(1);
    expect(receipts[0].receipt_id).toBe("01KRECEIPT");
    expect(receipts[0].direction).toBe("issued");
    expect(receipts[0].verb).toBe("peer.ask");
    expect(receipts[0].signature).toBe("base64sig");

    const last = daemon.calls[daemon.calls.length - 1];
    expect(last.method).toBe("daimon.reputation.receipts");
    expect(last.params).toEqual({});
  });

  it("propagates direction filter on the wire", async () => {
    daemon.handle("daimon.reputation.receipts", { receipts: [] });

    await client.reputation.receipts({ direction: "issued" });
    expect(daemon.calls[daemon.calls.length - 1].params).toEqual({
      direction: "issued",
    });

    await client.reputation.receipts({ direction: "received" });
    expect(daemon.calls[daemon.calls.length - 1].params).toEqual({
      direction: "received",
    });
  });

  it("normalises empty/missing response to []", async () => {
    daemon.handle("daimon.reputation.receipts", {});
    expect(await client.reputation.receipts()).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// Error path: CodeCapabilityDenied surfaces as RPCError
// ---------------------------------------------------------------------------

describe("capability error path", () => {
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

  it("surfaces CodeCapabilityDenied (-32014) as RPCError", async () => {
    daemon.handle("daimon.capability.revoke", () => {
      throw new StubRPCError(-32014, "capability: token expired");
    });

    await expect(client.capability.revoke("01KTOKEN")).rejects.toThrow(
      RPCError,
    );
    try {
      await client.capability.revoke("01KTOKEN");
    } catch (e) {
      expect((e as RPCError).code).toBe(-32014);
      expect((e as RPCError).message.toLowerCase()).toContain("expired");
    }
  });
});
