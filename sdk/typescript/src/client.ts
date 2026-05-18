/**
 * High-level Daimon client.
 *
 * Wraps the JSON-RPC primitive in ./rpc with namespaced verb groups
 * (client.identity, client.memory, client.provider, client.activity)
 * mirroring sdk/python/daimon/client.py and the Go CLI's dispatch shape.
 *
 * Return types are intentionally loose (Record<string, unknown> /
 * unknown[]): the SDK doesn't bake in schema models so it doesn't drift
 * behind the Go side's evolving record shapes. Callers narrow at the
 * call site.
 */

import * as fs from "node:fs";
import * as path from "node:path";

import { resolveHome, socketPath } from "./home.js";
import { rpcCall, type RpcOptions } from "./rpc.js";
import { openStream, StreamHandle } from "./stream.js";

export type JsonObject = Record<string, unknown>;
export type JsonArray = unknown[];

export interface ClientOptions {
  /** Override $DAIMON_HOME. Ignored when socketPath is set. */
  home?: string;
  /** Direct socket path override. Takes precedence over home. */
  socketPath?: string;
  /** Per-call socket timeout in milliseconds. */
  timeoutMs?: number;
}

export interface MemoryWriteParams {
  kind: string;
  content: string;
  metadata?: JsonObject;
  source?: string;
}

export interface MemorySearchParams {
  limit?: number;
  kind?: string;
}

export interface ProviderInvokeParams {
  provider: string;
  messages: JsonObject[];
  model?: string;
  system?: string;
  temperature?: number;
  max_tokens?: number;
  inject_context?: JsonObject;
}

export interface ProviderStreamParams extends ProviderInvokeParams {
  /** Per-stream timeout in milliseconds. Defaults to 5 minutes. */
  timeoutMs?: number;
}

export interface ActivityQueryParams {
  since?: number;
  until?: number;
  kind?: string;
  limit?: number;
}

export interface ActivityAppendParams {
  kind: string;
  payload?: JsonObject;
}

// --- v0.2 surface: wallet + payment -----------------------------------------

/** Single entry returned by `daimon.wallet.{list,create}`. */
export interface Wallet {
  id: string;
  chain: string;
  path: string;
  /** EIP-55-checksummed EVM address. */
  address: string;
  /** hex-encoded 33-byte compressed secp256k1 public key. */
  pubkey: string;
  /** unix milliseconds */
  created_at: number;
}

export interface WalletCreateParams {
  chain: string;
}

export interface WalletAddressParams {
  chain: string;
}

export interface WalletSignParams {
  chain: string;
  /** 32-byte digest, hex-encoded; `0x` prefix optional. */
  digestHex: string;
}

export interface PaymentPayParams {
  url: string;
  /** Defaults to `GET`. */
  method?: string;
  /** Extra request headers — must not include `Payment-Signature`. */
  headers?: Record<string, string>;
  /** Request body. `Uint8Array` rides via base64; `string` rides as text. */
  body?: Uint8Array | string;
  /**
   * Per-payment ceiling in the asset's smallest unit (USDC has 6
   * decimals — 100000 ≡ $0.10). `undefined` disables the ceiling —
   * STRONGLY DISCOURAGED in production. Accept as bigint / number /
   * string for ergonomics; SDK stringifies on the wire.
   */
  ceilingSmallestUnit?: bigint | number | string;
  /** EIP-3009 validBefore window override, in seconds. Default 300. */
  validitySeconds?: number;
}

/** PAYMENT-RESPONSE structure parsed by the daemon. */
export interface PaymentResponse {
  success: boolean;
  transaction?: string;
  network?: string;
  payer?: string;
}

export interface PaymentPayResult {
  statusCode: number;
  /** Small allowlist: Content-Type, Content-Length, Payment-Response. */
  responseHeaders: Record<string, string>;
  /** Decoded response body (SDK base64-decodes the wire form). */
  body: Uint8Array;
  paymentResponse: PaymentResponse | null;
}

class IdentityNamespace {
  constructor(private readonly c: Client) {}

  /** Return the principal's DID. `{did: "did:key:..."}` */
  get(): Promise<JsonObject> {
    return this.c._call("daimon.identity.get", undefined) as Promise<JsonObject>;
  }
}

class MemoryNamespace {
  constructor(private readonly c: Client) {}

  /** Write a memory record. Returns `{id: "..."}`. */
  async write(params: MemoryWriteParams): Promise<JsonObject> {
    const wire: JsonObject = { kind: params.kind, content: params.content };
    if (params.metadata !== undefined) wire["metadata"] = params.metadata;
    if (params.source !== undefined) wire["source"] = params.source;
    return (await this.c._call("daimon.memory.write", wire)) as JsonObject;
  }

  /** Read a memory by id. Returns the full memory record. */
  async read(id: string): Promise<JsonObject> {
    return (await this.c._call("daimon.memory.read", { id })) as JsonObject;
  }

