/**
 * v0.2 verb-level tests: client.wallet.* and client.payment.pay.
 * Mirrors sdk/python/tests/test_wallet_payment.py on the wire shape.
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

describe("Client.wallet", () => {
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

  it("list normalises null to []", async () => {
    daemon.handle("daimon.wallet.list", () => null);
    const out = await client.wallet.list();
    expect(out).toEqual([]);
  });

  it("list returns entries", async () => {
    daemon.handle("daimon.wallet.list", () => [
      {
        id: "01K",
        chain: "evm:base",
        path: "m/44'/60'/0'/0/0",
        address: "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
        pubkey: "02" + "00".repeat(32),
        created_at: 1_700_000_000,
      },
    ]);
    const out = await client.wallet.list();
    expect(out.length).toBe(1);
    expect(out[0]!.chain).toBe("evm:base");
    expect(out[0]!.address.startsWith("0x")).toBe(true);
  });

  it("create threads chain param", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.wallet.create", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return {
        id: "01K",
        chain: (params as { chain: string }).chain,
        path: "m/44'/60'/0'/0/0",
        address: "0xFFFF000000000000000000000000000000000000",
        pubkey: "02" + "ff".repeat(32),
        created_at: 1_700_000_000,
      };
    });
    const out = await client.wallet.create({ chain: "evm:base-sepolia" });
    expect(received).toEqual({ chain: "evm:base-sepolia" });
    expect(out.chain).toBe("evm:base-sepolia");
  });

  it("address returns string", async () => {
    daemon.handle("daimon.wallet.address", () => ({ address: "0x123" }));
    const addr = await client.wallet.address({ chain: "evm:base" });
    expect(addr).toBe("0x123");
  });

  it("sign returns hex signature", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.wallet.sign", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return { signature_hex: "0x" + "ab".repeat(65) };
    });
    const sig = await client.wallet.sign({
      chain: "evm:base",
      digestHex: "0x" + "11".repeat(32),
    });
    expect(received).toEqual({
      chain: "evm:base",
      digest_hex: "0x" + "11".repeat(32),
    });
    expect(sig.startsWith("0x")).toBe(true);
    expect(sig.length).toBe(2 + 130);
  });

  it("derive threads chain + index", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.wallet.derive", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      const p = params as { chain: string; index: number };
      return {
        chain: p.chain,
        path: `m/44'/60'/0'/0/${p.index}`,
        address: "0xDEADBEEF000000000000000000000000DEADBEEF",
        pubkey: "03" + "ab".repeat(32),
      };
    });
    const out = await client.wallet.derive({ chain: "evm:base", index: 5 });
    expect(received).toEqual({ chain: "evm:base", index: 5 });
    expect(out.path).toBe("m/44'/60'/0'/0/5");
    expect(out.address.startsWith("0x")).toBe(true);
  });

  it("derive defaults index to 0", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.wallet.derive", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return {
        chain: (params as { chain: string }).chain,
        path: "m/44'/60'/0'/0/0",
        address: "0x0000000000000000000000000000000000000000",
        pubkey: "02" + "00".repeat(32),
      };
    });
    await client.wallet.derive({ chain: "evm:base" });
    expect(received).toEqual({ chain: "evm:base", index: 0 });
  });

  it("show-mnemonic returns the 24-word seed list", async () => {
    const received: Record<string, unknown> = {};
    const seed = [
      "abandon", "abandon", "abandon", "abandon",
      "abandon", "abandon", "abandon", "abandon",
      "abandon", "abandon", "abandon", "abandon",
      "abandon", "abandon", "abandon", "abandon",
      "abandon", "abandon", "abandon", "abandon",
      "abandon", "abandon", "abandon", "art",
    ];
    daemon.handle("daimon.wallet.show_mnemonic", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return { mnemonic: seed };
    });
    const out = await client.wallet.showMnemonic({ password: "hunter2" });
    expect(received).toEqual({ password: "hunter2" });
    expect(out).toEqual(seed);
  });

  it("show-mnemonic wrong-password surfaces typed code -32008", async () => {
    daemon.handle("daimon.wallet.show_mnemonic", () => {
      // CodeWrongPassword — distinct from CodeIdentityLocked so the
      // SDK doesn't trigger the "daemon is locked" rewrite.
      throw new StubRPCError(-32008, "wrong password");
    });
    try {
      await client.wallet.showMnemonic({ password: "WRONG" });
      throw new Error("expected RPCError");
    } catch (e) {
      expect(e).toBeInstanceOf(RPCError);
      expect((e as RPCError).code).toBe(-32008);
    }
  });

  it("unsupported-chain rejection surfaces RPCError", async () => {
    daemon.handle("daimon.wallet.create", () => {
      throw new StubRPCError(-32602, "unsupported chain", "stellar");
    });
    await expect(client.wallet.create({ chain: "stellar" })).rejects.toBeInstanceOf(RPCError);
  });
});

describe("Client.payment.pay", () => {
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

  it("happy path decodes body + surfaces payment_response", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.payment.pay", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      const bodyB64 = Buffer.from("paid resource").toString("base64");
      return {
        status_code: 200,
        response_headers: { "Content-Type": "text/plain" },
        response_body_base64: bodyB64,
        payment_response: {
          success: true,
          transaction: "0xabc",
          network: "base",
          payer: "0xff",
        },
      };
    });
    const out = await client.payment.pay({
      url: "https://example.com/r",
      ceilingSmallestUnit: 100000n,
    });
    expect(received["url"]).toBe("https://example.com/r");
    expect(received["method"]).toBe("GET");
    expect(received["ceiling_smallest_unit"]).toBe("100000");
    expect(out.statusCode).toBe(200);
    expect(Buffer.from(out.body).toString()).toBe("paid resource");
    expect(out.paymentResponse?.success).toBe(true);
    expect(out.paymentResponse?.transaction).toBe("0xabc");
  });

  it("body Uint8Array becomes body_base64", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.payment.pay", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return {
        status_code: 200,
        response_headers: {},
        response_body_base64: "",
      };
    });
    const bytes = new Uint8Array([0, 1, 2, 104, 101, 108, 108, 111]); // \x00\x01\x02hello
    await client.payment.pay({
      url: "https://e.invalid/",
      body: bytes,
    });
    expect(received["body_base64"]).toBe(Buffer.from(bytes).toString("base64"));
    expect("body_text" in received).toBe(false);
  });

  it("body string becomes body_text", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.payment.pay", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return { status_code: 200, response_headers: {}, response_body_base64: "" };
    });
    await client.payment.pay({ url: "https://e.invalid/", body: "hello world" });
    expect(received["body_text"]).toBe("hello world");
    expect("body_base64" in received).toBe(false);
  });

  it("optional params omitted when undefined", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.payment.pay", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return { status_code: 200, response_headers: {}, response_body_base64: "" };
    });
    await client.payment.pay({ url: "https://e.invalid/" });
    expect(Object.keys(received).sort()).toEqual(["method", "url"]);
  });

  it("threads headers", async () => {
    const received: Record<string, unknown> = {};
    daemon.handle("daimon.payment.pay", (params) => {
      Object.assign(received, params as Record<string, unknown>);
      return { status_code: 200, response_headers: {}, response_body_base64: "" };
    });
    await client.payment.pay({
      url: "https://e.invalid/",
      headers: { "X-Custom": "v" },
    });
    expect(received["headers"]).toEqual({ "X-Custom": "v" });
  });

  it("ceiling-exceeded surfaces typed code", async () => {
    daemon.handle("daimon.payment.pay", () => {
      throw new StubRPCError(-32006, "payment exceeds local ceiling", "required > ceiling");
    });
    try {
      await client.payment.pay({
        url: "https://e.invalid/",
        ceilingSmallestUnit: 100,
      });
      throw new Error("expected RPCError");
    } catch (e) {
      expect(e).toBeInstanceOf(RPCError);
      expect((e as RPCError).code).toBe(-32006);
    }
  });

  it("no-compatible-requirement surfaces typed code", async () => {
    daemon.handle("daimon.payment.pay", () => {
      throw new StubRPCError(-32007, "no wallet matches", "polygon not in v0.2 registry");
    });
    try {
      await client.payment.pay({ url: "https://e.invalid/" });
      throw new Error("expected RPCError");
    } catch (e) {
      expect(e).toBeInstanceOf(RPCError);
      expect((e as RPCError).code).toBe(-32007);
    }
  });

  it("handles missing body field on 204-style responses", async () => {
    daemon.handle("daimon.payment.pay", () => ({ status_code: 204 }));
    const out = await client.payment.pay({ url: "https://e.invalid/" });
    expect(out.statusCode).toBe(204);
    expect(out.body.length).toBe(0);
    expect(out.responseHeaders).toEqual({});
    expect(out.paymentResponse).toBeNull();
  });
});
