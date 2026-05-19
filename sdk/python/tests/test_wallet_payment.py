"""v0.2 verb-level tests: Client.wallet.* and Client.payment.pay.

Same stub-daemon harness as test_client.py — each test registers a
handler for the RPC method under test, then drives the SDK and
asserts on the wire shape it produced.
"""

from __future__ import annotations

import base64

import pytest

from daimon import Client, RPCError

from .conftest import StubDaemon, StubRPCError


# --- Client.wallet -----------------------------------------------------------


def test_wallet_list_empty_normalises_to_empty_list(stub_daemon: StubDaemon):
    # Mirrors memory.search / activity.query: a null result is the
    # daemon's idiomatic "no rows" and the SDK normalises it to [].
    stub_daemon.handle("daimon.wallet.list", lambda _p: None)
    client = Client(socket_path=stub_daemon.socket_path)
    assert client.wallet.list() == []


def test_wallet_list_returns_entries(stub_daemon: StubDaemon):
    entries = [
        {
            "id": "01K",
            "chain": "evm:base",
            "path": "m/44'/60'/0'/0/0",
            "address": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
            "pubkey": "02" + "00" * 32,
            "created_at": 1700000000,
        }
    ]
    stub_daemon.handle("daimon.wallet.list", entries)
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.wallet.list()
    assert len(out) == 1
    assert out[0]["chain"] == "evm:base"
    assert out[0]["address"].startswith("0x")


def test_wallet_create_threads_chain_param(stub_daemon: StubDaemon):
    received: dict = {}

    def create(params):
        received.update(params)
        return {
            "id": "01K",
            "chain": params["chain"],
            "path": "m/44'/60'/0'/0/0",
            "address": "0xFFFF000000000000000000000000000000000000",
            "pubkey": "02" + "ff" * 32,
            "created_at": 1700000000,
        }

    stub_daemon.handle("daimon.wallet.create", create)
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.wallet.create(chain="evm:base-sepolia")
    assert received == {"chain": "evm:base-sepolia"}
    assert out["chain"] == "evm:base-sepolia"


