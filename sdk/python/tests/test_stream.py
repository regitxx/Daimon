"""Streaming surface: Client.provider.stream over notification frames + terminal envelope."""

from __future__ import annotations

import pytest

from daimon import Client, DaemonLocked, RPCError

from .conftest import StubDaemon, StubRPCError


def test_stream_yields_all_deltas_in_order(stub_daemon: StubDaemon):
    stub_daemon.stream(
        "daimon.provider.stream",
        deltas=["Hel", "lo, ", "wor", "ld"],
        terminal={
            "response": {
                "model": "llama3.2",
                "content": "Hello, world",
                "stop_reason": "end_turn",
                "usage": {"input_tokens": 5, "output_tokens": 4},
            }
        },
    )
    client = Client(socket_path=stub_daemon.socket_path)
    stream = client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
    )
    chunks = list(stream)
    assert chunks == ["Hel", "lo, ", "wor", "ld"]


def test_stream_final_envelope_populates_after_iteration(stub_daemon: StubDaemon):
    terminal = {
        "response": {
            "model": "llama3.2",
            "content": "Pong",
            "stop_reason": "end_turn",
            "usage": {"input_tokens": 32, "output_tokens": 3},
        },
        "injected_memory_ids": ["01ABC", "01DEF"],
    }
    stub_daemon.stream("daimon.provider.stream", deltas=["Pong"], terminal=terminal)
    client = Client(socket_path=stub_daemon.socket_path)
    stream = client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "say pong"}],
        inject_context={"query": "ping", "max_tokens": 64},
    )
    assert stream.final is None  # not yet exhausted
    for _ in stream:
        pass
    assert stream.final == terminal
    assert stream.final["injected_memory_ids"] == ["01ABC", "01DEF"]


def test_stream_assembles_nested_request_from_flat_kwargs(stub_daemon: StubDaemon):
    received: dict = {}

    def deltas(params):
        received.update(params)
        return ["x"]

    stub_daemon.stream(
        "daimon.provider.stream",
        deltas=deltas,
        terminal={"response": {"model": "m", "content": "x", "stop_reason": "end", "usage": {"input_tokens": 0, "output_tokens": 1}}},
    )
    client = Client(socket_path=stub_daemon.socket_path)
    stream = client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
        system="be terse",
        temperature=0.5,
        max_tokens=64,
    )
    list(stream)  # drain
    # Wire shape mirrors invoke: {provider, request: {model, messages, system?, ...}}
    assert received["provider"] == "ollama"
    req = received["request"]
    assert req["model"] == "llama3.2"
    assert req["messages"] == [{"role": "user", "content": "hi"}]
    assert req["system"] == "be terse"
    assert req["temperature"] == 0.5
    assert req["max_tokens"] == 64
    assert "inject_context" not in received


def test_stream_passes_inject_context_verbatim(stub_daemon: StubDaemon):
    received: dict = {}

    def deltas(params):
        received.update(params)
        return []  # no deltas — terminal only is valid

    stub_daemon.stream(
        "daimon.provider.stream",
        deltas=deltas,
        terminal={"response": {"model": "m", "content": "", "stop_reason": "end", "usage": {"input_tokens": 0, "output_tokens": 0}}},
    )
    client = Client(socket_path=stub_daemon.socket_path)
    stream = client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
        inject_context={"query": "todo", "max_tokens": 256, "kinds": ["fact"]},
    )
    list(stream)
    assert received["inject_context"] == {"query": "todo", "max_tokens": 256, "kinds": ["fact"]}


def test_stream_zero_deltas_terminal_only(stub_daemon: StubDaemon):
    # Mirrors the fallback-to-invoke case: providers without native
    # streaming (Claude/OpenAI/LM Studio) return the full content in the
    # terminal envelope with no deltas in between.
    stub_daemon.stream(
        "daimon.provider.stream",
        deltas=[],
        terminal={"response": {"model": "m", "content": "all at once", "stop_reason": "end", "usage": {"input_tokens": 1, "output_tokens": 3}}},
    )
    client = Client(socket_path=stub_daemon.socket_path)
    stream = client.provider.stream(
        provider="anthropic",
        model="claude-sonnet-4-6",
        messages=[{"role": "user", "content": "hi"}],
    )
    chunks = list(stream)
    assert chunks == []
    assert stream.final["response"]["content"] == "all at once"


