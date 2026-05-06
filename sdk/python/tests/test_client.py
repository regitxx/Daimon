"""Verb-level: Client.identity / Client.memory through the stub daemon."""

from __future__ import annotations

from pathlib import Path

import pytest

from daimon import Client, DaemonLocked

from .conftest import StubDaemon, StubRPCError


def test_client_resolves_socket_via_home_env(
    daimon_home: Path, monkeypatch: pytest.MonkeyPatch
):
    # When neither home nor socket_path is passed, the Client must dial
    # exactly the Go-CLI-compatible path. We verify the resolved path,
    # not the round-trip — that is what test_home / test_rpc cover.
    # ``.resolve()`` on both sides since macOS ``/tmp`` symlinks to
    # ``/private/tmp`` and the Client canonicalises during construction.
    client = Client()
    assert client.socket_path == (daimon_home / "daimon.sock").resolve()


def test_client_socket_path_override_wins(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.identity.get", {"did": "did:key:zABC"})
    client = Client(socket_path=stub_daemon.socket_path)
    assert client.identity.get() == {"did": "did:key:zABC"}


def test_identity_get_round_trip(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.identity.get", {"did": "did:key:zXYZ"})
    client = Client(socket_path=stub_daemon.socket_path)
    assert client.identity.get() == {"did": "did:key:zXYZ"}
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.identity.get"
    assert params is None


def test_memory_write_minimal(stub_daemon: StubDaemon):
    received = {}

    def write(params):
        received.update(params)
        return {"id": "01K7Q"}

    stub_daemon.handle("daimon.memory.write", write)
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.memory.write(kind="note", content="hello")
    assert out == {"id": "01K7Q"}
    # Optional fields stay omitted on the wire when not passed — keeps the
    # Python SDK aligned with the Go CLI's request body.
    assert received == {"kind": "note", "content": "hello"}


def test_memory_write_passes_metadata_and_source(stub_daemon: StubDaemon):
    received = {}

    def write(params):
        received.update(params)
        return {"id": "01K"}

    stub_daemon.handle("daimon.memory.write", write)
    client = Client(socket_path=stub_daemon.socket_path)
    client.memory.write(
        kind="note",
        content="x",
        metadata={"tag": "draft"},
        source="cli",
    )
    assert received["metadata"] == {"tag": "draft"}
    assert received["source"] == "cli"


def test_memory_read_round_trip(stub_daemon: StubDaemon):
    stub_daemon.handle(
        "daimon.memory.read",
        lambda p: {
            "id": p["id"],
            "kind": "note",
            "content": "the content",
            "metadata": {},
            "created_at": 1700000000,
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)
    mem = client.memory.read("01K")
    assert mem["id"] == "01K"
    assert mem["content"] == "the content"


def test_memory_search_returns_list(stub_daemon: StubDaemon):
    stub_daemon.handle(
        "daimon.memory.search",
        lambda p: [
            {"id": "01A", "kind": "note", "content": "alpha", "score": 0.9},
            {"id": "01B", "kind": "note", "content": "alpha-ish", "score": 0.7},
        ],
    )
    client = Client(socket_path=stub_daemon.socket_path)
    hits = client.memory.search("alpha", limit=5, kind="note")
    assert len(hits) == 2
    assert hits[0]["score"] == 0.9
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.memory.search"
    assert params == {"query": "alpha", "limit": 5, "kind": "note"}


def test_memory_search_returns_empty_list_on_null_result(stub_daemon: StubDaemon):
    # Go encodes a nil slice as JSON null; the SDK should normalise this
    # to [] so callers can iterate without a guard.
    stub_daemon.handle("daimon.memory.search", lambda p: None)
    client = Client(socket_path=stub_daemon.socket_path)
    assert client.memory.search("nothingever") == []


def test_memory_list_uses_empty_query(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.memory.search", lambda p: [])
    client = Client(socket_path=stub_daemon.socket_path)
    client.memory.list()
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.memory.search"
    # Mirrors cmd_memory.go::cmdMemoryList — list is search with query="".
    assert params == {"query": ""}


def test_locked_daemon_propagates_as_daemon_locked(stub_daemon: StubDaemon):
    def locked(_params):
        raise StubRPCError(-32001, "daemon is locked")

    stub_daemon.handle("daimon.memory.write", locked)
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(DaemonLocked):
        client.memory.write(kind="note", content="x")