  /**
   * Search memories. Returns a list of `{...memory, score}`.
   * Null result is normalised to []. Mirrors cmd_memory.go::cmdMemorySearch.
   */
  async search(query: string, params: MemorySearchParams = {}): Promise<JsonObject[]> {
    const wire: JsonObject = { query };
    if (params.limit !== undefined) wire["limit"] = params.limit;
    if (params.kind !== undefined) wire["kind"] = params.kind;
    const result = await this.c._call("daimon.memory.search", wire);
    return (result as JsonObject[] | null) ?? [];
  }

  /** List all memories — search with empty query. Mirrors cmd_memory.go::cmdMemoryList. */
  list(params: MemorySearchParams = {}): Promise<JsonObject[]> {
    return this.search("", params);
  }
}

class ProviderNamespace {
  constructor(private readonly c: Client) {}

  /** List configured providers. Null is normalised to []. */
  async list(): Promise<JsonObject[]> {
    const result = await this.c._call("daimon.provider.list", undefined);
    return (result as JsonObject[] | null) ?? [];
  }

  /**
   * Invoke a provider synchronously and return the full envelope.
   *
   * Returns the daemon's wrapping envelope verbatim:
   * `{response: {model, content, stop_reason, usage}, injected_memory_ids?}`.
   * The SDK takes flat kwargs and assembles the nested `{provider, request}`
   * wire shape internally, matching cmd_provider.go.
   */
  async invoke(params: ProviderInvokeParams): Promise<JsonObject> {
    const request: JsonObject = {
      model: params.model ?? "",
      messages: params.messages,
    };
    if (params.system !== undefined) request["system"] = params.system;
    if (params.temperature !== undefined) request["temperature"] = params.temperature;
    if (params.max_tokens !== undefined) request["max_tokens"] = params.max_tokens;

    const wire: JsonObject = { provider: params.provider, request };
    if (params.inject_context !== undefined) wire["inject_context"] = params.inject_context;
    return (await this.c._call("daimon.provider.invoke", wire)) as JsonObject;
  }

  /**
   * Stream a provider response as an async iterable of delta strings.
   *
   * The terminal envelope ({response, injected_memory_ids?}) is
   * populated on the returned handle's `.final` after iteration
   * completes. Providers without native streaming (Claude / OpenAI /
   * LM Studio) return zero deltas with the full content carried in
   * the terminal envelope; native streamers (Ollama) yield deltas
   * token-by-token.
   */
  stream(params: ProviderStreamParams): Promise<StreamHandle> {
    const request: JsonObject = {
      model: params.model ?? "",
      messages: params.messages,
    };
    if (params.system !== undefined) request["system"] = params.system;
    if (params.temperature !== undefined) request["temperature"] = params.temperature;
    if (params.max_tokens !== undefined) request["max_tokens"] = params.max_tokens;

    const wire: JsonObject = { provider: params.provider, request };
    if (params.inject_context !== undefined) wire["inject_context"] = params.inject_context;

    const opts: { timeoutMs?: number } = {};
    if (params.timeoutMs !== undefined) opts.timeoutMs = params.timeoutMs;
    return openStream(this.c.socketPath, "daimon.provider.stream", wire, opts);
  }
}

class ActivityNamespace {
  constructor(private readonly c: Client) {}

  /** Append an entry to the activity log. Returns `{id, hash}`. */
  async append(params: ActivityAppendParams): Promise<JsonObject> {
    const wire: JsonObject = { kind: params.kind };
    if (params.payload !== undefined) wire["payload"] = params.payload;
    return (await this.c._call("daimon.activity.append", wire)) as JsonObject;
  }

  /**
   * Query the activity log. Returns a list of entries.
   * Null result is normalised to []. No filters → params omitted on wire.
   */
  async query(params: ActivityQueryParams = {}): Promise<JsonObject[]> {
    const wire: JsonObject = {};
    if (params.since !== undefined) wire["since"] = params.since;
    if (params.until !== undefined) wire["until"] = params.until;
    if (params.kind !== undefined) wire["kind"] = params.kind;
    if (params.limit !== undefined) wire["limit"] = params.limit;
    const hasAny = Object.keys(wire).length > 0;
    const result = await this.c._call(
      "daimon.activity.query",
      hasAny ? wire : undefined,
    );
    return (result as JsonObject[] | null) ?? [];
  }

  /**
   * Walk the chain end-to-end. Returns `{verified, ok}`.
   * Wire shape is empty-object params (mirrors the Go CLI's struct{}{}).
   */
  async verify(): Promise<JsonObject> {
    return (await this.c._call("daimon.activity.verify", {})) as JsonObject;
  }
}

class WalletNamespace {
  constructor(private readonly c: Client) {}

  /**
   * List all wallets in the keystore.
   * `null` result is normalised to `[]` (mirrors memory.search,
   * activity.query).
   */
  async list(): Promise<Wallet[]> {
    const result = await this.c._call("daimon.wallet.list", undefined);
    return (result as Wallet[] | null) ?? [];
  }

