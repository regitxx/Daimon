"""SDK verb tests: client.federation.* and client.peer.* (v0.3 phase 36).

All tests use the StubDaemon fixture — no real daemon process required.
The stub speaks byte-for-byte JSON-RPC 2.0 so the SDK's wire-encoding
path is fully exercised without touching the network.
"""

from __future__ import annotations

from daimon import Client

from .conftest import StubDaemon, StubRPCError


# ---------------------------------------------------------------------------
# federation.config
# ---------------------------------------------------------------------------

def test_federation_config_round_trip(stub_daemon: StubDaemon) -> None:
    """federation.config returns all fields including optional public_endpoint."""
    canned = {
        "did": "did:key:z6MkHello",
        "transport_pubkey_multibase": "z6MkHello",
        "did_methods": ["did:key"],
        "protocols": ["peer.echo", "peer.ask", "peer.pay.required"],
        "public_endpoint": "tcp://127.0.0.1:9999",
        "federation_version": "v0.3-draft",
    }
    stub_daemon.handle("daimon.federation.config", canned)
    client = Client(socket_path=stub_daemon.socket_path)

    result = client.federation.config()

    assert result["did"] == "did:key:z6MkHello"
    assert result["transport_pubkey_multibase"] == "z6MkHello"
    assert result["did_methods"] == ["did:key"]
    assert "peer.echo" in result["protocols"]
    assert "peer.ask" in result["protocols"]
    assert "peer.pay.required" in result["protocols"]
    assert result["public_endpoint"] == "tcp://127.0.0.1:9999"
    assert result["federation_version"] == "v0.3-draft"

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.federation.config"
    assert params is None  # no params on wire


def test_federation_config_no_endpoint(stub_daemon: StubDaemon) -> None:
    """federation.config without public_endpoint (PeerListen not called) is valid."""
    canned = {
        "did": "did:key:z6MkNoListen",
        "transport_pubkey_multibase": "z6MkNoListen",
        "did_methods": ["did:key"],
        "protocols": ["peer.echo"],
        "federation_version": "v0.3-draft",
    }
    stub_daemon.handle("daimon.federation.config", canned)
    client = Client(socket_path=stub_daemon.socket_path)

    result = client.federation.config()
    # public_endpoint absent from response — SDK should not crash
    assert "public_endpoint" not in result or result["public_endpoint"] == ""
    assert result["did"] == "did:key:z6MkNoListen"


# ---------------------------------------------------------------------------
# peer.dial / peer.close / peer.list
# ---------------------------------------------------------------------------