def test_stream_ignores_unknown_notification_kinds(stub_daemon: StubDaemon):
    # Hand-craft frames with a mix of real deltas + an unknown notification
    # kind. The SDK should silently skip the unknown ones for forward-compat.
    import json as _json

    def write_frames(conn, stream_pair, params, req_id):  # pragma: no cover — patched below
        pass

    # Patch StubDaemon to write a custom frame sequence for one call.
    real_serve = stub_daemon._serve_stream

    def custom_serve(conn, stream_pair, params, req_id):
        # Send: real delta, unknown notification, real delta, terminal.
        conn.sendall((_json.dumps({"jsonrpc": "2.0", "method": "daimon.provider.stream.delta", "params": {"content": "A"}}) + "\n").encode())
        conn.sendall((_json.dumps({"jsonrpc": "2.0", "method": "daimon.provider.stream.tool", "params": {"name": "future"}}) + "\n").encode())
        conn.sendall((_json.dumps({"jsonrpc": "2.0", "method": "daimon.provider.stream.delta", "params": {"content": "B"}}) + "\n").encode())
        conn.sendall((_json.dumps({"jsonrpc": "2.0", "result": {"response": {"model": "m", "content": "AB", "stop_reason": "end", "usage": {"input_tokens": 0, "output_tokens": 0}}}, "id": req_id}) + "\n").encode())

    stub_daemon._serve_stream = custom_serve
    stub_daemon.stream("daimon.provider.stream", deltas=[], terminal={})
    try:
        client = Client(socket_path=stub_daemon.socket_path)
        stream = client.provider.stream(
            provider="ollama",
            model="llama3.2",
            messages=[{"role": "user", "content": "hi"}],
        )
        chunks = list(stream)
        assert chunks == ["A", "B"]
        assert stream.final["response"]["content"] == "AB"
    finally:
        stub_daemon._serve_stream = real_serve


def test_stream_terminal_error_raises_rpc_error(stub_daemon: StubDaemon):
    stub_daemon.stream(
        "daimon.provider.stream",
        deltas=["partial "],
        terminal=StubRPCError(-32603, "provider.ollama.stream: connection reset"),
    )
    client = Client(socket_path=stub_daemon.socket_path)
    stream = client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
    )
    delta = next(stream)
    assert delta == "partial "
    with pytest.raises(RPCError) as exc:
        next(stream)
    assert exc.value.code == -32603
    assert "connection reset" in exc.value.message


def test_stream_terminal_minus_32001_raises_daemon_locked(stub_daemon: StubDaemon):
    stub_daemon.stream(
        "daimon.provider.stream",
        deltas=[],
        terminal=StubRPCError(-32001, "identity is locked"),
    )
    client = Client(socket_path=stub_daemon.socket_path)
    stream = client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
    )
    with pytest.raises(DaemonLocked):
        next(stream)


def test_stream_context_manager_closes_socket_on_early_exit(stub_daemon: StubDaemon):
    stub_daemon.stream(
        "daimon.provider.stream",
        deltas=["a", "b", "c", "d"],
        terminal={"response": {"model": "m", "content": "abcd", "stop_reason": "end", "usage": {"input_tokens": 0, "output_tokens": 4}}},
    )
    client = Client(socket_path=stub_daemon.socket_path)
    with client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
    ) as stream:
        first = next(stream)
        assert first == "a"
        # Bail early.
    # Socket should be closed and the StreamHandle marked done. A new
    # call should still work — the stub spawns one thread per accept.
    with client.provider.stream(
        provider="ollama",
        model="llama3.2",
        messages=[{"role": "user", "content": "hi"}],
    ) as stream2:
        chunks = list(stream2)
        assert chunks == ["a", "b", "c", "d"]


def test_stream_peer_closes_without_terminal_raises(stub_daemon: StubDaemon):
    # Custom override: write two deltas then close without a terminal frame.
    import json as _json

    def custom_serve(conn, stream_pair, params, req_id):
        conn.sendall((_json.dumps({"jsonrpc": "2.0", "method": "daimon.provider.stream.delta", "params": {"content": "X"}}) + "\n").encode())
        conn.sendall((_json.dumps({"jsonrpc": "2.0", "method": "daimon.provider.stream.delta", "params": {"content": "Y"}}) + "\n").encode())
        # No terminal — just close.

    real = stub_daemon._serve_stream
    stub_daemon._serve_stream = custom_serve
    stub_daemon.stream("daimon.provider.stream", deltas=[], terminal={})
    try:
        client = Client(socket_path=stub_daemon.socket_path)
        stream = client.provider.stream(
            provider="ollama",
            model="llama3.2",
            messages=[{"role": "user", "content": "hi"}],
        )
        assert next(stream) == "X"
        assert next(stream) == "Y"
        with pytest.raises(RPCError) as exc:
            next(stream)
        assert "without terminal response" in exc.value.message
    finally:
        stub_daemon._serve_stream = real