  /**
   * Derive a new HD wallet for `chain` and persist it.
   *
   * v0.2 supports EVM chains only (`evm:base`, `evm:base-sepolia`,
   * etc.). Throws `RPCError` for unsupported chains or duplicate-chain
   * requests.
   */
  async create(params: WalletCreateParams): Promise<Wallet> {
    return (await this.c._call("daimon.wallet.create", {
      chain: params.chain,
    })) as Wallet;
  }

  /**
   * Convenience: return just the EIP-55-checksummed address for the
   * wallet bound to `chain`. Throws `RPCError` with code `-32002`
   * if no wallet exists for the chain.
   */
  async address(params: WalletAddressParams): Promise<string> {
    const result = (await this.c._call("daimon.wallet.address", {
      chain: params.chain,
    })) as { address: string };
    return result.address;
  }

  /**
   * Low-level signing primitive. Returns the 65-byte
   * `[r || s || v]` EVM signature as a `0x`-prefixed hex string.
   * Most callers should use `payment.pay` instead — it builds the
   * EIP-3009 digest internally.
   */
  async sign(params: WalletSignParams): Promise<string> {
    const result = (await this.c._call("daimon.wallet.sign", {
      chain: params.chain,
      digest_hex: params.digestHex,
    })) as { signature_hex: string };
    return result.signature_hex;
  }
}

class PaymentNamespace {
  constructor(private readonly c: Client) {}

  /**
   * Pay an x402-protected URL end-to-end. Returns the resource
   * response decoded into a structured shape.
   *
   * Throws `RPCError`:
   * - code `-32006` if the resource demanded more than
   *   `ceilingSmallestUnit`
   * - code `-32007` if no wallet matches the resource's payment
   *   requirements
   * - code `-32603` for transport / server errors
   */
  async pay(params: PaymentPayParams): Promise<PaymentPayResult> {
    const wire: JsonObject = {
      url: params.url,
      method: params.method ?? "GET",
    };
    if (params.headers !== undefined) wire["headers"] = params.headers;

    if (params.body !== undefined) {
      if (params.body instanceof Uint8Array) {
        wire["body_base64"] = uint8ToBase64(params.body);
      } else if (typeof params.body === "string") {
        wire["body_text"] = params.body;
      } else {
        throw new TypeError("body must be Uint8Array, string, or undefined");
      }
    }
    if (params.ceilingSmallestUnit !== undefined) {
      wire["ceiling_smallest_unit"] = String(params.ceilingSmallestUnit);
    }
    if (params.validitySeconds !== undefined) {
      wire["validity_seconds"] = params.validitySeconds;
    }

    const result = (await this.c._call("daimon.payment.pay", wire)) as {
      status_code?: number;
      response_headers?: Record<string, string>;
      response_body_base64?: string;
      payment_response?: PaymentResponse | null;
    };
    return {
      statusCode: result.status_code ?? 0,
      responseHeaders: result.response_headers ?? {},
      body: result.response_body_base64
        ? base64ToUint8(result.response_body_base64)
        : new Uint8Array(),
      paymentResponse: result.payment_response ?? null,
    };
  }
}

// --- base64 helpers ---------------------------------------------------------
// Node's Buffer is the canonical base64 path; using globalThis lookups so
// the SDK doesn't import @types/node into its surface unconditionally.

function uint8ToBase64(b: Uint8Array): string {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const B: any = (globalThis as any).Buffer;
  if (B) return B.from(b).toString("base64");
  // Browser fallback — works for arbitrary byte values.
  let bin = "";
  for (const x of b) bin += String.fromCharCode(x);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (globalThis as any).btoa(bin);
}

function base64ToUint8(s: string): Uint8Array {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const B: any = (globalThis as any).Buffer;
  if (B) return new Uint8Array(B.from(s, "base64"));
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const bin: string = (globalThis as any).atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export class Client {
  readonly identity: IdentityNamespace;
  readonly memory: MemoryNamespace;
  readonly provider: ProviderNamespace;
  readonly activity: ActivityNamespace;
  readonly wallet: WalletNamespace;
  readonly payment: PaymentNamespace;

  private readonly socket: string;
  private readonly timeoutMs?: number;

  constructor(options: ClientOptions = {}) {
    if (options.socketPath !== undefined) {
      this.socket = options.socketPath;
    } else {
      const home = options.home
        ? fs.realpathSync(path.resolve(options.home))
        : resolveHome();
      this.socket = socketPath(home).path;
    }
    this.timeoutMs = options.timeoutMs;
    this.identity = new IdentityNamespace(this);
    this.memory = new MemoryNamespace(this);
    this.provider = new ProviderNamespace(this);
    this.activity = new ActivityNamespace(this);
    this.wallet = new WalletNamespace(this);
    this.payment = new PaymentNamespace(this);
  }

  /** Resolved Unix-socket path the client dials. */
  get socketPath(): string {
    return this.socket;
  }

  /** Internal: send one RPC. Public so namespaces can dispatch through it. */
  _call(method: string, params: unknown): Promise<unknown> {
    const opts: RpcOptions = {};
    if (this.timeoutMs !== undefined) opts.timeoutMs = this.timeoutMs;
    return rpcCall(this.socket, method, params, opts);
  }
}
