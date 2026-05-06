"""Protocol-level: JSON-RPC envelope round-trip + error rewrites."""

from __future__ import annotations

from pathlib import Path

import pytest

from daimon import DaemonLocked, DaemonNotRunning, RPCError, _rpc

from .conftest import StubDaemon, StubRPCError


def test_call_returns_decoded_result(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.identity.get", {"did": "did:key:zABC"})
    result = _rpc.rpc_call(stub_daemon.socket_path, "daimon.identity.get", None)
    assert result == {"did": "did:key:zABC"}
    method, params = stub_daemon.calls[-1]
    assert method == "daimon.identity.get"
    # Match Go's json.Marshal: when params is None we omit the key
    # entirely so the server sees no params field, not params=null.
    assert params is None


def test_call_sends_params_when_given(stub_daemon: StubDaemon):
    received: dict = {}

    def echo(params):
        received.update(params)
        return {"id": "01K"}

    stub_daemon.handle("daimon.memory.write", echo)
    _rpc.rpc_call(
        stub_daemon.socket_path,
        "daimon.memory.write",
        {"kind": "note", "content": "hi"},
    )
    assert received == {"kind": "note", "content": "hi"}


def test_call_raises_daemon_not_running_when_socket_absent(short_tmp: Path):
    missing = short_tmp / "nope.sock"
    with pytest.raises(DaemonNotRunning):
        _rpc.rpc_call(missing, "daimon.identity.get", None)


def test_call_raises_daemon_locked_on_minus_32001(stub_daemon: StubDaemon):
    def locked(_params):
        raise StubRPCError(-32001, "unlock failed", "wrong password")

    stub_daemon.handle("daimon.identity.get", locked)
    with pytest.raises(DaemonLocked) as ei:
        _rpc.rpc_call(stub_daemon.socket_path, "daimon.identity.get", None)
    # Underlying RPC fields preserved on the typed exception so callers
    # that catch the family can still read the server-supplied detail.
    assert ei.value.code == -32001  # type: ignore[attr-defined]
    assert ei.value.message == "unlock failed"  # type: ignore[attr-defined]


def test_call_raises_rpc_error_for_other_codes(stub_daemon: StubDaemon):
    def boom(_params):
        raise StubRPCError(-32602, "id is required")

    stub_daemon.handle("daimon.memory.read", boom)
    with pytest.raises(RPCError) as ei:
        _rpc.rpc_call(stub_daemon.socket_path, "daimon.memory.read", {"id": ""})
    assert ei.value.code == -32602
    assert "id is required" in ei.value.message


def test_call_raises_rpc_error_on_unknown_method(stub_daemon: StubDaemon):
    with pytest.raises(RPCError) as ei:
        _rpc.rpc_call(stub_daemon.socket_path, "daimon.bogus", None)
    assert ei.value.code == -32601
    assert "method not found" in ei.value.message


def test_call_returns_none_when_result_omitted(stub_daemon: StubDaemon):
    # SPEC §6.1 allows result-less success responses. The SDK should
    # surface this as None rather than raising.
    def silent(_params):
        return None

    stub_daemon.handle("daimon.silent", silent)
    out = _rpc.rpc_call(stub_daemon.socket_path, "daimon.silent", None)
    assert out is None
