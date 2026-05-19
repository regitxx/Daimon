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

from . import _home, _rpc, _stream


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


class _ProviderNamespace:
    """Verbs under ``daimon.provider.*``.

    ``list``, ``invoke``, and ``stream`` are surfaced. ``stream`` returns
    a :class:`daimon._stream.StreamHandle` — iterable for delta strings
    with the terminal envelope on ``.final``.
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def list(self) -> list[dict]:
        """List configured providers. Returns ``[{name, models, configured}, ...]``.

        An empty registry returns ``[]`` (not an error). Mirrors the
        ``handleProviderList`` empty-slice behaviour.
        """
        result = self._c._call("daimon.provider.list", None)
        return result or []

    def invoke(
        self,
        *,
        provider: str,
        messages: list[dict],
        model: str = "",
        system: str | None = None,
        temperature: float | None = None,
        max_tokens: int | None = None,
        inject_context: dict | None = None,
    ) -> dict:
        """Invoke a provider synchronously and return the full envelope.

        Returns the daemon's wrapping envelope verbatim:
        ``{"response": {model, content, stop_reason, usage: {...}},
        "injected_memory_ids"?: [...]}``. The SDK does not unwrap to the
        bare response — the metadata that lives at the envelope level
        (memory IDs the daimon folded into the prompt) is part of the
        contract a library caller may want.

        The wire shape nests the request fields under ``request``; this
        method takes them as flat kwargs and assembles the nested
        envelope internally to match the Go CLI's user surface.

        ``inject_context`` is passed through verbatim when supplied —
        callers construct ``{"query": ..., "max_tokens"?: int,
        "kinds"?: [str]}`` themselves. Bare-bool "use the prompt as
        query" is a CLI ergonomic; library callers can build the dict
        explicitly.
        """
        request: dict[str, Any] = {"model": model, "messages": messages}
        if system is not None:
            request["system"] = system
        if temperature is not None:
            request["temperature"] = temperature
        if max_tokens is not None:
            request["max_tokens"] = max_tokens

        params: dict[str, Any] = {"provider": provider, "request": request}
        if inject_context is not None:
            params["inject_context"] = inject_context
        return self._c._call("daimon.provider.invoke", params)

    def stream(
        self,
        *,
        provider: str,
        messages: list[dict],
        model: str = "",
        system: str | None = None,
        temperature: float | None = None,
        max_tokens: int | None = None,
        inject_context: dict | None = None,
        timeout: float | None = None,
    ) -> _stream.StreamHandle:
        """Stream a provider response token-by-token.

        Returns a :class:`daimon._stream.StreamHandle`. Iterate it to
        consume delta strings; the terminal envelope (``{response,
        injected_memory_ids?}``) is available as ``.final`` once the
        iteration completes.

        The wire shape is identical to :meth:`invoke` — flat kwargs are
        assembled into the nested ``{provider, request: {...}}`` envelope
        the daemon expects. The transport differs: streaming keeps the
        socket open in both directions and yields a notification frame
        per delta until the terminal frame arrives.

        Provider support follows the daemon's adapter capabilities:
        Ollama streams natively; Claude/OpenAI/LM Studio fall back to a
        synchronous invoke on the daemon side (the SDK still returns a
        :class:`StreamHandle`, but it yields the full content as a
        single delta).
        """
        request: dict[str, Any] = {"model": model, "messages": messages}
        if system is not None:
            request["system"] = system
        if temperature is not None:
            request["temperature"] = temperature
        if max_tokens is not None:
            request["max_tokens"] = max_tokens
        params: dict[str, Any] = {"provider": provider, "request": request}
        if inject_context is not None:
            params["inject_context"] = inject_context
        effective_timeout = timeout if timeout is not None else self._c._timeout
        return _stream.open_stream(
            self._c._socket,
            "daimon.provider.stream",
            params,
            timeout=effective_timeout,
        )


class _ActivityNamespace:
    """Verbs under ``daimon.activity.*``.

    Mirrors the audit-trail surface closed in sessions 28-31: ``append``
    writes a row, ``query`` reads rows (and is itself logged as
    ``activity.queried``), ``verify`` walks the chain end-to-end and
    appends an ``activity.verified`` row on success.
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def append(self, *, kind: str, payload: dict | None = None) -> dict:
        """Append an entry to the activity log. Returns ``{id, hash}``."""
        params: dict[str, Any] = {"kind": kind}
        if payload is not None:
            params["payload"] = payload
        return self._c._call("daimon.activity.append", params)

    def query(
        self,
        *,
        since: int | None = None,
        until: int | None = None,
        kind: str | None = None,
        limit: int | None = None,
    ) -> list[dict]:
        """Query the activity log. Returns a list of entries.

        Filters mirror SPEC §6.1: ``since``/``until`` are unix-millisecond
        bounds, ``kind`` is a single-kind filter (multi-kind OR is a CLI
        client-side concern), ``limit`` caps the result count.
        ``null``-result is normalised to ``[]`` (mirrors memory.search).
        """
        params: dict[str, Any] = {}
        if since is not None:
            params["since"] = since
        if until is not None:
            params["until"] = until
        if kind is not None:
            params["kind"] = kind
        if limit is not None:
            params["limit"] = limit
        result = self._c._call("daimon.activity.query", params if params else None)
        return result or []

    def verify(self) -> dict:
        """Walk the chain end-to-end. Returns ``{verified: int, ok: bool}``.

        On chain failure (broken prev_hash, signature mismatch, AEAD
        authentication failure) the daemon returns a typed
        ``CodeInternalError`` and the SDK raises :class:`RPCError`. The
        Verify call appends an ``activity.verified`` row on success.
        """
        return self._c._call("daimon.activity.verify", {})


