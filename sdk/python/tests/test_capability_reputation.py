"""SDK verb tests: client.capability.* and client.reputation.* (v0.4 phase 46).

All tests use the StubDaemon fixture — no real daemon process required.
The stub speaks byte-for-byte JSON-RPC 2.0 so the SDK's wire-encoding
path is fully exercised without touching the network.

Pair with sdk/typescript/test/capability_reputation.test.ts — both files
cover the same surface (issue/list/revoke/attenuate + receipts) to ensure
the two SDKs stay byte-identical on the wire.
"""

from __future__ import annotations

import pytest

from daimon import Client

from .conftest import StubDaemon, StubRPCError


# ---------------------------------------------------------------------------
# capability.issue
# ---------------------------------------------------------------------------


def test_capability_issue_minimal(stub_daemon: StubDaemon) -> None:
    """issue with only verbs sends a minimal request and returns the wire shape."""
    stub_daemon.handle(
        "daimon.capability.issue",
        {
            "token_id": "01KTOKEN",
            "token": "base64urltoken",
            "expires_at": "",
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)

    result = client.capability.issue(verbs=["peer.ask"])

    assert result["token_id"] == "01KTOKEN"
    assert result["token"] == "base64urltoken"
    assert result["expires_at"] == ""

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.capability.issue"
    assert params == {"verbs": ["peer.ask"]}


def test_capability_issue_all_options(stub_daemon: StubDaemon) -> None:
    """issue forwards every optional constraint with the wire field name."""
    stub_daemon.handle(
        "daimon.capability.issue",
        {
            "token_id": "01KTOKEN",
            "token": "base64urltoken",
            "expires_at": "2027-01-01T00:00:00Z",
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)

    client.capability.issue(
        verbs=["peer.ask", "peer.echo"],
        valid_until="2027-01-01T00:00:00Z",
        max_calls=10,
        model_constraint="claude-haiku-4-5",
        grantee_did="did:key:z6MkPeer",
    )

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.capability.issue"
    assert params == {
        "verbs": ["peer.ask", "peer.echo"],
        "valid_until": "2027-01-01T00:00:00Z",
        "max_calls": 10,
        "model_constraint": "claude-haiku-4-5",
        "grantee_did": "did:key:z6MkPeer",
    }


def test_capability_issue_rejects_empty_verbs(stub_daemon: StubDaemon) -> None:
    """Client-side guard: verbs must be non-empty (matches daemon validation)."""
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(ValueError, match="verbs is required"):
        client.capability.issue(verbs=[])
    # No call should have reached the daemon.
    assert all(m != "daimon.capability.issue" for m, _ in stub_daemon.calls)


# ---------------------------------------------------------------------------
# capability.list
# ---------------------------------------------------------------------------


def test_capability_list_returns_tokens(stub_daemon: StubDaemon) -> None:
    """list returns the wire tokens list, normalising None → []."""
    stub_daemon.handle(
        "daimon.capability.list",
        {
            "tokens": [
                {
                    "token_id": "01KTOKEN",
                    "verbs": ["peer.ask"],
                    "grantee_did": "did:key:z6MkPeer",
                    "issued_at": "2026-05-24T20:00:00Z",
                    "revoked": False,
                },
            ]
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)

    tokens = client.capability.list()

    assert len(tokens) == 1
    assert tokens[0]["token_id"] == "01KTOKEN"
    assert tokens[0]["verbs"] == ["peer.ask"]
    assert tokens[0]["revoked"] is False

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.capability.list"
    assert params == {"include_revoked": False}


def test_capability_list_include_revoked(stub_daemon: StubDaemon) -> None:
    """include_revoked=True propagates on the wire."""
    stub_daemon.handle("daimon.capability.list", {"tokens": []})
    client = Client(socket_path=stub_daemon.socket_path)

    tokens = client.capability.list(include_revoked=True)

    assert tokens == []
    _, params = stub_daemon.calls[-1]
    assert params == {"include_revoked": True}


def test_capability_list_empty_response_returns_empty_list(
    stub_daemon: StubDaemon,
) -> None:
    """Both null and {tokens: null} normalise to []."""
    stub_daemon.handle("daimon.capability.list", {})
    client = Client(socket_path=stub_daemon.socket_path)

    assert client.capability.list() == []


# ---------------------------------------------------------------------------
# capability.revoke
# ---------------------------------------------------------------------------


def test_capability_revoke(stub_daemon: StubDaemon) -> None:
    """revoke returns None and sends the token_id verbatim."""
    stub_daemon.handle("daimon.capability.revoke", {})
    client = Client(socket_path=stub_daemon.socket_path)

    result = client.capability.revoke("01KTOKEN")

    assert result is None
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.capability.revoke"
    assert params == {"token_id": "01KTOKEN"}


# ---------------------------------------------------------------------------
# capability.attenuate
# ---------------------------------------------------------------------------


def test_capability_attenuate_returns_new_token(stub_daemon: StubDaemon) -> None:
    """attenuate returns the new base64url token from the wire."""
    stub_daemon.handle(
        "daimon.capability.attenuate", {"token": "tightertoken"}
    )
    client = Client(socket_path=stub_daemon.socket_path)

    new_token = client.capability.attenuate(
        "originaltoken",
        valid_until="2026-12-31T00:00:00Z",
        max_calls=3,
    )

    assert new_token == "tightertoken"
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.capability.attenuate"
    assert params == {
        "token": "originaltoken",
        "valid_until": "2026-12-31T00:00:00Z",
        "max_calls": 3,
    }


# ---------------------------------------------------------------------------
# reputation.receipts
# ---------------------------------------------------------------------------


def test_reputation_receipts_no_filter(stub_daemon: StubDaemon) -> None:
    """receipts() with no direction returns the full list, sends empty params."""
    stub_daemon.handle(
        "daimon.reputation.receipts",
        {
            "receipts": [
                {
                    "receipt_id": "01KRECEIPT",
                    "direction": "issued",
                    "served_at": "2026-05-24T20:00:00Z",
                    "verb": "peer.ask",
                    "server_did": "did:key:z6MkB",
                    "caller_did": "did:key:z6MkA",
                    "provider": "mock",
                    "model": "mock-1",
                    "input_tokens": 12,
                    "output_tokens": 24,
                    "duration_ms": 350,
                    "signature": "base64sig",
                }
            ]
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)

    receipts = client.reputation.receipts()

    assert len(receipts) == 1
    r = receipts[0]
    assert r["receipt_id"] == "01KRECEIPT"
    assert r["direction"] == "issued"
    assert r["verb"] == "peer.ask"
    assert r["signature"] == "base64sig"

    method, params = stub_daemon.calls[-1]
    assert method == "daimon.reputation.receipts"
    assert params == {}


def test_reputation_receipts_filter_by_direction(stub_daemon: StubDaemon) -> None:
    """direction='issued' / 'received' propagates on the wire."""
    stub_daemon.handle("daimon.reputation.receipts", {"receipts": []})
    client = Client(socket_path=stub_daemon.socket_path)

    client.reputation.receipts(direction="issued")
    _, params = stub_daemon.calls[-1]
    assert params == {"direction": "issued"}

    client.reputation.receipts(direction="received")
    _, params = stub_daemon.calls[-1]
    assert params == {"direction": "received"}


def test_reputation_receipts_empty_response_returns_empty_list(
    stub_daemon: StubDaemon,
) -> None:
    """Both null and missing key normalise to []."""
    stub_daemon.handle("daimon.reputation.receipts", {})
    client = Client(socket_path=stub_daemon.socket_path)

    assert client.reputation.receipts() == []


# ---------------------------------------------------------------------------
# Error path: CodeCapabilityDenied surfaces as RPCError
# ---------------------------------------------------------------------------


def test_capability_denied_error_surfaces(stub_daemon: StubDaemon) -> None:
    """A CodeCapabilityDenied (-32014) from the daemon raises RPCError."""
    from daimon import RPCError

    def deny(_params):
        raise StubRPCError(-32014, "capability: token expired")

    stub_daemon.handle("daimon.capability.revoke", deny)
    client = Client(socket_path=stub_daemon.socket_path)

    with pytest.raises(RPCError) as ei:
        client.capability.revoke("01KTOKEN")
    assert ei.value.code == -32014
    assert "expired" in str(ei.value).lower()
