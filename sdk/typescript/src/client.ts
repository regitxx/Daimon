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

export interface WalletDeriveParams {
  /** Chain label (e.g. `"evm:base"`). */
  chain: string;
  /**
   * BIP-44 HD index. Defaults to `0` (the typical "main address" position).
   */
  index?: number;
}

export interface WalletDeriveResult {
  chain: string;
  path: string;
  address: string;
  pubkey: string;
}

export interface WalletShowMnemonicParams {
  /**
   * The keystore password. Re-verified via the daemon's full Argon2id +
   * AES-GCM-decrypt pipeline against the on-disk keystore — wrong
   * password throws `RPCError` with code `-32008`. Industry-standard
   * "prove you know the password right now" attestation for seed reveal
   * (MetaMask, Phantom, Trezor all require this).
   */
  password: string;
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

// --- v0.3 surface: federation + peer -----------------------------------------

/**
 * Returned by `daimon.federation.config`.
 * Lets a client introspect this daimon's federation identity and capabilities.
 */
export interface FederationConfig {
  /** This daimon's own DID (always present). */
  did: string;
  /**
   * Base58btc-encoded Ed25519 public key (the z6Mk… fragment of the DID).
   * Used as the Noise IK static key for transport auth.
   */
  transport_pubkey_multibase: string;
  /** DID methods this daimon can resolve outbound. `["did:key"]` in v0.3. */
  did_methods: string[];
  /**
   * peer.* verbs this daimon serves over inbound Noise channels.
   * `["peer.echo", "peer.ask", "peer.pay.required"]` when fully configured.
   */
  protocols: string[];
  /**
   * `"tcp://host:port"` when PeerListen is active; absent when not listening.
   * Use as the `endpoint` argument to `client.peer.dial`.
   */
  public_endpoint?: string;
  /** Protocol vocabulary version. `"v0.3-draft"` in the current release. */
  federation_version: string;
}

/** Wire shape for one open outbound peer channel. */
export interface PeerChannel {
  channel_id: string;
  peer_did: string;
  opened_at: string;
}

/** Returned by `client.peer.dial`. */
export interface PeerDialResult extends PeerChannel {}

/** One entry in the local address book. */
export interface AddressBookEntry {
  did: string;
  label: string;
  /** `"FirstSeen"` | `"Pinned"` | `"Blocked"` */
  status: string;
  approved_verbs: string[];
  transport_pubkey_multibase: string;
  first_seen: string;
  last_seen: string;
}

/** x402 v2 PaymentRequirements shape returned by `peer.pay.required`. */
export interface PaymentRequirement {
  scheme: string;
  network: string;
  /** Smallest-unit integer, decimal string. E.g. `"1000000"` for 1.00 USDC. */
  maxAmountRequired: string;
  /** Protected resource identifier. E.g. `"peer.ask"`. */
  resource: string;
  description: string;
  /** EIP-55-checksummed recipient address. */
  payTo: string;
  maxTimeoutSeconds: number;
  /** Token contract address on the specified network. */
  asset: string;
  mimeType?: string;
  outputSchema?: unknown;
  extra?: unknown;
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

  /**
   * Re-display the 24-word BIP-39 mnemonic stored in the keystore.
   *
   * Requires password re-confirmation: the supplied password is run
   * through the daemon's full Argon2id + AES-GCM-decrypt pipeline
   * against the on-disk keystore — NOT compared against the daemon's
   * in-memory unlocked state. A wrong password throws `RPCError` with
   * code `-32008` (CodeWrongPassword), distinct from `-32001`
   * (CodeIdentityLocked) so callers can branch on the code without
   * string-matching.
   *
   * Industry standard for non-custodial seed reveal (MetaMask,
   * Phantom, Trezor all require password re-confirmation). Typical
   * use cases: verify the backup was written down correctly after
   * the unlock-time display; export the mnemonic for import into
   * another wallet to inspect or move funds outside the daimon.
   *
   * Performance: Argon2id KDF costs ~100ms by design.
   */
  /**
   * Compute the address that would be derived at (chain, index)
   * WITHOUT persisting anything. Read-only counterpart to `create`.
   * Useful for "did my `daimon wallet recover` import the right
   * seed?" — derive at index 0 and compare against an externally-
   * known address (e.g. what MetaMask shows for the same seed).
   *
   * Returns the address along with the BIP-44 derivation path and
   * compressed pubkey hex — same shape as `create` minus the
   * persistence fields (`id`, `createdAt`).
   *
   * Throws `RPCError` with code `-32602` if the chain is not in
   * v0.2's supported registry.
   */
  async derive(params: WalletDeriveParams): Promise<WalletDeriveResult> {
    const result = (await this.c._call("daimon.wallet.derive", {
      chain: params.chain,
      index: params.index ?? 0,
    })) as WalletDeriveResult;
    return {
      chain: result.chain,
      path: result.path,
      address: result.address,
      pubkey: result.pubkey,
    };
  }

