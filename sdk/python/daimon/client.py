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


class _FederationNamespace:
    """Verbs under ``daimon.federation.*`` (v0.3, phase 30+).

    Introspection surface for this daimon's federation identity:
    its DID, transport pubkey, supported DID methods, served protocols,
    and (when PeerListen is active) its public TCP endpoint.

    Clients use ``config()`` to:

    - Discover the daimon's DID before dialling it from another daimon
    - Check which ``peer.*`` verbs it serves before invoking them
    - Obtain the public endpoint URL for ``client.peer.dial``
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def config(self) -> dict:
        """Return federation configuration for this daimon.

        Returns a dict with keys:

        - ``did`` — the daimon's own did:key DID (always present)
        - ``transport_pubkey_multibase`` — base58btc-encoded Ed25519 pubkey
          fragment (the z6Mk… portion of the DID, usable as Noise IK static)
        - ``did_methods`` — list of DID methods this daimon resolves
          (``["did:key"]`` in v0.3)
        - ``protocols`` — list of ``peer.*`` verbs this daimon serves
          (``["peer.echo", "peer.ask", "peer.pay.required"]`` when fully
          configured)
        - ``public_endpoint`` — ``"tcp://host:port"`` when PeerListen is
          active; absent otherwise
        - ``federation_version`` — ``"v0.3-draft"``

        Requires the identity to be unlocked (returns CodeIdentityLocked
        if not). Pure-read — no audit-log row, no state mutation.
        """
        return self._c._call("daimon.federation.config", None)


class _PeerAddressBookNamespace:
    """Verbs under ``daimon.peer.address_book.*`` (v0.3, phase 32).

    The address book records the trust state of every daimon this node has
    interacted with. Entries progress through three states:

    - **FirstSeen** — dialled but not yet explicitly trusted. ``peer.*``
      verbs beyond ``peer.echo`` are blocked.
    - **Pinned** — explicitly trusted. ``peer.ask`` (and future verbs) are
      allowed per ``ApprovedVerbs``.
    - **Blocked** — explicitly refused. All ``peer.*`` calls fail.

    ``Touch(did, pubkey)`` is called automatically on every successful
    ``daimon.peer.dial`` — it creates a ``FirstSeen`` entry on first contact
    and enforces TOFU (the same pubkey must appear on every subsequent dial
    from that DID).
    """

    def __init__(self, client: "Client") -> None:
        self._c = client

    def list(self) -> list[dict]:
        """Return all entries in the address book.

        Each entry is a dict with at minimum:
        ``{did, label, status, approved_verbs, transport_pubkey_multibase,
        first_seen, last_seen}``.
        Empty book returns ``[]``.
        """
        result = self._c._call("daimon.peer.address_book.list", None)
        if result is None:
            return []
        return result.get("entries", []) or []

    def add(self, *, did: str, label: str = "", pubkey_multibase: str = "") -> dict:
        """Manually add a peer to the address book as FirstSeen.

        Idempotent: if the DID is already present, returns an error
        (:class:`RPCError` with code ``-32603``, message
        "address book: entry already exists"). Use ``pin`` after ``add``
        to grant verb-level authorization.

        Parameters
        ----------
        did:
            The peer's DID (``did:key:z6Mk...``).
        label:
            Optional human-readable name (sent as ``pet_name`` on the
            wire, stored that way in the daemon; returned as ``pet_name``
            in list results).
        pubkey_multibase:
            The multibase fragment of the peer's did:key DID
            (the ``z6Mk…`` portion). Sent as
            ``transport_pubkey_multibase`` on the wire. Required for Noise
            IK authentication; can be left empty and populated by
            ``daimon.peer.dial``.
        """
        params: dict[str, Any] = {"did": did}
        if label:
            params["pet_name"] = label
        if pubkey_multibase:
            params["transport_pubkey_multibase"] = pubkey_multibase
        return self._c._call("daimon.peer.address_book.add", params)

    def pin(self, *, did: str, verbs: list[str]) -> dict:
        """Elevate a peer from FirstSeen → Pinned and set approved verbs.

        ``verbs`` is the complete list of ``peer.*`` verbs this peer may
        invoke on this daimon (e.g. ``["peer.ask"]``). An empty list grants
        Pinned status with no additional verb rights (peer can still call
        the universally-available ``peer.echo`` and ``peer.pay.required``).

        Raises :class:`RPCError` (CodeNotFound) if the DID is not in the
        address book; call ``add`` first.
        """
        return self._c._call(
            "daimon.peer.address_book.pin",
            {"did": did, "verbs": verbs},
        )

    def block(self, *, did: str) -> dict:
        """Block a peer. All inbound peer.* calls from this DID will fail.

        Idempotent — blocking an already-blocked entry is not an error.
        Clears ApprovedVerbs.
        """
        return self._c._call("daimon.peer.address_book.block", {"did": did})

    def unblock(self, *, did: str) -> dict:
        """Unblock a peer — transitions Blocked → Pinned.

        The entry retains its previous ApprovedVerbs; use ``pin`` afterwards
        to update them if needed.
        """
        return self._c._call("daimon.peer.address_book.unblock", {"did": did})

    def remove(self, *, did: str) -> dict:
        """Permanently remove a peer from the address book.

        Raises :class:`RPCError` (CodeNotFound) if the DID is not present.
        Removing a Pinned peer does NOT revoke in-flight connections — it
        only prevents future authorization lookups from finding the entry.
        """
        return self._c._call("daimon.peer.address_book.remove", {"did": did})


class _PeerNamespace:
    """Verbs under ``daimon.peer.*`` (v0.3, phases 33–35).

    The peer surface has two layers:

    **Outbound channel management** (calls *from* this daimon):

    - :meth:`dial` — open a Noise IK channel to a remote daimon
    - :meth:`close` — close an open channel
    - :meth:`list` — enumerate open channels
    - :meth:`invoke` — raw peer.* verb dispatch
    - :meth:`echo` — convenience wrapper for ``peer.echo``
    - :meth:`pay_required` — convenience wrapper for ``peer.pay.required``

    **Address book management** (trust model):

    - ``client.peer.address_book.*`` — list, add, pin, block, unblock, remove
    """

    def __init__(self, client: "Client") -> None:
        self._c = client
        self.address_book = _PeerAddressBookNamespace(client)

    def dial(self, *, did: str, endpoint: str) -> dict:
        """Open an authenticated Noise IK channel to a remote daimon.

        ``did`` must be a ``did:key:z6Mk…`` DID (the peer's identity key
        is embedded in the DID and used directly for Noise IK mutual auth).
        ``endpoint`` is the peer's TCP address — either ``"tcp://host:port"``
        or bare ``"host:port"``.

        On success, returns ``{channel_id, peer_did, opened_at}``. The
        ``channel_id`` is used by :meth:`invoke`, :meth:`close`, and
        :meth:`echo`. Multiple channels to the same peer are allowed.

        Also auto-populates the local address book: the peer is added as
        ``FirstSeen`` on first contact and ``LastSeen`` is updated on
        subsequent dials. A TOFU violation (transport pubkey changed since
        first sight) is audited but does NOT abort the dial — the Noise
        handshake is the security gate.

        Raises :class:`RPCError` with ``CodePeerUnreachable (-32010)`` on
        network failure or Noise handshake rejection.
        """
        return self._c._call("daimon.peer.dial", {"did": did, "endpoint": endpoint})

    def close(self, channel_id: str) -> None:
        """Close an open peer channel. Idempotent — does not error if already closed."""
        self._c._call("daimon.peer.close", {"channel_id": channel_id})

    def list(self) -> list[dict]:
        """List open outbound peer channels.

        Returns a list of ``{channel_id, peer_did, opened_at}`` dicts.
        An empty or null result is normalised to ``[]``.
        """
        result = self._c._call("daimon.peer.list", None)
        if result is None:
            return []
        return result.get("channels", []) or []

    def invoke(
        self,
        channel_id: str,
        method: str,
        params: Any | None = None,
    ) -> Any:
        """Raw peer verb dispatch — send ``method`` with ``params`` over
        ``channel_id`` and return the peer's ``result`` field verbatim.

        Use :meth:`echo` and :meth:`pay_required` for the common verbs.
        Use this method for custom peer verbs or future additions.

        Raises :class:`RPCError` with ``CodePeerUnreachable (-32010)`` if
        the channel was broken (remote closed, network timeout). The broken
        channel is removed from the open-channel map automatically — a new
        :meth:`dial` is needed to reconnect.
        """
        p: dict[str, Any] = {"channel_id": channel_id, "method": method}
        if params is not None:
            p["params"] = params
        result = self._c._call("daimon.peer.invoke", p)
        if isinstance(result, dict):
            return result.get("result")
        return result

    def echo(self, channel_id: str, *, message: str) -> dict:
        """Invoke ``peer.echo`` on the remote daimon.

        Sends ``message`` and returns ``{message, from_did}`` where
        ``from_did`` is the remote daimon's own DID. Available on every
        daimon regardless of address-book state — the canonical health-check
        / "is this channel still alive?" verb.
        """
        result = self.invoke(channel_id, "peer.echo", {"message": message})
        return result if isinstance(result, dict) else {}

    def pay_required(self, channel_id: str, *, service: str) -> list[dict]:
        """Query the remote daimon's payment requirements for ``service``.

        Returns a list of ``PaymentRequirements`` dicts (x402 v2 shape):
        ``{scheme, network, maxAmountRequired, resource, description,
        payTo, maxTimeoutSeconds, asset}``. Callers use the list to
        pick a (scheme, network, asset) tuple they can satisfy and construct
        the EIP-3009 authorization.

        In v0.3 only ``service="peer.ask"`` is supported. An empty list
        means the remote daimon hasn't configured a wallet for payments yet
        (treat as "service not available at this peer").

        Universally available — no address-book authorization required.
        """
        result = self.invoke(channel_id, "peer.pay.required", {"service": service})
        if isinstance(result, dict):
            return result.get("requirements", []) or []
        return []


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
        self.federation = _FederationNamespace(self)
        self.peer = _PeerNamespace(self)

    @property
    def socket_path(self) -> Path:
        """Resolved Unix-socket path the client dials."""
        return self._socket

    def _call(self, method: str, params: Any | None) -> Any:
        return _rpc.rpc_call(self._socket, method, params, timeout=self._timeout)
