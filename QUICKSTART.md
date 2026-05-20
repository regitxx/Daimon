# Daimon — Quickstart

> From zero to a daimon that holds memory + identity + a wallet + just paid for an
> x402-protected resource. ~30 minutes of clock time, mostly waiting for builds.

This walkthrough takes you through the full v0.1 + v0.2 surface end-to-end against
your own machine. By the end you'll have:

- A locally-encrypted identity (`did:key:…`) with a hash-chained audit log
- Persistent memory you can search by meaning (cosine over local embeddings)
- A streaming round-trip against a local LLM (Ollama or LM Studio)
- A BIP-39/BIP-32 HD wallet on Base, MetaMask-compatible
- A paid-resource fetch through the cross-language x402 mock server, with the
  cryptographic signature verified by the mock the same way a real x402
  facilitator would verify it on-chain
- Every step above logged into the same signed activity chain, walkable
  end-to-end

The full SPEC is at [`SPEC.md`](./SPEC.md). The per-session decision log is at
[`JOURNAL.md`](./JOURNAL.md). Use this file when you want to *do* something
rather than read about it.

---

## Prerequisites

- **Go 1.22+** to build from source (`go version` should say 1.22 or later)
- **Python 3.10+** OR **Node 18+** for one of the SDKs (optional — the CLI on
  its own covers most of the walkthrough)
- **Ollama** running locally (`ollama serve` + at least one pulled model like
  `ollama pull llama3.2`) for the streaming step. If you don't want to install
  Ollama, skip step 4 — everything else works without it.

Daimon stores its state in `$DAIMON_HOME` (default: `~/Library/Application
Support/daimon` on macOS, `~/.config/daimon` on Linux). Set the env var to
point somewhere else if you want to keep this walkthrough's state isolated
from a real install:

```sh
export DAIMON_HOME=/tmp/daimon-quickstart
mkdir -p "$DAIMON_HOME"
```

## Install

### Option A — one-line installer (recommended, no Go needed)

```sh
curl -fsSL https://raw.githubusercontent.com/regitxx/Daimon/main/install.sh | sh
```