  async showMnemonic(params: WalletShowMnemonicParams): Promise<string[]> {
    const result = (await this.c._call("daimon.wallet.show_mnemonic", {
      password: params.password,
    })) as { mnemonic: string[] };
    return result.mnemonic;
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

class FederationNamespace {
  constructor(private readonly c: Client) {}

  /**
   * Return federation configuration for this daimon.
   *
   * Returns a `FederationConfig` with the daimon's DID, transport pubkey,
   * supported DID methods, served `peer.*` protocols, optional public
   * endpoint URL, and federation protocol version.
   *
   * Pure-read — no audit-log row, no state mutation. Requires identity
   * to be unlocked (throws `DaemonLocked` if not).
   */
  async config(): Promise<FederationConfig> {
    return (await this.c._call(
      "daimon.federation.config",
      undefined,
    )) as FederationConfig;
  }
}

class AddressBookNamespace {
  constructor(private readonly c: Client) {}

  /**
   * List all entries in the address book.
   * Returns `[]` when empty or when the daemon returns null.
   */
  async list(): Promise<AddressBookEntry[]> {
    const result = (await this.c._call(
      "daimon.peer.address_book.list",
      undefined,
    )) as { entries?: AddressBookEntry[] } | null;
    return result?.entries ?? [];
  }

  /**
   * Manually add a peer as `FirstSeen`. Use `pin` afterwards to grant
   * verb-level authorization.
   *
   * Throws `RPCError` (`-32603`) if the DID is already in the address book.
   */
  async add(params: {
    did: string;
    label?: string;
    pubkeyMultibase?: string;
  }): Promise<JsonObject> {
    const wire: JsonObject = { did: params.did };
    if (params.label !== undefined) wire["label"] = params.label;
    if (params.pubkeyMultibase !== undefined)
      wire["pubkey_multibase"] = params.pubkeyMultibase;
    return (await this.c._call(
      "daimon.peer.address_book.add",
      wire,
    )) as JsonObject;
  }

  /**
   * Elevate a peer to `Pinned` and set the allowed `peer.*` verbs.
   * Throws `RPCError` (`-32002` CodeNotFound) if the DID is not in the book.
   */
  async pin(params: { did: string; verbs: string[] }): Promise<JsonObject> {
    return (await this.c._call("daimon.peer.address_book.pin", {
      did: params.did,
      verbs: params.verbs,
    })) as JsonObject;
  }

  /**
   * Block a peer. All inbound `peer.*` calls from this DID will fail.
   * Idempotent.
   */
  async block(did: string): Promise<JsonObject> {
    return (await this.c._call("daimon.peer.address_book.block", {
      did,
    })) as JsonObject;
  }

  /**
   * Unblock a peer — transitions `Blocked` → `Pinned` (retaining prior
   * `ApprovedVerbs`). Throws `RPCError` if the DID is not in the book.
   */
  async unblock(did: string): Promise<JsonObject> {
    return (await this.c._call("daimon.peer.address_book.unblock", {
      did,
    })) as JsonObject;
  }

  /**
   * Permanently remove a peer from the address book.
   * Throws `RPCError` (`-32002` CodeNotFound) if the DID is not present.
   */
  async remove(did: string): Promise<JsonObject> {
    return (await this.c._call("daimon.peer.address_book.remove", {
      did,
    })) as JsonObject;
  }
}

class PeerNamespace {
  /** Address book sub-namespace (`client.peer.addressBook.*`). */
  readonly addressBook: AddressBookNamespace;

  constructor(private readonly c: Client) {
    this.addressBook = new AddressBookNamespace(c);
  }

  /**
   * Open an authenticated Noise IK channel to a remote daimon.
   *
   * `did` must be a `did:key:z6Mk…` DID. `endpoint` is the peer's TCP
   * address — either `"tcp://host:port"` or bare `"host:port"`.
   *
   * The returned `channel_id` is used by `invoke`, `close`, and `echo`.
   * Also auto-populates the local address book as `FirstSeen`.
   *
   * Throws `RPCError` with `CodePeerUnreachable (-32010)` on network
   * failure or Noise handshake rejection.
   */
  async dial(params: { did: string; endpoint: string }): Promise<PeerDialResult> {
    return (await this.c._call("daimon.peer.dial", {
      did: params.did,
      endpoint: params.endpoint,
    })) as PeerDialResult;
  }

  /**
   * Close an open peer channel.
   * No-ops and does not throw if the channel is already gone.
   */
  async close(channelId: string): Promise<void> {
    await this.c._call("daimon.peer.close", { channel_id: channelId });
  }

  /**
   * List open outbound peer channels.
   * Returns `[]` when no channels are open.
   */
  async list(): Promise<PeerChannel[]> {
    const result = (await this.c._call(
      "daimon.peer.list",
      undefined,
    )) as { channels?: PeerChannel[] } | null;
    return result?.channels ?? [];
  }

  /**
   * Raw peer verb dispatch — send `method` with `params` over `channelId`
   * and return the peer's `result` field verbatim.
   *
   * Use `echo` and `payRequired` for the common verbs; use this method for
   * custom verbs or future protocol additions.
   *
   * Throws `RPCError` with `CodePeerUnreachable (-32010)` if the channel
   * was broken (remote closed, network timeout). Broken channels are
   * automatically removed from the open-channel map.
   */
  async invoke(
    channelId: string,
    method: string,
    params?: unknown,
  ): Promise<unknown> {
    const wire: JsonObject = { channel_id: channelId, method };
    if (params !== undefined) wire["params"] = params as JsonObject;
    const result = (await this.c._call("daimon.peer.invoke", wire)) as {
      result?: unknown;
    } | null;
    return result?.result ?? result;
  }

  /**
   * Invoke `peer.echo` on the remote daimon.
   *
   * Returns `{message, from_did}` where `from_did` is the remote daimon's
   * DID. Universally available — the canonical "is this channel alive?"
   * health-check verb.
   */
  async echo(channelId: string, message: string): Promise<JsonObject> {
    const result = await this.invoke(channelId, "peer.echo", { message });
    return (result as JsonObject) ?? {};
  }

  /**
   * Query the remote daimon's payment requirements for `service`.
   *
   * Returns a list of `PaymentRequirement` objects (x402 v2 shape).
   * Callers pick the (scheme, network, asset) tuple they can satisfy and
   * construct the EIP-3009 authorization.
   *
   * In v0.3 only `service = "peer.ask"` is supported. An empty list means
   * the remote daimon has no wallet configured.
   *
   * Universally available — no address-book authorization required.
   */
  async payRequired(
    channelId: string,
    service: string,
  ): Promise<PaymentRequirement[]> {
    const result = (await this.invoke(channelId, "peer.pay.required", {
      service,
    })) as { requirements?: PaymentRequirement[] } | null;
    return result?.requirements ?? [];
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
  /** v0.3: federation introspection (`daimon.federation.config`). */
  readonly federation: FederationNamespace;
  /** v0.3: peer channel management + address book (`daimon.peer.*`). */
  readonly peer: PeerNamespace;

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
    this.federation = new FederationNamespace(this);
    this.peer = new PeerNamespace(this);
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
