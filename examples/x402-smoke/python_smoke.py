"""Daimon — Python SDK x402 cross-language smoke (Python half).

Pays the x402-mock-server's `/r` endpoint through `client.payment.pay`,
prints the response + parsed PaymentResponse, and reports
`activity.verify`'s view of the audit chain. Sibling of
typescript_smoke.mjs — both run against the same daimon, producing two
payment.signed + payment.settled audit-row pairs in one signed chain.

Prerequisites:

  1. A daimon is running and unlocked, with a wallet on evm:base:
       ./bin/daimon init
       ./bin/daimon unlock           # also auto-creates wallet
       ./bin/daimon wallet create --chain evm:base
  2. The mock x402 server is listening at the URL below:
       ./bin/x402-mock-server -addr 127.0.0.1:8402 &
  3. The Python SDK is installed (`pip install -e sdk/python`).

Run:

  python examples/x402-smoke/python_smoke.py
"""

from __future__ import annotations

import os
import sys
import time

from daimon import Client, DaemonNotRunning, RPCError


URL = os.environ.get("X402_URL", "http://127.0.0.1:8402/r")
CEILING = int(os.environ.get("X402_CEILING_SMALLEST", "100000"))  # $0.10 USDC default


def main() -> int:
    try:
        client = Client()
    except DaemonNotRunning as e:
        print(f"py: daemon not running — {e}", file=sys.stderr)
        return 1

    me = client.identity.get()
    print(f"py: DID = {me['did']}")

    wallets = client.wallet.list()
    if not wallets:
        print("py: no wallets in keystore — run `daimon wallet create --chain evm:base` first", file=sys.stderr)
        return 1
    print(f"py: wallets in keystore = {[w['chain'] for w in wallets]}")
    evm = next((w for w in wallets if w["chain"].startswith("evm:")), None)
    if evm is None:
        print("py: no EVM wallet in keystore", file=sys.stderr)
        return 1
    print(f"py: paying from {evm['address']} on {evm['chain']}")

    # Cross-language wire-shape check: derive at index 0 should match the
    # existing wallet's address (both go through the same daemon-side BIP-44
    # derivation pipeline). Catches drift between Python's wallet.derive
    # wrapper and the daemon's daimon.wallet.derive handler — and, paired
    # with the TS smoke's same assertion, drift between Python's wrapper
    # and TS's. If the field names or shape ever diverge, this trips.
    derived = client.wallet.derive(chain=evm["chain"], index=0)
    assert derived["address"] == evm["address"], (
        f"py: derive index 0 returned {derived['address']!r} "
        f"but wallet.list reports {evm['address']!r}"
    )
    assert derived["path"] == "m/44'/60'/0'/0/0", (
        f"py: derive index 0 returned path {derived['path']!r}, want m/44'/60'/0'/0/0"
    )
    print(f"py: derive index 0 matches wallet.list — wire-shape parity ✓")

    t0 = time.monotonic()
    try:
        result = client.payment.pay(
            url=URL,
            ceiling_smallest_unit=CEILING,
        )
    except RPCError as e:
        print(f"py: payment failed (rpc code {e.code}): {e.message}", file=sys.stderr)
        return 2
    elapsed_ms = (time.monotonic() - t0) * 1000

    print(f"py: HTTP {result['status_code']} in {elapsed_ms:.1f}ms")
    print(f"py: body = {result['body'].decode('utf-8', errors='replace').rstrip()!r}")
    if result.get("payment_response"):
        pr = result["payment_response"]
        print(
            f"py: PAYMENT-RESPONSE success={pr.get('success')} "
            f"tx={pr.get('transaction')} payer={pr.get('payer')}"
        )

    summary = client.activity.verify()
    print(f"py: activity.verify -> {summary}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
