# Cross-language streaming example

Two minimal scripts — one Python, one TypeScript — that stream a short
prompt through Ollama via a running daimon. They demonstrate the
canonical `daimon.provider.stream` surface in both SDKs and prove that
the wire shape is identical: both produce a sequence of delta strings
followed by a terminal envelope, both contribute one
`provider.invoke streamed=true` row to the same signed audit chain.

The SDK unit suites have full coverage of the streaming wire protocol
against a stub daemon (46 pytest + 47 vitest cases). These examples
are the user-facing reference for what `client.provider.stream(...)`
actually feels like against a real daemon.

## Prerequisites

1. **A daimon is running and unlocked.** From a checkout of the repo:
   ```
   make build
   ./bin/daimon init      # once — choose a password
   ./bin/daimon unlock    # auto-spawns daimond
   ```

2. **Ollama is up locally with `llama3.2:latest` pulled:**
   ```
   ollama pull llama3.2:latest
   curl -fsS http://127.0.0.1:11434/api/tags | head -c 200
   ```

3. **The SDKs are built / installed:**
   ```
   pip install -e sdk/python
   (cd sdk/typescript && npm install && npm run build)
   ```

## Run

```
python   examples/streaming/python_stream.py
node     examples/streaming/typescript_stream.mjs
```

Each script:

- Streams a short prompt through `ollama/llama3.2:latest`, printing
  deltas as they arrive (so you can see the token-by-token rendering).
- Reports first-delta latency, last-delta latency, and mean inter-delta
  gap.
- Prints the terminal envelope's `model`, `stop_reason`, and `usage`.
- Calls `activity.verify()` to confirm the audit chain is intact
  including the row this stream just created.

## What a healthy run looks like

```
$ python examples/streaming/python_stream.py
py: DID = did:key:z6Mk...
hello from python
py: 3 deltas — first 1398.8ms, last 1420.2ms, mean inter-gap 10.72ms
py: model=llama3.2:latest stop=end_turn usage={'input_tokens': 32, 'output_tokens': 4}
py: activity.verify -> {'verified': 2, 'ok': True}

$ node examples/streaming/typescript_stream.mjs
ts: DID = did:key:z6Mk...
hello from typescript
ts: 4 deltas — first 213.0ms, last 239.5ms, mean inter-gap 8.83ms
ts: model=llama3.2:latest stop=end_turn usage={"input_tokens":33,"output_tokens":5}
ts: activity.verify -> {"verified":4,"ok":true}

$ ./bin/daimon activity verify
verified 5 entries — chain ok
```

The `verified` count grows monotonically: Python sees the genesis row +
its own `provider.invoke`; TypeScript sees those plus Python's
`activity.verified` row plus its own `provider.invoke`; the CLI sees all
five. Both SDK calls and the CLI's verify walk the same chain under one
DID and agree on every hash and Ed25519 signature.

## Fallback providers (Claude, OpenAI, LM Studio)

Providers without a native streaming adapter on the daemon side fall
back to a synchronous invoke wrapped in a single terminal frame — the
SDKs see **zero deltas** and the full content lands on `stream.final`.
Both examples handle this case explicitly with a "0 deltas
(terminal-only fallback)" message. You can demonstrate it by swapping
the `provider` field to `anthropic` or `openai` (with the relevant API
key configured via `daimon provider list`).

## Inject context (SPEC §11)

To exercise the memory-injection path on the streaming side, pass an
`inject_context` payload alongside the stream call. The daemon performs
the same retrieval as on `daimon.provider.invoke` and surfaces the
matched memory IDs on the terminal envelope's `injected_memory_ids`
field. Example:

```python
stream = client.provider.stream(
    provider="ollama",
    model="llama3.2:latest",
    messages=[{"role": "user", "content": "what did I tell you yesterday?"}],
    inject_context={"query": "yesterday", "max_tokens": 256, "kinds": ["fact"]},
)
```

```ts
const stream = await client.provider.stream({
  provider: "ollama",
  model: "llama3.2:latest",
  messages: [{ role: "user", content: "what did I tell you yesterday?" }],
  inject_context: { query: "yesterday", max_tokens: 256, kinds: ["fact"] },
});
```

The matched IDs are durable on the audit row's `injected_memory_ids`
field as well, so a downstream auditor can reconstruct what context any
streamed response was conditioned on.