class _WalletNamespace:
    """Verbs under ``daimon.wallet.*`` (v0.2, phase 40.2).

    The wallet keystore is auto-created by the daemon on first
    ``daimon unlock`` — at that moment the unlock response carries a
    ``mnemonic`` field (24 BIP-39 words) that callers MUST surface to
    the principal exactly once for backup. The daemon does NOT keep a
    copy: lose both the mnemonic AND the on-disk keystore file and the
    wallets it derives become permanently inaccessible.

    All verbs require the daemon to be unlocked AND the wallet keystore
    to be loaded (the normal post-unlock state). If the wallet keystore
    fails to open for non-fatal reasons every verb returns an
    :class:`RPCError` with code ``-32600`` ("wallet keystore not
    loaded").
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def list(self) -> list[dict]:
        """List all wallets in the keystore.

        Returns a list of ``{id, chain, path, address, pubkey,
        created_at}``. Empty keystore returns ``[]`` (mirrors
        memory.list and activity.query's null-normalisation).
        """
        result = self._c._call("daimon.wallet.list", None)
        return result or []

    def create(self, *, chain: str) -> dict:
        """Derive a new HD wallet for ``chain`` and persist it.

        v0.2 supports EVM chains (``evm:base``, ``evm:base-sepolia``,
        and any other ``evm:*`` label — they all use the same
        secp256k1 derivation under m/44'/60'/0'/0/N). Raises
        :class:`RPCError` for unsupported chains or duplicate-chain
        requests (one wallet per chain in v0.2).

        Returns ``{id, chain, path, address, pubkey, created_at}``.
        """
        return self._c._call("daimon.wallet.create", {"chain": chain})

    def address(self, *, chain: str) -> str:
        """Return just the EIP-55-checksummed address for ``chain``.

        Convenience wrapper over the structured ``list`` / ``create``
        results. Raises :class:`RPCError` with code ``-32002``
        (CodeNotFound) if no wallet exists for the chain.
        """
        result = self._c._call("daimon.wallet.address", {"chain": chain})
        return result["address"]

    def sign(self, *, chain: str, digest_hex: str) -> str:
        """Low-level: sign a 32-byte digest with the chain's wallet key.

        Returns the 65-byte EVM ``[r || s || v]`` signature as a
        ``0x``-prefixed hex string. Most callers should use
        :meth:`_PaymentNamespace.pay` instead — it builds the
        EIP-3009 digest internally.

        ``digest_hex`` may be ``0x``-prefixed or bare hex; the daemon
        accepts both.
        """
        result = self._c._call(
            "daimon.wallet.sign",
            {"chain": chain, "digest_hex": digest_hex},
        )
        return result["signature_hex"]

    def derive(self, *, chain: str, index: int = 0) -> dict:
        """Compute the address that would be derived at (chain, index)
        WITHOUT persisting anything.

        Read-only counterpart to :meth:`create`. Useful for "did my
        ``daimon wallet recover`` import the right seed?" verification:
        derive at index 0 and compare against an externally-known
        address (e.g. what MetaMask shows for the same seed). Also
        useful for pre-computing what address index N would produce
        before actually creating a wallet at that index.

        Returns a dict with ``chain``, ``path``, ``address``, and
        ``pubkey`` keys — same shape ``create`` returns minus the
        persistence fields (``id``, ``created_at``).

        :param chain: Chain label (e.g. ``"evm:base"``). Required.
        :param index: BIP-44 HD index. Defaults to 0 (the typical
            "main address" position).
        :raises RPCError: code ``-32602`` if the chain is not in v0.2's
            supported registry.
        """
        result = self._c._call(
            "daimon.wallet.derive",
            {"chain": chain, "index": index},
        )
        return {
            "chain": result["chain"],
            "path": result["path"],
            "address": result["address"],
            "pubkey": result["pubkey"],
        }

    def show_mnemonic(self, *, password: str) -> list[str]:
        """Re-display the 24-word BIP-39 mnemonic stored in the keystore.

        Requires password re-confirmation: the supplied password is fed
        through the daemon's full Argon2id + AES-GCM-decrypt pipeline
        against the on-disk keystore, NOT compared against the in-memory
        unlocked state. A wrong password raises :class:`RPCError` with
        code ``-32008`` (CodeWrongPassword), distinct from the
        ``-32001`` (CodeIdentityLocked) returned for "daemon needs
        unlocking" — callers can branch on the code without string-
        matching.

        Industry standard for non-custodial seed reveal (MetaMask,
        Phantom, Trezor all require password re-confirmation). Typical
        use cases: principal wants to verify they wrote down the backup
        correctly after the unlock-time display; principal wants to
        import the daimon mnemonic into MetaMask / Phantom / Rabby to
        inspect or move funds outside the daimon.

        Performance: Argon2id KDF costs ~100ms by design. Don't invoke
        this in a tight loop.
        """
        result = self._c._call(
            "daimon.wallet.show_mnemonic",
            {"password": password},
        )
        return list(result["mnemonic"])


class _PaymentNamespace:
    """Verbs under ``daimon.payment.*`` (v0.2, phase 40.5).

    Wraps the daimon's internal x402 client. Each call dispatches an
    outbound HTTP request through the 402-retry handshake:
    PAYMENT-REQUIRED → pick a compatible PaymentRequirement → build +
    sign EIP-3009 authorisation → retry with PAYMENT-SIGNATURE → parse
    PAYMENT-RESPONSE. The wallet keystore + activity log are both used
    on the daemon side; the SDK just shuttles the request and result.
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def pay(
        self,
        *,
        url: str,
        method: str = "GET",
        headers: dict[str, str] | None = None,
        body: bytes | str | None = None,
        ceiling_smallest_unit: int | str | None = None,
        validity_seconds: int | None = None,
    ) -> dict:
        """Pay an x402-protected URL end-to-end.

        ``body`` may be ``bytes`` (sent verbatim) or ``str`` (sent as
        UTF-8 text — the daemon's wire surface accepts both shapes via
        separate ``body_base64`` / ``body_text`` fields and the SDK
        picks based on the Python type).

        ``ceiling_smallest_unit`` caps the value the daimon will sign
        in a single payment, in the asset's smallest unit (USDC has
        6 decimals, so 100000 ≡ $0.10). ``None`` disables the ceiling
        — STRONGLY DISCOURAGED in production code. The default
        ceiling on the CLI is 100000; SDK callers should pass an
        explicit value.

        Returns ``{status_code, response_headers, body, payment_response}``:

        - ``status_code`` — final HTTP status from the resource server
        - ``response_headers`` — small allowlist (Content-Type,
          Content-Length, Payment-Response)
        - ``body`` — bytes (the SDK base64-decodes the wire form)
        - ``payment_response`` — parsed PAYMENT-RESPONSE structure
          (``None`` if absent or malformed)

        Raises:
        - :class:`RPCError` with code ``-32006`` if the resource
          demanded more than ``ceiling_smallest_unit`` (audit-logged
          as ``payment.failed`` with reason "ceiling exceeded")
        - :class:`RPCError` with code ``-32007`` if no wallet matches
          the resource's PaymentRequirements
        - :class:`RPCError` with code ``-32603`` for transport or
          server errors
        """
        import base64 as _base64  # local import keeps top-level light

        params: dict[str, Any] = {"url": url, "method": method}
        if headers is not None:
            params["headers"] = headers
        if isinstance(body, bytes):
            params["body_base64"] = _base64.b64encode(body).decode("ascii")
        elif isinstance(body, str):
            params["body_text"] = body
        elif body is not None:
            raise TypeError(f"body must be bytes, str, or None (got {type(body).__name__})")
        if ceiling_smallest_unit is not None:
            params["ceiling_smallest_unit"] = str(ceiling_smallest_unit)
        if validity_seconds is not None:
            params["validity_seconds"] = validity_seconds

        result = self._c._call("daimon.payment.pay", params)
        # Decode the body once on the SDK side so callers don't have
        # to know about the wire's base64 envelope.
        decoded_body = b""
        if result.get("response_body_base64"):
            decoded_body = _base64.b64decode(result["response_body_base64"])
        return {
            "status_code": result.get("status_code", 0),
            "response_headers": result.get("response_headers") or {},
            "body": decoded_body,
            "payment_response": result.get("payment_response"),
        }


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
        self.provider = _ProviderNamespace(self)
        self.activity = _ActivityNamespace(self)
        self.wallet = _WalletNamespace(self)
        self.payment = _PaymentNamespace(self)

    @property
    def socket_path(self) -> Path:
        """Resolved Unix-socket path the client dials."""
        return self._socket

    def _call(self, method: str, params: Any | None) -> Any:
        return _rpc.rpc_call(self._socket, method, params, timeout=self._timeout)
