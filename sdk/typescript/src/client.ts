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

export class Client {
  readonly identity: IdentityNamespace;
  readonly memory: MemoryNamespace;
  readonly provider: ProviderNamespace;
  readonly activity: ActivityNamespace;

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
