"""High-level Daimon client.

The :class:`Client` wraps the JSON-RPC primitive in :mod:`._rpc` with
namespaced verb groups (``client.identity``, ``client.memory``) that
mirror the Go CLI's dispatch shape. Verb groups are thin: each method is
one line over :meth:`Client._call`.

Adding a new verb in this SDK is intentionally lightweight — paste the
SPEC §6.1 method name plus the param map. Type modelling is deferred:
returns are raw decoded JSON (dicts/lists/scalars), not pydantic models,
so the SDK doesn't drift behind the Go side's evolving record shapes.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from . import _home, _rpc


class _MemoryNamespace:
    """Verbs under ``daimon.memory.*``.

    The Go server registers six methods under this prefix
    (write/read/search/delete/export/import). This session ships the four
    the kickoff calls out (write/read/search/list) — list is built on top
    of search-with-empty-query, matching ``cmd/daimon/cmd_memory.go``'s
    ``cmdMemoryList``.
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def write(
        self,
        *,
        kind: str,
        content: str,
        metadata: dict | None = None,
        source: str | None = None,
    ) -> dict:
        """Write a memory record. Returns ``{"id": "..."}``."""
        params: dict[str, Any] = {"kind": kind, "content": content}
        if metadata is not None:
            params["metadata"] = metadata
        if source is not None:
            params["source"] = source
        return self._c._call("daimon.memory.write", params)

    def read(self, memory_id: str) -> dict:
        """Read a memory by id. Returns the full memory record."""
        return self._c._call("daimon.memory.read", {"id": memory_id})

    def search(
        self,
        query: str,
        *,
        limit: int | None = None,
        kind: str | None = None,
    ) -> list[dict]:
        """Search memories. Returns a list of ``{...memory, "score": float}``.

        An empty result list is returned as ``[]`` — distinct from a server
        error (which raises :class:`RPCError`). Mirrors the Go CLI's
        empty-vs-error split (cmd_memory.go::cmdMemorySearch).
        """
        params: dict[str, Any] = {"query": query}
        if limit is not None:
            params["limit"] = limit
        if kind is not None:
            params["kind"] = kind
        result = self._c._call("daimon.memory.search", params)
        return result or []

    def list(self, *, limit: int | None = None, kind: str | None = None) -> list[dict]:
        """List all memories — daimon.memory.search with an empty query.

        Mirrors cmd/daimon/cmd_memory.go::cmdMemoryList.
        """
        return self.search("", limit=limit, kind=kind)


class _IdentityNamespace:
    """Verbs under ``daimon.identity.*``.

    Only ``get`` is exposed in this SDK session. ``daimon.identity.unlock``
    is reachable via :meth:`Client._call` for advanced callers but is
    deliberately not surfaced — unlocking from a library would mean
    holding the password in process memory, which is the wrong default
    posture. The CLI's ``daimon unlock`` is the canonical path.
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def get(self) -> dict:
        """Return the principal's DID. ``{"did": "did:key:..."}``."""
        return self._c._call("daimon.identity.get", None)


class Client:
    """Synchronous Daimon client over the local Unix socket.

    Parameters
    ----------
    home:
        Override for ``$DAIMON_HOME``. When ``None`` (default), the SDK
        resolves the home dir via :func:`daimon._home.resolve_home`,
        which mirrors the Go CLI exactly.
    socket_path:
        Direct override for the socket path. Useful for tests against a
        stub daemon. Takes precedence over ``home``.
    timeout:
        Per-call socket timeout, in seconds. Defaults to
        :data:`daimon._rpc.DEFAULT_TIMEOUT`.
    """

    def __init__(
        self,
        home: str | Path | None = None,
        socket_path: str | Path | None = None,
        timeout: float | None = None,
    ) -> None:
        if socket_path is not None:
            self._socket = Path(socket_path)
        else:
            resolved_home = Path(home).resolve() if home is not None else _home.resolve_home()
            self._socket, _fallback = _home.socket_path(resolved_home)
        self._timeout = timeout
        self.identity = _IdentityNamespace(self)
        self.memory = _MemoryNamespace(self)

    @property
    def socket_path(self) -> Path:
        """Resolved Unix-socket path the client dials."""
        return self._socket

    def _call(self, method: str, params: Any | None) -> Any:
        return _rpc.rpc_call(self._socket, method, params, timeout=self._timeout)
