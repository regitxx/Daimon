"""Streaming support for ``daimon.provider.stream``.

Wire shape (mirrors ``cmd/daimon/rpc.go::rpcStream`` and the server's
``handleProviderStream``):

  1. Client sends one JSON-RPC request envelope.
  2. Server sends 0..N ``daimon.provider.stream.delta`` notifications
     (no ``id`` field per JSON-RPC 2.0 §4.1, params ``{content: "..."}``).
  3. Server sends ONE terminal response frame carrying the request ``id``
     and either ``result`` (the full ``{response, injected_memory_ids?}``
     envelope) or ``error``.

Each frame is JSON-encoded and newline-terminated — Go's
``json.Encoder.Encode`` always appends ``\\n``. Splitting the read buffer
on raw ``\\n`` bytes is safe because JSON strings escape literal newlines
as ``\\\\n`` (two ASCII chars), so the only ``\\n`` byte on the wire is
the frame separator.

The connection lifecycle is different from the one-request-per-call
path in :mod:`._rpc`: we do NOT half-close the write side after sending
the request because the server keeps writing until the stream
completes. We close fully after the terminal frame (or on early exit
via the context-manager protocol).
"""

from __future__ import annotations

import errno
import json
import socket
from pathlib import Path
from typing import Any, Iterator

from .errors import DaemonNotRunning, RPCError, _from_error_object

DEFAULT_TIMEOUT = 300.0


class StreamHandle:
    """Iterable of delta strings, with the terminal envelope on ``.final``.

    Usage::

        stream = client.provider.stream(
            provider="ollama",
            model="llama3.2:latest",
            messages=[{"role": "user", "content": "say hi"}],
        )
        for delta in stream:
            print(delta, end="", flush=True)
        print()
        # After iteration: stream.final is the wrapping envelope
        # {response: {...}, injected_memory_ids?: [...]}
        usage = stream.final["response"]["usage"]

    Or as a context manager — closes the socket on exit even if the
    caller breaks out of iteration early::

        with client.provider.stream(...) as stream:
            for delta in stream:
                if some_cancel_condition():
                    break
    """

    final: dict | None = None

    def __init__(self, sock: socket.socket) -> None:
        self._sock: socket.socket | None = sock
        self._buf = bytearray()
        self._done = False

    def __iter__(self) -> "StreamHandle":
        return self

    def __next__(self) -> str:
        if self._done:
            raise StopIteration
        while True:
            frame = self._read_one_frame()
            if frame is None:
                # Peer closed without sending a terminal response.
                self._finish()
                raise RPCError(0, "stream ended without terminal response")

            # Notification frame: no `id`, has a `method`. JSON-RPC 2.0 §4.1.
            if "id" not in frame and frame.get("method"):
                method = frame.get("method", "")
                if method == "daimon.provider.stream.delta":
                    params = frame.get("params") or {}
                    content = params.get("content", "") if isinstance(params, dict) else ""
                    if content:
                        return content
                # Unknown notification kinds (future tool-call deltas, role
                # markers, etc.) — ignore, forward-compat with the daemon.
                continue

            # Terminal frame.
            if frame.get("error"):
                self._finish()
                raise _from_error_object(frame["error"])
            self.final = frame.get("result")
            self._finish()
            raise StopIteration

    def __enter__(self) -> "StreamHandle":
        return self

    def __exit__(self, *_a: Any) -> None:
        self.close()

    def close(self) -> None:
        """Tear down the underlying socket. Idempotent."""
        self._done = True
        if self._sock is not None:
            try:
                self._sock.close()
            except OSError:
                pass
            self._sock = None

    # --- internal -----------------------------------------------------------

    def _finish(self) -> None:
        self._done = True
        if self._sock is not None:
            try:
                self._sock.close()
            except OSError:
                pass
            self._sock = None

    def _read_one_frame(self) -> dict | None:
        """Return the next decoded JSON object on the wire, or None at EOF."""
        if self._sock is None:
            return None
        while True:
            i = self._buf.find(b"\n")
            if i >= 0:
                line = bytes(self._buf[:i])
                del self._buf[: i + 1]
                if line.strip():
                    return json.loads(line)
                continue
            try:
                chunk = self._sock.recv(4096)
            except OSError:
                # Treat read errors as EOF — match the non-streaming
                # client's tolerant drain in :mod:`._rpc`.
                chunk = b""
            if not chunk:
                # Final trailing line without a terminator.
                if self._buf.strip():
                    line = bytes(self._buf)
                    self._buf.clear()
                    return json.loads(line)
                return None
            self._buf.extend(chunk)


def open_stream(
    socket_path: Path | str,
    method: str,
    params: Any,
    timeout: float | None = None,
) -> StreamHandle:
    """Open a streaming RPC and return a :class:`StreamHandle`.

    The handle owns the socket — the caller iterates it (or uses it as a
    context manager) and the socket is closed on terminal frame, on error,
    or on context-manager exit.
    """
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(timeout if timeout is not None else DEFAULT_TIMEOUT)
    try:
        sock.connect(str(socket_path))
    except FileNotFoundError as e:
        sock.close()
        raise DaemonNotRunning(
            f"daemon socket not present at {socket_path} — run `daimon unlock` first"
        ) from e
    except OSError as e:
        sock.close()
        if e.errno in (errno.ECONNREFUSED, errno.ENOENT):
            raise DaemonNotRunning(
                f"daemon not accepting connections at {socket_path} — "
                f"run `daimon unlock` first"
            ) from e
        raise

    request = {
        "jsonrpc": "2.0",
        "method": method,
        **({"params": params} if params is not None else {}),
        "id": 1,
    }
    payload = (json.dumps(request, separators=(",", ":")) + "\n").encode("utf-8")
    try:
        sock.sendall(payload)
    except OSError:
        sock.close()
        raise

    # Critical: do NOT half-close the write side. The server reads our
    # single request, then writes notifications and the terminal frame on
    # the same connection — and on Linux, FIN on the read side can be
    # surfaced as a write-side error on some kernels. The non-streaming
    # half-close trick is correct for one-shot but wrong for streams.
    return StreamHandle(sock)
