/**
 * SDK verb tests: client.federation.* and client.peer.* (v0.3 phase 36).
 *
 * All tests use StubDaemon — no real daemon required. The stub speaks
 * byte-for-byte JSON-RPC 2.0 so the SDK wire-encoding path is fully
 * exercised without touching the network.
 */

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Client } from "../src/client.js";
import { StubDaemon, cleanupShortTmp, startStubDaemon } from "./stub-daemon.js";

// ---------------------------------------------------------------------------
// federation.config
// ---------------------------------------------------------------------------

describe("client.federation.config", () => {
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

  it("returns all fields including public_endpoint", async () => {
    daemon.handle("daimon.federation.config", {
      did: "did:key:z6MkHello",
      transport_pubkey_multibase: "z6MkHello",
      did_methods: ["did:key"],
      protocols: ["peer.echo", "peer.ask", "peer.pay.required"],
      public_endpoint: "tcp://127.0.0.1:9999",
      federation_version: "v0.3-draft",
    });

    const cfg = await client.federation.config();

    expect(cfg.did).toBe("did:key:z6MkHello");
    expect(cfg.transport_pubkey_multibase).toBe("z6MkHello");
    expect(cfg.did_methods).toEqual(["did:key"]);
    expect(cfg.protocols).toContain("peer.echo");
    expect(cfg.protocols).toContain("peer.ask");
    expect(cfg.protocols).toContain("peer.pay.required");
    expect(cfg.public_endpoint).toBe("tcp://127.0.0.1:9999");
    expect(cfg.federation_version).toBe("v0.3-draft");
  });

  it("sends no params on the wire", async () => {
    daemon.handle("daimon.federation.config", {
      did: "did:key:z6MkX",
      transport_pubkey_multibase: "z6MkX",
      did_methods: ["did:key"],
      protocols: [],
      federation_version: "v0.3-draft",
    });

    await client.federation.config();

    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.federation.config");
    expect(last.params).toBeUndefined();
  });

  it("works without public_endpoint (PeerListen not called)", async () => {
    daemon.handle("daimon.federation.config", {
      did: "did:key:z6MkNoListen",
      transport_pubkey_multibase: "z6MkNoListen",
      did_methods: ["did:key"],
      protocols: ["peer.echo"],
      federation_version: "v0.3-draft",
    });

    const cfg = await client.federation.config();
    expect(cfg.public_endpoint).toBeUndefined();
    expect(cfg.did).toBe("did:key:z6MkNoListen");
  });
});

// ---------------------------------------------------------------------------
// peer.dial / peer.close / peer.list
// ---------------------------------------------------------------------------

