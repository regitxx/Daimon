"""Exception hierarchy for the daimon Python SDK.

The taxonomy mirrors the two failure modes `humaniseDaemonErr` rewrites in
the Go CLI (see cmd/daimon/client.go) — DaemonNotRunning for socket
absence/refusal, DaemonLocked for the -32001 typed RPC error — plus a
generic RPCError for everything else the daemon may return. All inherit
from DaimonError so callers can catch the family in one block.
"""

from __future__ import annotations

from typing import Any, Optional


class DaimonError(Exception):
    """Base class for all SDK-raised errors."""


class DaemonNotRunning(DaimonError):
    """The daemon socket could not be opened.

    Either the socket path does not exist (daemon never started) or the
    connection was refused (daemon crashed leaving a stale socket file).
    Running ``daimon unlock`` from a shell will (re)start it.
    """


class DaemonLocked(DaimonError):
    """The daemon is running but the principal's keys are not loaded.

    Surfaces the typed JSON-RPC error code -32001
    (CodeIdentityLocked). Run ``daimon unlock`` to load keys.
    """


# JSON-RPC 2.0 reserved error codes plus daimon-specific extensions.
# Kept in lockstep with internal/server/jsonrpc.go and cmd/daimon/rpc.go.
CODE_INVALID_REQUEST = -32600
CODE_METHOD_NOT_FOUND = -32601
CODE_INVALID_PARAMS = -32602
CODE_INTERNAL_ERROR = -32603
CODE_IDENTITY_LOCKED = -32001
CODE_NOT_FOUND = -32002


class RPCError(DaimonError):
    """A JSON-RPC error returned by the daemon.

    Attributes mirror the wire shape: ``code`` (int), ``message`` (str),
    ``data`` (any JSON-decoded value or ``None``). The string form
    matches the Go side's ``rpc error <code>: <message> (<data>)`` format
    for grep-ability across language boundaries.
    """

    def __init__(self, code: int, message: str, data: Any = None):
        self.code = code
        self.message = message
        self.data = data
        super().__init__(self._render())

    def _render(self) -> str:
        if self.data is not None:
            return f"rpc error {self.code}: {self.message} ({self.data!r})"
        return f"rpc error {self.code}: {self.message}"


def _from_error_object(obj: dict) -> RPCError | DaemonLocked:
    """Build the right exception subtype from a JSON-RPC error object."""
    code = int(obj.get("code", 0))
    message = str(obj.get("message", ""))
    data = obj.get("data")
    if code == CODE_IDENTITY_LOCKED:
        # Preserve the original RPC fields on the DaemonLocked instance so
        # callers that catch the family still see the underlying code.
        err = DaemonLocked(message or "daemon is locked")
        err.code = code  # type: ignore[attr-defined]
        err.message = message  # type: ignore[attr-defined]
        err.data = data  # type: ignore[attr-defined]
        return err
    return RPCError(code, message, data)
