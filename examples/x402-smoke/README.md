# Cross-language x402 smoke

Two scripts — one Python, one TypeScript — that pay the same
[mock x402 server](../../cmd/x402-mock-server/) through the same running
daimon, demonstrating that both SDKs encode wire-shape-equivalent
EIP-3009 authorizations. Sibling in spirit to
[`examples/streaming/`](../streaming/) for v0.1; the structural
property both prove is that the protocol's language-neutrality is
real — every byte the daimon writes to its audit log is reachable
identically from Python, TypeScript, and the Go CLI.

The mock x402 server lives outside the test process (real HTTP on a
real socket, not an `httptest.Server` inside a Go test). It
**cryptographically verifies the PAYMENT-SIGNATURE** by recovering the
public key from the secp256k1 signature and asserting it matches the
authorization's `from` address — the same property a production x402
facilitator (Coinbase CDP or self-hosted) checks before `/settle`
submits the transaction on-chain. What it does NOT do: actually
move stablecoins. The `PAYMENT-RESPONSE` it emits names a synthetic
transaction hash so daimon's audit log gets a `payment.settled` row,
but no blockchain settlement happens.

## Quick start: one-line runner

If you just want to see the smoke green end-to-end, run the orchestrator
from the repo root:

```sh
./examples/x402-smoke/run.sh
```

It does the whole dance: builds binaries, installs the Python SDK
in editable mode if missing, builds the TS SDK dist if missing,
allocates a temp `DAIMON_HOME`, init+unlocks the daimon (password
piped non-interactively), creates an `evm:base` wallet, starts the
mock x402 server, runs both SDK smokes, runs `daimon activity verify`,
asserts the audit log has 2 `payment.signed` + 2 `payment.settled`
rows both attributing to the wallet, and tears down the mock server.
Leaves the `DAIMON_HOME` on disk for post-mortem; rm it when done.

The runner is also wired into CI as the `x402-smoke` job in
`.github/workflows/ci.yml` — so every push to `main` re-verifies that
the Python and TS SDK EIP-3009 encoders are still wire-equivalent
against a real-network mock server.

The rest of this README walks through what the runner does step by
step, for cases where you want to inspect or modify individual pieces.

## Prerequisites (manual run)

1. **Build the daimon binaries + the mock server.** From the repo root:
   ```sh
   make build
   go build -o bin/x402-mock-server ./cmd/x402-mock-server
   ```

2. **Start the mock server** in another terminal:
   ```sh
   ./bin/x402-mock-server -addr 127.0.0.1:8402
   ```
   You should see:
   ```
   x402-mock-server listening on http://127.0.0.1:8402/
     network=base chain_id=8453 usdc=0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913
     pay-to=0xfFFf0000000000000000000000000000000000ff amount=100 smallest-units
   ```

3. **Init + unlock + wallet** in a working terminal:
   ```sh
   ./bin/daimon init        # once — choose a password
   ./bin/daimon unlock      # auto-spawns daimond + auto-creates wallet keystore
                            # MNEMONIC is surfaced once; back it up if real
   ./bin/daimon wallet create --chain evm:base
   ```
   The unlock step surfaces a 24-word BIP-39 mnemonic in a deliberately-
   loud banner. For a local smoke you can discard it; for any real use
   write it down.

4. **Install the SDKs:**
   ```sh
   pip install -e sdk/python
   (cd sdk/typescript && npm install && npm run build)
   ```

## Run

```sh
python   examples/x402-smoke/python_smoke.py
node     examples/x402-smoke/typescript_smoke.mjs
./bin/daimon activity verify
```

Each script:

- Resolves the daemon socket from `$DAIMON_HOME`.
- Pulls the wallet list to confirm an EVM wallet exists.
- Calls `client.payment.pay(...)` against the mock server with a
  ceiling of $0.10 USDC (overridable via `X402_CEILING_SMALLEST` env).
- Prints the HTTP status, decoded body, parsed `PAYMENT-RESPONSE`,
  and the `activity.verify` summary.

## What a healthy run looks like

```
$ python examples/x402-smoke/python_smoke.py
py: DID = did:key:z6Mk...
py: wallets in keystore = ['evm:base']
py: paying from 0x... on evm:base
py: HTTP 200 in 24.3ms
py: body = 'paid resource served to 0x... (network=base, amount=100)'
py: PAYMENT-RESPONSE success=True tx=0xababab... payer=0x...
py: activity.verify -> {'verified': 4, 'ok': True}

$ node examples/x402-smoke/typescript_smoke.mjs
ts: DID = did:key:z6Mk...
ts: wallets in keystore = ["evm:base"]
ts: paying from 0x... on evm:base
ts: HTTP 200 in 22.1ms
ts: body = "paid resource served to 0x... (network=base, amount=100)"
ts: PAYMENT-RESPONSE success=true tx=0xababab... payer=0x...
ts: activity.verify -> {"verified":6,"ok":true}

$ ./bin/daimon activity verify
verified 7 entries — chain ok
```

The `verified` count grows monotonically across the three observers:
Python sees genesis + `wallet.created` + its own `payment.signed` +
`payment.settled`; TypeScript sees those plus Python's
`activity.verified` self-append plus its own `payment.signed` +
`payment.settled`; the CLI sees all of it plus TS's `activity.verified`.
Both SDK clients and the CLI walk the same chain under one DID and
agree on every hash and Ed25519 signature.

## What this proves

1. **Both SDKs encode wire-shape-equivalent EIP-3009 authorizations.**
   The mock server recovers the same public key from both Python and
   TypeScript signatures, and that public key matches the wallet's
   address. If either SDK's encoder drifted — wrong field ordering,
   wrong padding, wrong domain separator — the recovery would fail
   and the mock server would 400.

2. **The audit log is language-neutral.** Every `payment.signed` /
   `payment.settled` row carries identical structural fields whether
   the caller was Python, TypeScript, or the CLI. Downstream auditors
   can reconstruct payment history without knowing which SDK wrote
   any given row.

3. **The wire-shape contract is real on the network.** Unlike the
   in-process `httptest` mock in `internal/payment/payment_test.go`,
   this smoke goes through real HTTP framing, real Base64 envelopes
   on real headers, and real Unix-socket JSON-RPC dispatch through
   the daemon.

## Tearing down

```sh
# Kill the mock server (Ctrl-C in its terminal, or):
pkill -f x402-mock-server

# Kill the daemon:
pkill -f 'daimond serve'

# Remove the temp DAIMON_HOME if you used one:
rm -rf "$DAIMON_HOME"   # only if it's a test/throwaway dir
```

## Configuration overrides

Both scripts honor two env vars for ad-hoc parameter sweeps:

- `X402_URL` — override the target URL (default `http://127.0.0.1:8402/r`)
- `X402_CEILING_SMALLEST` — override the ceiling in USDC smallest units
  (default `100000`, which is $0.10)

For testing the ceiling-rejection path, run the mock server with
`-amount 999999999` and watch the smoke scripts surface
`CodePaymentCeiling` (`-32006`) cleanly through `RPCError`.
