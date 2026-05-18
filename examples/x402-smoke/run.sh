#!/usr/bin/env bash
# examples/x402-smoke/run.sh — orchestrate the cross-language x402 live smoke.
#
# What this does, in order:
#   1. Build the daimon binaries + x402-mock-server (no-op if up-to-date).
#   2. Install the Python SDK in editable mode if not already importable.
#   3. Build the TypeScript SDK dist if not already present.
#   4. Allocate a fresh DAIMON_HOME under /tmp.
#   5. Init + unlock the daimon (password piped non-interactively).
#   6. Create an evm:base wallet.
#   7. Start the x402-mock-server on a random localhost port.
#   8. Run the Python smoke against the mock server.
#   9. Run the TypeScript smoke against the same mock server.
#  10. Run `daimon activity verify` as the third independent observer.
#  11. Assert the chain has ≥ 7 entries with the expected
#      payment.signed / payment.settled rows (one pair per SDK).
#  12. Tear down: kill the mock server, kill daimond, leave the
#      DAIMON_HOME on disk for post-mortem (caller can rm if desired).
#
# Exits 0 on success, non-zero on any step failure. Designed to be
# CI-runnable AND human-runnable from a clean repo checkout.
#
# Environment overrides:
#   PORT      — mock server listen port (default 18402)
#   PASSWORD  — daimon keystore password (default "x402-smoke")
#
# Requires Go, Python 3.10+, Node 18+, all already on PATH.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

PORT="${PORT:-18402}"
PASSWORD="${PASSWORD:-x402-smoke}"
PYTHON="${PYTHON:-python3}"

# Find a working python3 — different CI runners install it under different
# names.
for cand in python3.13 python3.12 python3.11 python3.10 python3 python; do
  if command -v "$cand" >/dev/null 2>&1; then
    PYTHON="$cand"
    break
  fi
done

log() { printf '\033[1;36m[smoke]\033[0m %s\n' "$*" >&2; }
ok()  { printf '\033[1;32m[ ok ]\033[0m %s\n' "$*" >&2; }
err() { printf '\033[1;31m[fail]\033[0m %s\n' "$*" >&2; }

cleanup() {
  local rc=$?
  if [[ -n "${MOCK_PID:-}" ]]; then
    kill -KILL "$MOCK_PID" 2>/dev/null || true
  fi
  if [[ -n "${DAIMON_HOME:-}" && -S "$DAIMON_HOME/daimon.sock" ]]; then
    pkill -KILL -f "daimond serve.*$DAIMON_HOME" 2>/dev/null || true
  fi
  if [[ -n "${MOCK_LOG:-}" ]]; then
    log "mock server log tail:"
    tail -20 "$MOCK_LOG" >&2 || true
  fi
  if [[ $rc -eq 0 ]]; then
    ok "smoke complete; DAIMON_HOME left at ${DAIMON_HOME:-(unset)} for inspection"
  else
    err "smoke FAILED with rc=$rc; DAIMON_HOME at ${DAIMON_HOME:-(unset)}"
  fi
  exit "$rc"
}
trap cleanup EXIT

# --- step 1: build binaries -------------------------------------------------

log "building daimon + daimond + x402-mock-server"
make build >/dev/null
go build -o bin/x402-mock-server ./cmd/x402-mock-server
ok "binaries built"

# --- step 2: install Python SDK ---------------------------------------------

if ! "$PYTHON" -c 'import daimon' >/dev/null 2>&1; then
  log "installing Python SDK in editable mode"
  "$PYTHON" -m pip install --quiet -e sdk/python
  ok "Python SDK installed"
else
  log "Python SDK already importable, skipping install"
fi

# --- step 3: build TS SDK dist ---------------------------------------------

if [[ ! -f sdk/typescript/dist/index.js ]]; then
  log "building TypeScript SDK dist"
  (cd sdk/typescript && npm install --silent && npm run build --silent)
  ok "TS SDK built"
else
  log "TS SDK dist already present, skipping build"
fi

# --- step 4: allocate DAIMON_HOME ------------------------------------------

DAIMON_HOME="$(mktemp -d "/tmp/dt-x402-smoke-XXXXXX")"
export DAIMON_HOME
log "DAIMON_HOME=$DAIMON_HOME"

# --- step 5+6: init + unlock + wallet --------------------------------------

log "daimon init (password piped)"
printf '%s\n%s\n' "$PASSWORD" "$PASSWORD" | bin/daimon init >/dev/null

