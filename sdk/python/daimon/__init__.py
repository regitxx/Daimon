"""Daimon Python SDK — thin client over the local daimon daemon.

Public surface:

    from daimon import Client, RPCError, DaemonNotRunning, DaemonLocked

The package mirrors the Go `cmd/daimon` CLI's wire-level behaviour: one
TCP/Unix connection per RPC, JSON-RPC 2.0 envelope, no pipelining. See
SPEC §6.1 for the canonical method list.
"""

from .client import Client
from .errors import (
    DaimonError,
    DaemonNotRunning,
    DaemonLocked,
    RPCError,
)
from ._stream import StreamHandle
from ._version import __version__  # auto-generated; see scripts/gen_version.py

__all__ = [
    "Client",
    "DaimonError",
    "DaemonNotRunning",
    "DaemonLocked",
    "RPCError",
    "StreamHandle",
    "__version__",
]