def test_wallet_address_returns_string(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.wallet.address", {"address": "0x123"})
    client = Client(socket_path=stub_daemon.socket_path)
    assert client.wallet.address(chain="evm:base") == "0x123"


def test_wallet_sign_returns_signature_hex(stub_daemon: StubDaemon):
    received: dict = {}

    def sign(params):
        received.update(params)
        return {"signature_hex": "0x" + "ab" * 65}

    stub_daemon.handle("daimon.wallet.sign", sign)
    client = Client(socket_path=stub_daemon.socket_path)
    sig = client.wallet.sign(chain="evm:base", digest_hex="0x" + "11" * 32)
    assert received == {"chain": "evm:base", "digest_hex": "0x" + "11" * 32}
    assert sig.startswith("0x") and len(sig) == 2 + 130


def test_wallet_show_mnemonic_returns_word_list(stub_daemon: StubDaemon):
    received: dict = {}

    def show(params):
        received.update(params)
        return {
            "mnemonic": [
                "abandon", "abandon", "abandon", "abandon",
                "abandon", "abandon", "abandon", "abandon",
                "abandon", "abandon", "abandon", "abandon",
                "abandon", "abandon", "abandon", "abandon",
                "abandon", "abandon", "abandon", "abandon",
                "abandon", "abandon", "abandon", "art",
            ],
        }

    stub_daemon.handle("daimon.wallet.show_mnemonic", show)
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.wallet.show_mnemonic(password="hunter2")
    assert received == {"password": "hunter2"}
    assert isinstance(out, list)
    assert len(out) == 24
    assert out[-1] == "art"


def test_wallet_show_mnemonic_wrong_password_surfaces_typed_code(stub_daemon: StubDaemon):
    def reject(_p):
        # -32008 == CodeWrongPassword in the daemon. Distinct from
        # CodeIdentityLocked (-32001) so callers can distinguish
        # "type your password again" from "run daimon unlock".
        raise StubRPCError(-32008, "wrong password")

    stub_daemon.handle("daimon.wallet.show_mnemonic", reject)
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(RPCError) as exc:
        client.wallet.show_mnemonic(password="WRONG")
    assert exc.value.code == -32008


def test_wallet_create_unsupported_chain_propagates_rpc_error(stub_daemon: StubDaemon):
    def reject(_p):
        raise StubRPCError(-32602, "unsupported chain", "stellar")

    stub_daemon.handle("daimon.wallet.create", reject)
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(RPCError) as exc:
        client.wallet.create(chain="stellar")
    assert exc.value.code == -32602


# --- Client.payment.pay ------------------------------------------------------


def test_payment_pay_happy_path_decodes_body(stub_daemon: StubDaemon):
    received: dict = {}

    def pay(params):
        received.update(params)
        return {
            "status_code": 200,
            "response_headers": {"Content-Type": "text/plain"},
            "response_body_base64": base64.b64encode(b"paid resource").decode("ascii"),
            "payment_response": {
                "success": True,
                "transaction": "0xabc",
                "network": "base",
                "payer": "0xff",
            },
        }

    stub_daemon.handle("daimon.payment.pay", pay)
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.payment.pay(
        url="https://example.com/r",
        ceiling_smallest_unit=100000,
    )
    assert received["url"] == "https://example.com/r"
    assert received["method"] == "GET"
    assert received["ceiling_smallest_unit"] == "100000"
    assert out["status_code"] == 200
    assert out["body"] == b"paid resource"  # decoded on the SDK side
    assert out["payment_response"]["success"] is True
    assert out["payment_response"]["transaction"] == "0xabc"


def test_payment_pay_body_bytes_become_body_base64(stub_daemon: StubDaemon):
    received: dict = {}

    def pay(params):
        received.update(params)
        return {
            "status_code": 200,
            "response_headers": {},
            "response_body_base64": "",
        }

    stub_daemon.handle("daimon.payment.pay", pay)
    client = Client(socket_path=stub_daemon.socket_path)
    client.payment.pay(url="https://e.invalid/", body=b"\x00\x01\x02hello")
    # SDK should have encoded the bytes as base64 on the wire.
    expected = base64.b64encode(b"\x00\x01\x02hello").decode("ascii")
    assert received["body_base64"] == expected
    assert "body_text" not in received


def test_payment_pay_body_str_becomes_body_text(stub_daemon: StubDaemon):
    received: dict = {}

    def pay(params):
        received.update(params)
        return {"status_code": 200, "response_headers": {}, "response_body_base64": ""}

    stub_daemon.handle("daimon.payment.pay", pay)
    client = Client(socket_path=stub_daemon.socket_path)
    client.payment.pay(url="https://e.invalid/", body="hello world")
    assert received["body_text"] == "hello world"
    assert "body_base64" not in received


def test_payment_pay_body_invalid_type_raises_typeerror(stub_daemon: StubDaemon):
    stub_daemon.handle("daimon.payment.pay", lambda _p: {})
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(TypeError):
        client.payment.pay(url="https://e.invalid/", body=12345)  # type: ignore[arg-type]


def test_payment_pay_optional_params_omitted_when_none(stub_daemon: StubDaemon):
    received: dict = {}

    def pay(params):
        received.update(params)
        return {"status_code": 200, "response_headers": {}, "response_body_base64": ""}

    stub_daemon.handle("daimon.payment.pay", pay)
    client = Client(socket_path=stub_daemon.socket_path)
    client.payment.pay(url="https://e.invalid/")
    # Only url + method get sent when everything else is None/default.
    assert set(received.keys()) == {"url", "method"}


def test_payment_pay_threads_headers(stub_daemon: StubDaemon):
    received: dict = {}

    def pay(params):
        received.update(params)
        return {"status_code": 200, "response_headers": {}, "response_body_base64": ""}

    stub_daemon.handle("daimon.payment.pay", pay)
    client = Client(socket_path=stub_daemon.socket_path)
    client.payment.pay(
        url="https://e.invalid/",
        headers={"X-Custom": "v"},
    )
    assert received["headers"] == {"X-Custom": "v"}


def test_payment_pay_ceiling_exceeded_surfaces_typed_code(stub_daemon: StubDaemon):
    def reject(_p):
        raise StubRPCError(-32006, "payment exceeds local ceiling", "required > ceiling")

    stub_daemon.handle("daimon.payment.pay", reject)
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(RPCError) as exc:
        client.payment.pay(url="https://e.invalid/", ceiling_smallest_unit=100)
    assert exc.value.code == -32006


def test_payment_pay_unsupported_requirement_surfaces_typed_code(stub_daemon: StubDaemon):
    def reject(_p):
        raise StubRPCError(-32007, "no wallet matches", "polygon not in v0.2 registry")

    stub_daemon.handle("daimon.payment.pay", reject)
    client = Client(socket_path=stub_daemon.socket_path)
    with pytest.raises(RPCError) as exc:
        client.payment.pay(url="https://e.invalid/")
    assert exc.value.code == -32007


def test_payment_pay_handles_missing_body_field(stub_daemon: StubDaemon):
    # The daemon may legitimately omit response_body_base64 on a 204 /
    # head-only response. The SDK should normalise to empty bytes.
    stub_daemon.handle(
        "daimon.payment.pay",
        {"status_code": 204},
    )
    client = Client(socket_path=stub_daemon.socket_path)
    out = client.payment.pay(url="https://e.invalid/")
    assert out["status_code"] == 204
    assert out["body"] == b""
    assert out["response_headers"] == {}
    assert out["payment_response"] is None