The script resolves the latest GitHub Release, detects your platform, downloads the matching tarball, verifies its SHA-256 against the published `checksums.txt`, and drops `daimon` + `daimond` into `/usr/local/bin` (or `$HOME/.local/bin` if that's not writable). Useful env vars before piping into `sh`:

- `DAIMON_INSTALL_PREFIX=…` — install somewhere else (e.g. `$HOME/bin`)
- `DAIMON_INSTALL_TAG=v0.2.0-dev.3` — pin to a specific release (the installer otherwise tracks the latest non-pre-release; for pre-releases like dev.3, you currently need to pin until v0.2.0 GA lands)
- `DAIMON_INCLUDE_MOCK=1` — also install `x402-mock-server`

The script is at [`install.sh`](./install.sh) — review it before running if `curl | sh` makes you nervous (it makes me nervous too; the manual path below is the same workflow, just step-by-step).

### Option B — manual tarball download (no Go needed)

Download the tarball for your platform from the [latest release](https://github.com/regitxx/Daimon/releases/latest), extract it, drop `daimon` + `daimond` somewhere on `PATH`:

```sh
# Replace darwin-arm64 with darwin-amd64 / linux-arm64 / linux-amd64 to match.
TAG=v0.2.0-dev.3
PLAT=darwin-arm64
curl -L -o /tmp/daimon.tar.gz \
  "https://github.com/regitxx/Daimon/releases/download/${TAG}/daimon-${TAG}-${PLAT}.tar.gz"
tar -C /tmp -xzf /tmp/daimon.tar.gz
sudo install /tmp/daimon-${TAG}-${PLAT}/{daimon,daimond} /usr/local/bin/
daimon --help                              # daimon v0.2.0-dev.3 — Daimon Protocol CLI
```

Verify the download against the published checksums (optional but recommended for any binary you didn't build yourself):

```sh
curl -LO "https://github.com/regitxx/Daimon/releases/download/${TAG}/checksums.txt"
shasum -a 256 -c <(grep "daimon-${TAG}-${PLAT}.tar.gz" checksums.txt)
# daimon-v0.2.0-dev.3-darwin-arm64.tar.gz: OK
```

Binaries are statically linked (no libc dependency on Linux), stripped, and ~11 MB per tarball. No Windows build — the daemon listens on a Unix socket (SPEC §3); Windows users can build from source under WSL2 today.

### Option C — build from source

Requires **Go 1.22+** (`go version`):

```sh
git clone https://github.com/regitxx/Daimon.git
cd Daimon
make build
# Produces bin/daimond (~15 MB) and bin/daimon (~4.6 MB).
# The Makefile injects `git describe --tags --dirty --always` as the
# binary's --version, so locally-built binaries report e.g.
# "v0.2.0-dev.3-2-g3fa5e8d-dirty" if you have uncommitted changes.
export PATH="$PWD/bin:$PATH"
```

## Step 1: Provision your identity

```sh
daimon init
# Choose a password: (hidden input)
# Confirm password:  (hidden input)
# Identity provisioned.
#   DID:      did:key:z6Mk…
#   Keystore: /tmp/daimon-quickstart/identity.keystore (mode 0600)
#   Genesis:  /tmp/daimon-quickstart/activity.log (1 entry, kind=daimon.created)
```

What just happened:
- Fresh Ed25519 keypair generated locally; nothing left your machine.
- Private key encrypted at rest under your password via Argon2id (64 MiB,
  3 iterations) + AES-256-GCM.
- A genesis row was written to your activity log — the daimon's birth
  certificate. Every subsequent action will hash-chain off this.

**Lose this password and the identity is unrecoverable.** Save it somewhere
durable. If you want to also save the encrypted file itself,
`$DAIMON_HOME/identity.keystore` is a 4 KiB blob you can rsync between
machines — you'll need both the file AND the password.

## Step 2: Unlock the daemon

```sh
daimon unlock
# Password: (hidden input)
```

`unlock` auto-spawns `daimond serve` as a detached background process (own
session, stdout/stderr to `$DAIMON_HOME/daimon.log`), polls the socket until
it's listening, then sends `daimon.identity.unlock {password}` to load the
keystore into memory.

If this is the first unlock, you'll also see a one-time banner with your
24-word BIP-39 wallet mnemonic:

```
═══════════════════════════════════════════════════════════════════
Wallet keystore initialised — back up this recovery phrase NOW.
───────────────────────────────────────────────────────────────────
Write these 24 words down on paper, in order. This is the only
copy. The daemon does NOT keep a separate backup. If you lose
both this phrase AND the wallet.keystore file, every wallet you
ever derive from it is permanently inaccessible — including any
funds those wallets hold.
───────────────────────────────────────────────────────────────────
   1. bright      2. welcome     3. spray       4. hello
   …
═══════════════════════════════════════════════════════════════════
```

**Write these 24 words down NOW** — they will not appear again unless you
explicitly ask via `daimon wallet show-mnemonic` (with the password). The
auto-create only happens once.

Confirm the daemon is up:

```sh
daimon identity get
# DID:          did:key:z6Mk…
# Public key:   z6Mk…
# DID methods:  [did:key]
```

(Add `--json` for tooling-friendly output.)

## Step 3: Write a memory

Daimon's memory is per-row signed + encrypted + indexable by meaning:

```sh
daimon memory write --kind fact "the sky is blue"
# 01KS…   (the row's ULID — pipe to xargs if you want to chain it)

daimon memory write --kind preference "I prefer concise responses"
daimon memory write --kind observation "user installed daimon at 16:00 UTC"

daimon memory list
# ID                          KIND        CREATED                    CONTENT
# 01KS…(observation row)      observation 2026-05-19T16:00:00+00:00  user installed …
# 01KS…(preference row)       preference  2026-05-19T16:00:00+00:00  I prefer concise …
# 01KS…(fact row)             fact        2026-05-19T16:00:00+00:00  the sky is blue
```

The four valid kinds are `fact | preference | task | observation`. Try
sending something else and the daemon will reject with `CodeInvalidParams`
and the canonical kind list — both the CLI and SDK stubs hold this list
in lockstep (see `internal/memory/memory.go:41`).

For meaning-based retrieval (cosine over embeddings) use `memory search`
— but it needs a real embedder running. Daimon defaults to Ollama's
`nomic-embed-text` model; if Ollama isn't running, embeddings degrade to
zero vectors and `search` returns no matches:

```sh
ollama pull nomic-embed-text                # one-time, ~270 MB
daimon memory search "what color is the sky"
# ID                          KIND  CREATED                    SCORE  CONTENT
# 01KS…                       fact  2026-05-19T16:00:00+00:00  0.711  the sky is blue
```

Without Ollama, stick with `memory list` (which doesn't need an
embedder). All other memory operations (`write`, `read`, `delete`,
`export`, `import`) work without one.

Memory rows are AES-256-GCM-encrypted at the column level under an HKDF
subkey derived from your identity — no SQLCipher, no CGO. The on-disk
`memory.db` is unreadable without the unlocked identity in memory.

## Step 4: Stream from a local LLM (optional, needs Ollama)

```sh
# Make sure Ollama is running and a model is pulled:
ollama serve &
ollama pull llama3.2

# Now stream:
daimon chat --provider ollama --model llama3.2:latest --stream --once "count to 3"
# 1, 2, 3
# [inject_context: query="count to 3" matched=0]
```

What just happened:
- The daimon dispatched to Ollama's `/api/chat` endpoint via the
  `internal/provider/ollama` adapter.
- Deltas streamed back as JSON-RPC notifications on the same Unix socket
  (no pipelining; one stream per connection).
- The CLI also asked the daimon to retrieve context-relevant memories — with
  the empty "what about counting?" search it matched none, so nothing was
  injected. Try `daimon chat --inject-context --stream --once "tell me a
  fact about colors"` and watch the inject_context banner light up.
- The whole turn was logged as a `provider.invoke` row in the activity log
  with `streamed=true` and full token usage.

For Claude / OpenAI / LM Studio, set `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`
or have LM Studio running locally, then swap `--provider`.

## Step 5: Hold money

```sh
daimon wallet create --chain evm:base
# Wallet created.
#   Chain:    evm:base
#   Address:  0x9858EfFD232B4033E47d90003D41EC34EcaEda94
#   Path:     m/44'/60'/0'/0/0
#   ID:       01K…
```

If you wrote down your 24-word mnemonic in step 2, the address above is the
exact same one MetaMask / Phantom / Rabby would show for that seed at the
same derivation path. **The daimon is now MetaMask-compatible**: you can
import the mnemonic into any standard EVM wallet and see identical balances
and history.

The same Ed25519 identity that signed your memory rows in step 3 is now also
auditing wallet operations — `wallet.created` is a typed activity-log kind
that lands in the same hash chain.

## Step 6: Verify the seed (catch typos BEFORE you fund anything)

If you ran `daimon init` and let `unlock` auto-generate your seed, you can
re-display the mnemonic any time to verify you wrote it down correctly:

```sh
daimon wallet show-mnemonic
# Password: (hidden input — re-runs the full Argon2id KDF + AES-GCM decrypt
# against the on-disk file, NOT against the in-memory unlocked state)
# ═══════════════════════════════════════════════════════════════════
# Your wallet mnemonic — keep this private.
#  …
```

Wrong password surfaces as `RPCError` with the typed code `-32008`
(`CodeWrongPassword`), distinct from `-32001` (`CodeIdentityLocked`) — your
CLI / SDK can branch on it without rewriting the message as "run daimon
unlock first" when really the user just mistyped.

The companion verb for importing an existing seed is `daimon wallet
recover` (offline, before `daimon unlock`):

```sh
# Stop the daemon first if it's running. Then with $DAIMON_HOME pointing
# at a fresh dir (no wallet.keystore yet):
daimon wallet recover
# Recovery phrase: (hidden input, paste your 12 or 24 words)
# Accepted 12-word phrase (BIP-39 checksum valid).
# Choose a password: …
# Confirm password: …
# Password matches identity keystore — recovery + unlock will share it.
# Wallet keystore written.
#   Verify this is the correct seed:
#     evm:base address at m/44'/60'/0'/0/0
#       0x9858EfFD232B4033E47d90003D41EC34EcaEda94
# This should match what your external wallet shows for the same seed
# at the same path.
```

If the displayed address doesn't match what your external wallet shows,
delete the keystore and re-run with the corrected phrase. **This is the
moment to catch a typo** — once the daemon unlocks against this seed and
you start creating wallets, fixing it gets harder.

Both verbs are formalised in [SPEC §14.6](./SPEC.md#146-backup-re-display-and-seed-import).

## Step 7: Pay an x402-protected resource

The repo ships a mock x402 server that cryptographically verifies the
EIP-3009 signature on each retry — the same property a real facilitator
checks before settling on-chain. So you don't need testnet USDC to verify
your daimon signs correctly:

```sh
# The mock isn't built by `make build` (only daimond + daimon are).
# Build it directly:
go build -o bin/x402-mock-server ./cmd/x402-mock-server

# In one shell, start it (default port 8402):
./bin/x402-mock-server &

# In another shell, pay the mock's /r endpoint. Flags come BEFORE the URL
# (Go's flag package stops parsing at the first positional argument):
daimon payment pay --ceiling-usd 0.10 http://127.0.0.1:8402/r
# HTTP 200
# Content-Type: text/plain
# Payment: success=true tx=0xabab… network=base payer=0x9858EfFD…
# paid resource served to 0x9858EfFD… (network=base, amount=100)
```

(The resource body — `paid resource served to …` — goes to stdout. The
HTTP status, content-type, and parsed `PAYMENT-RESPONSE` go to stderr so
you can pipe the body cleanly into scripts.)

Or skip the manual orchestration and run the full cross-language smoke
in one command:

```sh
bash examples/x402-smoke/run.sh
# Builds binaries + builds the mock server + spawns a temp daimon +
# pays through both Python AND TypeScript SDKs + verifies the audit
# chain + tests the negative ceiling-rejection path. ~30 seconds.
```

The daemon:
1. Sent a GET, received HTTP 402 + `PAYMENT-REQUIRED` header.
2. Parsed the requirement, matched it against your `evm:base` wallet.
3. Enforced your `$0.10` ceiling **before** signing (over-budget 402s
   never produce a signature on the wire).
4. Built the EIP-3009 `transferWithAuthorization` digest, signed with
   your wallet's secp256k1 key, returned the `[r || s || v]` form
   x402 verifiers expect.
5. Re-sent the request with `PAYMENT-SIGNATURE` header. Mock verified
   the signature recovers to your wallet address ✓.
6. Decoded the response body and the `PAYMENT-RESPONSE` header.

The full audit trail lands in `activity.log` as `payment.signed` +
`payment.settled` rows, both hash-chained with everything before.

To try the ceiling-rejection negative path:

```sh
daimon payment pay --ceiling-usd 0.0001 http://127.0.0.1:8402/r
# Error: payment exceeds local ceiling (rpc code -32006)
```

Zero new `payment.signed` rows are written — the daimon refused
**before** signing, so no over-budget authorization ever existed on the
wire.

## Step 8: Walk the audit chain

```sh
daimon activity verify
# verified 11 entries — chain ok

daimon activity query --limit 20
# TIME                       KIND              ID    SUMMARY
# 2026-05-19T16:00:00+00:00  daimon.created    01K…  v=v0.2.0-dev.2 did=…
# 2026-05-19T16:01:00+00:00  memory.write      01L…  kind=fact id=01K…
# 2026-05-19T16:03:00+00:00  provider.invoke   01M…  provider=ollama deltas=5 …
# 2026-05-19T16:04:00+00:00  wallet.created    01N…  chain=evm:base address=…
# 2026-05-19T16:05:00+00:00  payment.signed    01O…  network=base amount=100 …
# 2026-05-19T16:05:00+00:00  payment.settled   01P…  tx=0xabab… payer=…
# …
```

Every row is Ed25519-signed under your identity key and BLAKE3-chained
to the previous row. `verify` walks the whole chain end-to-end and
self-appends an `activity.verified` row on success — so the act of
verification is itself in the chain.

## Optional: drive everything through the SDKs

The Python or TypeScript SDK exposes the same verbs you've been calling
via the CLI:

```python
# pip install --pre daimon-protocol  (v0.2.0.dev2)
from daimon import Client

client = Client()                                       # auto-resolves $DAIMON_HOME
print(client.identity.get())                            # {"did": "did:key:…"}

client.memory.write(kind="fact", content="cats nap a lot")
print(client.memory.search("sleeping animals"))

w = client.wallet.create(chain="evm:base")              # idempotent if exists
predicted = client.wallet.derive(chain="evm:base", index=0)
assert w["address"] == predicted["address"]             # derive() doesn't persist

resp = client.payment.pay(
    url="http://127.0.0.1:8402/r",
    ceiling_smallest_unit=100_000,                      # $0.10 USDC
)
print(resp["status_code"], resp["body"])
```

```ts
// npm install @daimon-protocol/sdk@dev  (v0.2.0-dev.2)
import { Client } from "@daimon-protocol/sdk";

const client = new Client();
console.log(await client.identity.get());

await client.memory.write({ kind: "fact", content: "cats nap a lot" });
console.log(await client.memory.search({ query: "sleeping animals" }));

const w = await client.wallet.create({ chain: "evm:base" });
const predicted = await client.wallet.derive({ chain: "evm:base", index: 0 });
console.assert(w.address === predicted.address);

const resp = await client.payment.pay({
  url: "http://127.0.0.1:8402/r",
  ceilingSmallestUnit: 100_000n,
});
console.log(resp.statusCode, new TextDecoder().decode(resp.body));
```

Both SDKs share the wire shape byte-for-byte with the CLI — see
[`examples/x402-smoke`](./examples/x402-smoke) for a runnable cross-language
demo that pays the same mock server through both SDKs against one daimon and
asserts the audit chain has both pairs of payment rows.

## Common issues

**"daemon not running — run `daimon unlock` first"** — most likely the
auto-spawn failed. Check `$DAIMON_HOME/daimon.log` for the daemon's stderr.

**"daemon is locked — run `daimon unlock` first"** — the daemon is up but
hasn't been unlocked since process start. Idempotent: a second `unlock`
against an already-unlocked daemon returns the same DID.

**"wrong password"** (code -32008) — your re-confirmation password to
`wallet show-mnemonic` didn't decrypt the keystore. **This is NOT "the
daemon is locked"**; the daemon IS unlocked, you just mistyped at the
re-verification step. Try `show-mnemonic` again with the correct password.

**`daimon doctor` reports "rpc surface: DISABLED — wallet keystore not
loaded into the running daemon"** — your `wallet.keystore` exists on disk
but couldn't be decrypted at unlock time, typically because the wallet
password differs from the identity password. The doctor output points at
the remediation:

```sh
# Either: re-init from scratch (DESTROYS all current state):
daimon init --force
# Or: restore wallet.keystore from a backup encrypted under the
# identity password.
```

**`daimon wallet recover` says "password does not match the existing
identity keystore"** — same root cause from the input side. The recover
flow pre-flights the password against `identity.keystore` so you catch
the mismatch BEFORE writing the wallet keystore.

## Where to go next

- [`SPEC.md`](./SPEC.md) — the protocol document. §6.1 is the canonical
  RPC method list; §14 covers the wallet primitive; §15 covers x402.
- [`sdk/python/README.md`](./sdk/python/README.md) and
  [`sdk/typescript/README.md`](./sdk/typescript/README.md) — full SDK
  reference with all verbs and typed error codes.
- [`examples/streaming`](./examples/streaming) — cross-language streaming
  reference; both SDKs round-trip token deltas through one daemon.
- [`examples/x402-smoke`](./examples/x402-smoke) — cross-language x402
  reference; both SDKs pay the same mock server through one daimon.
- [`CHECKPOINT.md`](./CHECKPOINT.md) — current state of the project,
  read at conversation start.
- [`JOURNAL.md`](./JOURNAL.md) — append-only chronological log of
  decisions and discoveries.

## License

[Apache 2.0](./LICENSE). The protocol is the public good; anyone can
implement it.