describe("client.peer.dial / close / list", () => {
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

  it("dial sends correct wire shape and returns channel metadata", async () => {
    daemon.handle("daimon.peer.dial", {
      channel_id: "01AAAA",
      peer_did: "did:key:z6MkPeer",
      opened_at: "2026-05-21T10:00:00Z",
    });

    const result = await client.peer.dial({
      did: "did:key:z6MkPeer",
      endpoint: "tcp://127.0.0.1:9999",
    });

    expect(result.channel_id).toBe("01AAAA");
    expect(result.peer_did).toBe("did:key:z6MkPeer");
    expect(result.opened_at).toBe("2026-05-21T10:00:00Z");

    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.dial");
    expect((last.params as Record<string, unknown>)["did"]).toBe("did:key:z6MkPeer");
    expect((last.params as Record<string, unknown>)["endpoint"]).toBe("tcp://127.0.0.1:9999");
  });

  it("close sends channel_id and resolves without error", async () => {
    daemon.handle("daimon.peer.close", {});

    await client.peer.close("01AAAA");

    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.close");
    expect((last.params as Record<string, unknown>)["channel_id"]).toBe("01AAAA");
  });

  it("list returns the channels array", async () => {
    daemon.handle("daimon.peer.list", {
      channels: [
        {
          channel_id: "01BBBB",
          peer_did: "did:key:z6MkB",
          opened_at: "2026-05-21T10:00:00Z",
        },
      ],
    });

    const channels = await client.peer.list();

    expect(channels).toHaveLength(1);
    expect(channels[0]!.channel_id).toBe("01BBBB");
    expect(channels[0]!.peer_did).toBe("did:key:z6MkB");
  });

  it("list returns [] when channels array is empty", async () => {
    daemon.handle("daimon.peer.list", { channels: [] });
    expect(await client.peer.list()).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// peer.invoke / peer.echo / peer.payRequired
// ---------------------------------------------------------------------------

describe("client.peer.invoke / echo / payRequired", () => {
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

  it("invoke sends correct wire shape and unwraps result", async () => {
    daemon.handle("daimon.peer.invoke", {
      result: { message: "hello", from_did: "did:key:z6MkRemote" },
    });

    const result = await client.peer.invoke("01AAAA", "peer.echo", { message: "hello" });

    expect(result).toEqual({ message: "hello", from_did: "did:key:z6MkRemote" });

    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.invoke");
    const p = last.params as Record<string, unknown>;
    expect(p["channel_id"]).toBe("01AAAA");
    expect(p["method"]).toBe("peer.echo");
    expect(p["params"]).toEqual({ message: "hello" });
  });

  it("invoke without params omits params key from wire shape", async () => {
    daemon.handle("daimon.peer.invoke", { result: {} });

    await client.peer.invoke("01AAAA", "peer.echo");

    const last = daemon.calls[daemon.calls.length - 1]!;
    const p = last.params as Record<string, unknown>;
    expect(Object.prototype.hasOwnProperty.call(p, "params")).toBe(false);
  });

  it("echo wraps invoke with peer.echo method and returns result", async () => {
    daemon.handle("daimon.peer.invoke", {
      result: { message: "hi", from_did: "did:key:z6MkRemote" },
    });

    const result = await client.peer.echo("01AAAA", "hi");

    expect(result["message"]).toBe("hi");
    expect(result["from_did"]).toBe("did:key:z6MkRemote");

    const last = daemon.calls[daemon.calls.length - 1]!;
    const p = last.params as Record<string, unknown>;
    expect(p["method"]).toBe("peer.echo");
    expect((p["params"] as Record<string, unknown>)["message"]).toBe("hi");
  });

  it("payRequired returns the requirements list", async () => {
    const req = {
      scheme: "exact",
      network: "base-sepolia",
      maxAmountRequired: "1000000",
      resource: "peer.ask",
      description: "1.00 USDC",
      payTo: "0xDEAD",
      maxTimeoutSeconds: 300,
      asset: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    };
    daemon.handle("daimon.peer.invoke", { result: { requirements: [req] } });

    const reqs = await client.peer.payRequired("01AAAA", "peer.ask");

    expect(reqs).toHaveLength(1);
    expect(reqs[0]!.scheme).toBe("exact");
    expect(reqs[0]!.network).toBe("base-sepolia");
    expect(reqs[0]!.payTo).toBe("0xDEAD");
    expect(reqs[0]!.maxAmountRequired).toBe("1000000");

    const last = daemon.calls[daemon.calls.length - 1]!;
    const p = last.params as Record<string, unknown>;
    expect(p["method"]).toBe("peer.pay.required");
    expect((p["params"] as Record<string, unknown>)["service"]).toBe("peer.ask");
  });

  it("payRequired returns [] when stub returns null result", async () => {
    daemon.handle("daimon.peer.invoke", { result: null });
    expect(await client.peer.payRequired("01AAAA", "peer.ask")).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// peer.addressBook.*
// ---------------------------------------------------------------------------

describe("client.peer.addressBook", () => {
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

  it("list returns entries", async () => {
    daemon.handle("daimon.peer.address_book.list", {
      entries: [
        {
          did: "did:key:z6MkPeer",
          label: "Alice",
          status: "Pinned",
          approved_verbs: ["peer.ask"],
          transport_pubkey_multibase: "z6MkPeer",
          first_seen: "2026-05-21T00:00:00Z",
          last_seen: "2026-05-21T10:00:00Z",
        },
      ],
    });

    const entries = await client.peer.addressBook.list();

    expect(entries).toHaveLength(1);
    expect(entries[0]!.did).toBe("did:key:z6MkPeer");
    expect(entries[0]!.status).toBe("Pinned");
    expect(entries[0]!.approved_verbs).toContain("peer.ask");
  });

  it("list returns [] when entries is empty", async () => {
    daemon.handle("daimon.peer.address_book.list", { entries: [] });
    expect(await client.peer.addressBook.list()).toEqual([]);
  });

  it("add sends at minimum {did} on the wire", async () => {
    daemon.handle("daimon.peer.address_book.add", { ok: true });

    await client.peer.addressBook.add({ did: "did:key:z6MkNew" });

    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.address_book.add");
    const p = last.params as Record<string, unknown>;
    expect(p["did"]).toBe("did:key:z6MkNew");
    expect(Object.prototype.hasOwnProperty.call(p, "label")).toBe(false);
    expect(Object.prototype.hasOwnProperty.call(p, "pubkey_multibase")).toBe(false);
  });

  it("add sends label and pubkeyMultibase when supplied", async () => {
    daemon.handle("daimon.peer.address_book.add", { ok: true });

    await client.peer.addressBook.add({
      did: "did:key:z6MkNew",
      label: "Bob",
      pubkeyMultibase: "z6MkNew",
    });

    const last = daemon.calls[daemon.calls.length - 1]!;
    const p = last.params as Record<string, unknown>;
    expect(p["label"]).toBe("Bob");
    expect(p["pubkey_multibase"]).toBe("z6MkNew");
  });

  it("pin sends {did, verbs}", async () => {
    daemon.handle("daimon.peer.address_book.pin", { ok: true });

    await client.peer.addressBook.pin({ did: "did:key:z6MkPeer", verbs: ["peer.ask"] });

    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.address_book.pin");
    const p = last.params as Record<string, unknown>;
    expect(p["did"]).toBe("did:key:z6MkPeer");
    expect(p["verbs"]).toEqual(["peer.ask"]);
  });

  it("block sends did", async () => {
    daemon.handle("daimon.peer.address_book.block", { ok: true });
    await client.peer.addressBook.block("did:key:z6MkEvil");
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.address_book.block");
    expect((last.params as Record<string, unknown>)["did"]).toBe("did:key:z6MkEvil");
  });

  it("unblock sends did", async () => {
    daemon.handle("daimon.peer.address_book.unblock", { ok: true });
    await client.peer.addressBook.unblock("did:key:z6MkEvil");
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.address_book.unblock");
    expect((last.params as Record<string, unknown>)["did"]).toBe("did:key:z6MkEvil");
  });

  it("remove sends did", async () => {
    daemon.handle("daimon.peer.address_book.remove", { ok: true });
    await client.peer.addressBook.remove("did:key:z6MkOld");
    const last = daemon.calls[daemon.calls.length - 1]!;
    expect(last.method).toBe("daimon.peer.address_book.remove");
    expect((last.params as Record<string, unknown>)["did"]).toBe("did:key:z6MkOld");
  });
});