log "daimon unlock"
printf '%s\n' "$PASSWORD" | bin/daimon unlock >/dev/null
# Confirm unlock succeeded by querying identity.
DID=$(bin/daimon identity get 2>/dev/null | awk -F': *' '/^DID/ {print $2; exit}')
[[ -n "$DID" ]] || { err "unlock failed — no DID surfaced"; exit 1; }
log "DID=$DID"

log "creating evm:base wallet"
WALLET_ADDR=$(bin/daimon wallet create --chain evm:base --json 2>/dev/null | "$PYTHON" -c 'import json,sys; print(json.load(sys.stdin)["address"])')
[[ -n "$WALLET_ADDR" ]] || { err "wallet.create did not surface an address"; exit 1; }
log "wallet=$WALLET_ADDR"

# --- step 7: start mock server ---------------------------------------------

MOCK_LOG="$(mktemp "/tmp/x402-mock-XXXXXXXX")"
bin/x402-mock-server -addr "127.0.0.1:$PORT" >"$MOCK_LOG" 2>&1 &
MOCK_PID=$!
disown

# Wait for the listener to be ready.
for i in 1 2 3 4 5 6 7 8 9 10; do
  if curl -sS -o /dev/null "http://127.0.0.1:$PORT/" 2>/dev/null; then
    break
  fi
  sleep 0.5
done
curl -sS -o /dev/null "http://127.0.0.1:$PORT/" || { err "mock server failed to start"; exit 1; }
log "mock server pid=$MOCK_PID port=$PORT"

# --- step 8: Python smoke --------------------------------------------------

log "running Python smoke"
X402_URL="http://127.0.0.1:$PORT/r" "$PYTHON" examples/x402-smoke/python_smoke.py

# --- step 9: TS smoke ------------------------------------------------------

log "running TypeScript smoke"
X402_URL="http://127.0.0.1:$PORT/r" node examples/x402-smoke/typescript_smoke.mjs

# --- step 10+11: CLI verify + chain assertion -----------------------------

log "running daimon activity verify"
VERIFY_OUT=$(bin/daimon activity verify 2>&1)
log "$VERIFY_OUT"

# Assert the chain ended up at ≥ 7 rows (genesis + wallet.created +
# 2*(payment.signed + payment.settled) + at-least-2-activity.verified).
VERIFIED=$(printf '%s\n' "$VERIFY_OUT" | awk '/^verified/ {print $2; exit}')
[[ -n "$VERIFIED" ]] || { err "could not parse verified count"; exit 1; }
if (( VERIFIED < 7 )); then
  err "expected ≥ 7 verified entries, got $VERIFIED"
  exit 1
fi
ok "chain verified ($VERIFIED entries)"

# Assert both payment.signed rows are present.
SIGNED_COUNT=$(bin/daimon activity query --kind payment.signed --json 2>/dev/null | "$PYTHON" -c 'import json,sys; print(len(json.load(sys.stdin)))')
SETTLED_COUNT=$(bin/daimon activity query --kind payment.settled --json 2>/dev/null | "$PYTHON" -c 'import json,sys; print(len(json.load(sys.stdin)))')
if [[ "$SIGNED_COUNT" != "2" ]]; then
  err "expected 2 payment.signed rows, got $SIGNED_COUNT"
  exit 1
fi
if [[ "$SETTLED_COUNT" != "2" ]]; then
  err "expected 2 payment.settled rows, got $SETTLED_COUNT"
  exit 1
fi
ok "audit log shape: $SIGNED_COUNT payment.signed + $SETTLED_COUNT payment.settled"

# Assert the payer field on both payment.settled rows matches the wallet.
bin/daimon activity query --kind payment.settled --json 2>/dev/null \
  | "$PYTHON" -c "
import json,sys
rows = json.load(sys.stdin)
for r in rows:
    payer = r['payload'].get('payer','')
    if payer.lower() != '$WALLET_ADDR'.lower():
        sys.exit(f'payment.settled.payer ({payer}) != wallet ({\"$WALLET_ADDR\"}) on row {r[\"id\"]}')
print(f'both payment.settled rows have payer={\"$WALLET_ADDR\"}')
"
ok "all payment.settled rows attribute to wallet $WALLET_ADDR"

# --- step 12: negative path — ceiling rejection ----------------------------
# Spin up a second mock server demanding more than the daimon's ceiling
# and confirm both SDKs surface CodePaymentCeiling (-32006) cleanly. This
# guards the "refuse to sign over-budget payments" path — without it, a
# misconfigured or malicious endpoint could drain the wallet by quoting
# enormous prices.

