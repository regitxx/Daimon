"""Verb-level: Client.identity / Client.memory / Client.provider / Client.activity through the stub daemon."""

from __future__ import annotations

from pathlib import Path

import pytest

from daimon import Client, DaemonLocked, RPCError

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


# --- Client.provider ---------------------------------------------------------


def test_provider_list_returns_entries(stub_daemon: StubDaemon):
    stub_daemon.handle(
        "daimon.provider.list",
        [
            {
                "name": "ollama",
                "models": [{"id": "llama3.2:latest"}],
                "configured": True,
            },
            {"name": "anthropic", "models": [], "configured": False},
        ],
    )
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.provider.list()
    assert len(out) == 2
    assert out[0]["name"] == "ollama"
    assert out[0]["configured"] is True
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.provider.list"
    # nil params on the wire — mirrors the Go CLI's daemonCall("…list", nil, …).
    assert params is None


def test_provider_list_normalises_null_to_empty_list(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.provider.list", lambda _p: None)
    client = Client(socket_path=stub_daemon.socket_path)
    assert client.provider.list() == []


def test_provider_invoke_assembles_nested_request(stub_daemon: StubDaemon):
    received = {}

    def invoke(params):
        received.update(params)
        return {
            "response": {
                "model": "llama3.2",
                "content": "hello back",
                "stop_reason": "end_turn",
                "usage": {"input_tokens": 4, "output_tokens": 2},
            }
        }

    stub_daemon.handle("daimon.provider.invoke", invoke)
    client = Client(socket_path=stub_daemon.socket_path)
    env = client.provider.invoke(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
    )
    assert env["response"]["content"] == "hello back"
    # SDK assembles {provider, request: {model, messages}} from flat kwargs —
    # mirrors cmd_provider.go's request-struct construction.
    assert received["provider"] == "ollama"
    assert received["request"]["model"] == "llama3.2"
    assert received["request"]["messages"] == [{"role": "user", "content": "hi"}]
    # Optional fields stay omitted on the wire when not passed.
    assert "system" not in received["request"]
    assert "temperature" not in received["request"]
    assert "max_tokens" not in received["request"]
    assert "inject_context" not in received


def test_provider_invoke_threads_optional_fields(stub_daemon: StubDaemon):
    received = {}

    def invoke(params):
        received.update(params)
        return {"response": {"model": "m", "content": "", "stop_reason": "", "usage": {"input_tokens": 0, "output_tokens": 0}}}

    stub_daemon.handle("daimon.provider.invoke", invoke)
    client = Client(socket_path=stub_daemon.socket_path)
    client.provider.invoke(
        provider="anthropic",
        model="claude-sonnet-4-6",
        messages=[{"role": "user", "content": "x"}],
        system="be terse",
        temperature=0.5,
        max_tokens=128,
    )
    req = received["request"]
    assert req["system"] == "be terse"
    assert req["temperature"] == 0.5
    assert req["max_tokens"] == 128


def test_provider_invoke_passes_inject_context_verbatim(stub_daemon: StubDaemon):
    received = {}

    def invoke(params):
        received.update(params)
        return {
            "response": {"model": "m", "content": "ok", "stop_reason": "end", "usage": {"input_tokens": 0, "output_tokens": 0}},
            "injected_memory_ids": ["01ABC", "01DEF"],
        }

    stub_daemon.handle("daimon.provider.invoke", invoke)
    client = Client(socket_path=stub_daemon.socket_path)
    env = client.provider.invoke(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
        inject_context={"query": "todo", "max_tokens": 256, "kinds": ["fact"]},
    )
    assert received["inject_context"] == {"query": "todo", "max_tokens": 256, "kinds": ["fact"]}
    # Envelope-level metadata is preserved verbatim — callers iterate over
    # injected_memory_ids when inject_context is on, drop it when not.
    assert env["injected_memory_ids"] == ["01ABC", "01DEF"]


def test_provider_invoke_no_provider_registry_propagates_rpc_error(stub_daemon: StubDaemon):
    def no_registry(_params):
        # Mirrors handleProviderInvoke's CodeNotFound (-32002) when
        # s.providers is nil — most often hit by SDK tests against a daemon
        # without a provider-registry build.
        raise StubRPCError(-32002, "no provider registry attached to this daimon")

    stub_daemon.handle("daimon.provider.invoke", no_registry)
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(RPCError) as exc:
        client.provider.invoke(
            provider="anthropic",
            messages=[{"role": "user", "content": "x"}],
        )
    assert exc.value.code == -32002


# --- Client.activity ---------------------------------------------------------


def test_activity_append_minimal(stub_daemon: StubDaemon):
    received = {}

    def append(params):
        received.update(params)
        return {"id": "01KQ", "hash": "abc123"}

    stub_daemon.handle("daimon.activity.append", append)
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.activity.append(kind="custom.event")
    assert out == {"id": "01KQ", "hash": "abc123"}
    # payload omitted on the wire when not passed — matches the Go server's
    # omitempty on activityAppendParams.
    assert received == {"kind": "custom.event"}


def test_activity_append_with_payload(stub_daemon: StubDaemon):
    received = {}

    def append(params):
        received.update(params)
        return {"id": "01K", "hash": "h"}

    stub_daemon.handle("daimon.activity.append", append)
    client = Client(socket_path=stub_daemon.socket_path)
    client.activity.append(kind="custom.event", payload={"actor": "huckgod", "n": 42})
    assert received["payload"] == {"actor": "huckgod", "n": 42}


def test_activity_query_returns_entries(stub_daemon: StubDaemon):
    stub_daemon.handle(
        "daimon.activity.query",
        lambda _p: [
            {
                "id": "01A",
                "ts": 1700000000000,
                "kind": "memory.write",
                "payload": {"id": "01M", "kind": "fact"},
                "prev_hash": "00",
                "hash": "11",
            }
        ],
    )
    client = Client(socket_path=stub_daemon.socket_path)
    entries = client.activity.query()
    assert len(entries) == 1
    assert entries[0]["kind"] == "memory.write"
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.activity.query"
    # No filter args → omit params entirely on the wire (mirrors
    # daemonCall("…query", activityQueryWire{}, …) where every field has
    # omitempty so the encoded body is `{}`; the SDK goes one step further
    # and omits the params key altogether).
    assert params is None


def test_activity_query_threads_filters(stub_daemon: StubDaemon):
    received = {}

    def query(params):
        received.update(params or {})
        return []

    stub_daemon.handle("daimon.activity.query", query)
    client = Client(socket_path=stub_daemon.socket_path)
    client.activity.query(since=1700000000000, until=1800000000000, kind="memory.write", limit=20)
    assert received == {
        "since": 1700000000000,
        "until": 1800000000000,
        "kind": "memory.write",
        "limit": 20,
    }


def test_activity_query_normalises_null_to_empty_list(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.activity.query", lambda _p: None)
    client = Client(socket_path=stub_daemon.socket_path)
    assert client.activity.query() == []


def test_activity_verify_returns_envelope(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.activity.verify", {"verified": 7, "ok": True})
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.activity.verify()
    assert out == {"verified": 7, "ok": True}
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.activity.verify"
    # Empty-object params — mirrors daemonCall("…verify", struct{}{}, …) on
    # the Go CLI, which encodes to `{}` on the wire (not omitted).
    assert params == {}


def test_activity_verify_chain_failure_propagates(stub_daemon: StubDaemon):
    def broken(_params):
        # Server-side chain corruption maps to CodeInternalError (-32603) per
        # mapActivityError; the SDK surfaces it as a generic RPCError so
        # callers can switch on .code without a separate exception subtype.
        raise StubRPCError(-32603, "verify: chain broken at entry 3")

    stub_daemon.handle("daimon.activity.verify", broken)
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(RPCError) as exc:
        client.activity.verify()
    assert exc.value.code == -32603
    assert "chain broken" in exc.value.message