def test_peer_dial_round_trip(stub_daemon: StubDaemon) -> None:
    """peer.dial sends correct wire shape and returns channel metadata."""
    stub_daemon.handle(
        "daimon.peer.dial",
        {
            "channel_id": "01AAAA",
            "peer_did": "did:key:z6MkPeer",
            "opened_at": "2026-05-21T10:00:00Z",
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)

    result = client.peer.dial(did="did:key:z6MkPeer", endpoint="tcp://127.0.0.1:9999")

    assert result["channel_id"] == "01AAAA"
    assert result["peer_did"] == "did:key:z6MkPeer"
    assert result["opened_at"] == "2026-05-21T10:00:00Z"

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.peer.dial"
    assert params["did"] == "did:key:z6MkPeer"
    assert params["endpoint"] == "tcp://127.0.0.1:9999"


def test_peer_close_sends_channel_id(stub_daemon: StubDaemon) -> None:
    """peer.close sends channel_id and returns without error."""
    stub_daemon.handle("daimon.peer.close", {})
    client = Client(socket_path=stub_daemon.socket_path)

    client.peer.close("01AAAA")  # must not raise

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.peer.close"
    assert params["channel_id"] == "01AAAA"


def test_peer_list_returns_channels(stub_daemon: StubDaemon) -> None:
    """peer.list returns the channels list."""
    stub_daemon.handle(
        "daimon.peer.list",
        {
            "channels": [
                {
                    "channel_id": "01BBBB",
                    "peer_did": "did:key:z6MkB",
                    "opened_at": "2026-05-21T10:00:00Z",
                }
            ]
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)

    channels = client.peer.list()

    assert len(channels) == 1
    assert channels[0]["channel_id"] == "01BBBB"
    assert channels[0]["peer_did"] == "did:key:z6MkB"


def test_peer_list_empty_normalised(stub_daemon: StubDaemon) -> None:
    """peer.list with empty channels normalises to [] (not null/dict)."""
    stub_daemon.handle("daimon.peer.list", {"channels": []})
    client = Client(socket_path=stub_daemon.socket_path)

    assert client.peer.list() == []


# ---------------------------------------------------------------------------
# peer.invoke / peer.echo / peer.pay_required
# ---------------------------------------------------------------------------

def test_peer_invoke_round_trip(stub_daemon: StubDaemon) -> None:
    """peer.invoke sends correct wire shape and unwraps the result field."""
    stub_daemon.handle(
        "daimon.peer.invoke",
        {"result": {"message": "hello", "from_did": "did:key:z6MkRemote"}},
    )
    client = Client(socket_path=stub_daemon.socket_path)

    result = client.peer.invoke("01AAAA", "peer.echo", {"message": "hello"})

    assert result == {"message": "hello", "from_did": "did:key:z6MkRemote"}

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.peer.invoke"
    assert params["channel_id"] == "01AAAA"
    assert params["method"] == "peer.echo"
    assert params["params"] == {"message": "hello"}


def test_peer_invoke_no_params(stub_daemon: StubDaemon) -> None:
    """peer.invoke without params omits 'params' key from wire shape."""
    stub_daemon.handle("daimon.peer.invoke", {"result": {}})
    client = Client(socket_path=stub_daemon.socket_path)

    client.peer.invoke("01AAAA", "peer.echo")

    _, wire_params = stub_daemon.calls[-1]
    assert "params" not in wire_params  # must not send null params on wire


def test_peer_echo_convenience(stub_daemon: StubDaemon) -> None:
    """peer.echo wraps peer.invoke with peer.echo method and unwraps result."""
    stub_daemon.handle(
        "daimon.peer.invoke",
        {"result": {"message": "hi there", "from_did": "did:key:z6MkRemote"}},
    )
    client = Client(socket_path=stub_daemon.socket_path)

    result = client.peer.echo("01AAAA", message="hi there")

    assert result["message"] == "hi there"
    assert result["from_did"] == "did:key:z6MkRemote"

    _, wire_params = stub_daemon.calls[-1]
    assert wire_params["method"] == "peer.echo"
    assert wire_params["params"] == {"message": "hi there"}


def test_peer_pay_required_returns_requirements(stub_daemon: StubDaemon) -> None:
    """peer.pay_required invokes peer.pay.required and returns the requirements list."""
    req = {
        "scheme": "exact",
        "network": "base-sepolia",
        "maxAmountRequired": "1000000",
        "resource": "peer.ask",
        "description": "1.00 USDC",
        "payTo": "0xDEAD",
        "maxTimeoutSeconds": 300,
        "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
    }
    stub_daemon.handle(
        "daimon.peer.invoke",
        {"result": {"requirements": [req]}},
    )
    client = Client(socket_path=stub_daemon.socket_path)

    reqs = client.peer.pay_required("01AAAA", service="peer.ask")

    assert len(reqs) == 1
    assert reqs[0]["scheme"] == "exact"
    assert reqs[0]["network"] == "base-sepolia"
    assert reqs[0]["payTo"] == "0xDEAD"
    assert reqs[0]["maxAmountRequired"] == "1000000"

    _, wire_params = stub_daemon.calls[-1]
    assert wire_params["method"] == "peer.pay.required"
    assert wire_params["params"] == {"service": "peer.ask"}


def test_peer_pay_required_empty_when_no_wallet(stub_daemon: StubDaemon) -> None:
    """peer.pay_required returns [] when stub returns null result (no wallet)."""
    stub_daemon.handle("daimon.peer.invoke", {"result": None})
    client = Client(socket_path=stub_daemon.socket_path)

    reqs = client.peer.pay_required("01AAAA", service="peer.ask")
    assert reqs == []


# ---------------------------------------------------------------------------
# peer.address_book.*
# ---------------------------------------------------------------------------

def test_peer_address_book_list(stub_daemon: StubDaemon) -> None:
    """address_book.list returns the entries list."""
    entry = {
        "did": "did:key:z6MkPeer",
        "label": "Alice",
        "status": "Pinned",
        "approved_verbs": ["peer.ask"],
        "transport_pubkey_multibase": "z6MkPeer",
        "first_seen": "2026-05-21T00:00:00Z",
        "last_seen": "2026-05-21T10:00:00Z",
    }
    stub_daemon.handle(
        "daimon.peer.address_book.list",
        {"entries": [entry]},
    )
    client = Client(socket_path=stub_daemon.socket_path)

    entries = client.peer.address_book.list()

    assert len(entries) == 1
    assert entries[0]["did"] == "did:key:z6MkPeer"
    assert entries[0]["status"] == "Pinned"
    assert "peer.ask" in entries[0]["approved_verbs"]


def test_peer_address_book_list_empty(stub_daemon: StubDaemon) -> None:
    """address_book.list with empty entries normalises to []."""
    stub_daemon.handle("daimon.peer.address_book.list", {"entries": []})
    client = Client(socket_path=stub_daemon.socket_path)

    assert client.peer.address_book.list() == []


def test_peer_address_book_add_sends_did(stub_daemon: StubDaemon) -> None:
    """address_book.add sends at minimum {did} on the wire."""
    stub_daemon.handle("daimon.peer.address_book.add", {"ok": True})
    client = Client(socket_path=stub_daemon.socket_path)

    client.peer.address_book.add(did="did:key:z6MkNew")

    _, params = stub_daemon.calls[-1]
    assert params["did"] == "did:key:z6MkNew"
    # Optional fields omitted when not passed
    assert "label" not in params
    assert "pubkey_multibase" not in params


def test_peer_address_book_add_with_label(stub_daemon: StubDaemon) -> None:
    """address_book.add includes label and pubkey_multibase when supplied."""
    stub_daemon.handle("daimon.peer.address_book.add", {"ok": True})
    client = Client(socket_path=stub_daemon.socket_path)

    client.peer.address_book.add(
        did="did:key:z6MkNew",
        label="Bob",
        pubkey_multibase="z6MkNew",
    )

    _, params = stub_daemon.calls[-1]
    assert params["label"] == "Bob"
    assert params["pubkey_multibase"] == "z6MkNew"


def test_peer_address_book_pin_sends_verbs(stub_daemon: StubDaemon) -> None:
    """address_book.pin sends {did, verbs} on the wire."""
    stub_daemon.handle("daimon.peer.address_book.pin", {"ok": True})
    client = Client(socket_path=stub_daemon.socket_path)

    client.peer.address_book.pin(did="did:key:z6MkPeer", verbs=["peer.ask"])

    _, params = stub_daemon.calls[-1]
    assert params["did"] == "did:key:z6MkPeer"
    assert params["verbs"] == ["peer.ask"]


def test_peer_address_book_block(stub_daemon: StubDaemon) -> None:
    stub_daemon.handle("daimon.peer.address_book.block", {"ok": True})
    client = Client(socket_path=stub_daemon.socket_path)
    client.peer.address_book.block(did="did:key:z6MkEvil")
    _, params = stub_daemon.calls[-1]
    assert params["did"] == "did:key:z6MkEvil"


def test_peer_address_book_unblock(stub_daemon: StubDaemon) -> None:
    stub_daemon.handle("daimon.peer.address_book.unblock", {"ok": True})
    client = Client(socket_path=stub_daemon.socket_path)
    client.peer.address_book.unblock(did="did:key:z6MkEvil")
    _, params = stub_daemon.calls[-1]
    assert params["did"] == "did:key:z6MkEvil"


def test_peer_address_book_remove(stub_daemon: StubDaemon) -> None:
    stub_daemon.handle("daimon.peer.address_book.remove", {"ok": True})
    client = Client(socket_path=stub_daemon.socket_path)
    client.peer.address_book.remove(did="did:key:z6MkOld")
    _, params = stub_daemon.calls[-1]
    assert params["did"] == "did:key:z6MkOld"
