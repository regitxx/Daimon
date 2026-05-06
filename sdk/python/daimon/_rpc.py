"""Unix-socket JSON-RPC 2.0 client.

Mirrors cmd/daimon/rpc.go's behaviour:
  - one connection per RPC (no pipelining in v0.1)
  - request envelope: {jsonrpc:"2.0", method, params, id:1}
  - response envelope: {jsonrpc:"2.0", result|error, id}
  - error envelope: {code, message, data?}
  - errno -> exception rewrites: ENOENT/ECONNREFUSED -> DaemonNotRunning,
    code -32001 -> DaemonLocked, anything else -> RPCError.

Streaming (daimon.provider.stream notifications) is intentionally NOT
implemented in this SDK session — see SPEC §6.1 and the kickoff for the
deferred surface.
"""

from __future__ import annotations

import errno
import json
import socket
from pathlib import Path
from typing import Any

from .errors import DaemonNotRunning, RPCError, _from_error_object

# Default per-call timeout (seconds). Generous — most RPCs return in
# milliseconds, but provider.invoke (not in this SDK session) can be slow.
# Callers can override per-Client.
DEFAULT_TIMEOUT = 30.0


def rpc_call(
    socket_path: Path | str,
    method: str,
    params: Any | None,
    timeout: float | None = None,
) -> Any:
    """Send a single JSON-RPC request and return the decoded result.

    On socket connect failure (ENOENT, ECONNREFUSED) raises
    :class:`DaemonNotRunning`. On JSON-RPC error, raises
    :class:`DaemonLocked` (code -32001) or :class:`RPCError` (any other
    code).
    """
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(timeout if timeout is not None else DEFAULT_TIMEOUT)
    try:
        try:
            sock.connect(str(socket_path))
        except FileNotFoundError as e:
            raise DaemonNotRunning(
                f"daemon socket not present at {socket_path} — run `daimon unlock` first"
            ) from e
        except OSError as e:
            # ECONNREFUSED is the typical "stale socket file, no listener" mode.
            if e.errno in (errno.ECONNREFUSED, errno.ENOENT):
                raise DaemonNotRunning(
                    f"daemon not accepting connections at {socket_path} — "
                    f"run `daimon unlock` first"
                ) from e
            raise

        request = {
            "jsonrpc": "2.0",
            "method": method,
            # Match Go's json.Marshal: omit `params` only when None. Empty
            # objects/arrays are sent verbatim because the server treats
            # them differently from absent.
            **({"params": params} if params is not None else {}),
            "id": 1,
        }
        # Go encodes one object per connection followed by a newline. Our
        # decoder reads one object then closes; matching the Go side's
        # framing keeps the wire identical.
        payload = (json.dumps(request, separators=(",", ":")) + "\n").encode("utf-8")
        sock.sendall(payload)
        # Half-close the write side so the server's json.Decoder sees EOF
        # promptly after the single request — mirrors Go's per-call
        # connection lifecycle without us guessing the response size.
        try:
            sock.shutdown(socket.SHUT_WR)
        except OSError:
            pass

        body = _read_all(sock)
    finally:
        sock.close()

    if not body:
        raise RPCError(0, "empty response from daemon")
    try:
        resp = json.loads(body)
    except json.JSONDecodeError as e:
        raise RPCError(0, f"malformed response: {e}") from e

    if not isinstance(resp, dict):
        raise RPCError(0, f"unexpected response shape: {type(resp).__name__}")
    if "error" in resp and resp["error"] is not None:
        raise _from_error_object(resp["error"])
    # SPEC §6.1: result MAY be omitted when the RPC has no return payload.
    return resp.get("result")


def _read_all(sock: socket.socket, chunk: int = 4096) -> bytes:
    """Drain the socket until the peer closes the write side."""
    out = bytearray()
    while True:
        try:
            buf = sock.recv(chunk)
        except OSError:
            break
        if not buf:
            break
        out.extend(buf)
    return bytes(out)
