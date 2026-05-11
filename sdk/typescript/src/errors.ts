/**
 * Exception hierarchy for the daimon TypeScript SDK.
 *
 * Mirrors sdk/python/daimon/errors.py: DaemonNotRunning for socket
 * absence/refusal, DaemonLocked for the typed -32001 JSON-RPC error,
 * RPCError for everything else. All extend DaimonError so callers can
 * catch the family in one block.
 */

export class DaimonError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

export class DaemonNotRunning extends DaimonError {}

export const CODE_INVALID_REQUEST = -32600;
export const CODE_METHOD_NOT_FOUND = -32601;
export const CODE_INVALID_PARAMS = -32602;
export const CODE_INTERNAL_ERROR = -32603;
export const CODE_IDENTITY_LOCKED = -32001;
export const CODE_NOT_FOUND = -32002;

export class RPCError extends DaimonError {
  readonly code: number;
  readonly rpcMessage: string;
  readonly data: unknown;

  constructor(code: number, message: string, data: unknown = undefined) {
    super(renderRpc(code, message, data));
    this.code = code;
    this.rpcMessage = message;
    this.data = data;
  }
}

export class DaemonLocked extends DaimonError {
  readonly code: number = CODE_IDENTITY_LOCKED;
  readonly rpcMessage: string;
  readonly data: unknown;

  constructor(message: string, data: unknown = undefined) {
    super(message || "daemon is locked");
    this.rpcMessage = message;
    this.data = data;
  }
}

function renderRpc(code: number, message: string, data: unknown): string {
  if (data !== undefined && data !== null) {
    return `rpc error ${code}: ${message} (${JSON.stringify(data)})`;
  }
  return `rpc error ${code}: ${message}`;
}

export interface RpcErrorObject {
  code?: number;
  message?: string;
  data?: unknown;
}

export function fromErrorObject(obj: RpcErrorObject): RPCError | DaemonLocked {
  const code = Number(obj.code ?? 0);
  const message = String(obj.message ?? "");
  const data = obj.data;
  if (code === CODE_IDENTITY_LOCKED) {
    return new DaemonLocked(message, data);
  }
  return new RPCError(code, message, data);
}