log "step 12: negative-path ceiling rejection"

NEG_PORT="$((PORT + 1))"
NEG_LOG="$(mktemp "/tmp/x402-mock-neg-XXXXXXXX")"
# 999_999_999 USDC smallest-units = $999.99 — way above any sane ceiling.
bin/x402-mock-server -addr "127.0.0.1:$NEG_PORT" -amount 999999999 >"$NEG_LOG" 2>&1 &
NEG_MOCK_PID=$!
disown
for i in 1 2 3 4 5 6 7 8 9 10; do
  if curl -sS -o /dev/null "http://127.0.0.1:$NEG_PORT/" 2>/dev/null; then
    break
  fi
  sleep 0.5
done
curl -sS -o /dev/null "http://127.0.0.1:$NEG_PORT/" || { err "negative mock server failed to start"; kill -KILL "$NEG_MOCK_PID" 2>/dev/null; exit 1; }
log "negative mock server pid=$NEG_MOCK_PID port=$NEG_PORT amount=999999999"

# We expect both smoke scripts to exit non-zero (rc=2 specifically per
# their own conventions — payment-rejection paths). Capture the stderr
# so we can grep for the typed -32006 code.

PY_NEG_LOG="$(mktemp "/tmp/x402-neg-py-XXXXXXXX")"
TS_NEG_LOG="$(mktemp "/tmp/x402-neg-ts-XXXXXXXX")"

set +e
X402_URL="http://127.0.0.1:$NEG_PORT/r" "$PYTHON" examples/x402-smoke/python_smoke.py >"$PY_NEG_LOG" 2>&1
PY_NEG_RC=$?
X402_URL="http://127.0.0.1:$NEG_PORT/r" node examples/x402-smoke/typescript_smoke.mjs >"$TS_NEG_LOG" 2>&1
TS_NEG_RC=$?
set -e

kill -KILL "$NEG_MOCK_PID" 2>/dev/null || true

if (( PY_NEG_RC == 0 )); then
  err "Python smoke should have failed against over-ceiling endpoint, got rc=0"
  cat "$PY_NEG_LOG" >&2
  exit 1
fi
if (( TS_NEG_RC == 0 )); then
  err "TypeScript smoke should have failed against over-ceiling endpoint, got rc=0"
  cat "$TS_NEG_LOG" >&2
  exit 1
fi

# Both smokes should have surfaced rpc code -32006 in their error output.
# Python's RPCError stringifies as "rpc error -32006: ..."; TS's RPCError
# carries the code field which the smoke prints as "rpc code -32006".
if ! grep -q -- '-32006' "$PY_NEG_LOG"; then
  err "Python rejection did not surface code -32006"
  cat "$PY_NEG_LOG" >&2
  exit 1
fi
if ! grep -q -- '-32006' "$TS_NEG_LOG"; then
  err "TypeScript rejection did not surface code -32006"
  cat "$TS_NEG_LOG" >&2
  exit 1
fi
ok "both SDKs surfaced CodePaymentCeiling (-32006) on over-ceiling 402"

# Confirm the audit log got one payment.failed row per SDK with reason
# "ceiling …". Pre-existing payment.failed count was 0 before this step.
FAILED_COUNT=$(bin/daimon activity query --kind payment.failed --json 2>/dev/null | "$PYTHON" -c 'import json,sys; print(len(json.load(sys.stdin)))')
if [[ "$FAILED_COUNT" != "2" ]]; then
  err "expected 2 payment.failed rows after ceiling tests, got $FAILED_COUNT"
  exit 1
fi
ok "audit log gained 2 payment.failed rows from ceiling-rejection path"

# And importantly: no NEW payment.signed rows were added. The daimon
# refused to sign because the ceiling fires BEFORE signing — that's the
# whole point of the ceiling.
SIGNED_COUNT_AFTER=$(bin/daimon activity query --kind payment.signed --json 2>/dev/null | "$PYTHON" -c 'import json,sys; print(len(json.load(sys.stdin)))')
if [[ "$SIGNED_COUNT_AFTER" != "$SIGNED_COUNT" ]]; then
  err "payment.signed count changed during ceiling-rejection ($SIGNED_COUNT -> $SIGNED_COUNT_AFTER); ceiling should fire BEFORE signing"
  exit 1
fi
ok "ceiling-rejection produced NO new payment.signed rows (correctly refused to sign)"
