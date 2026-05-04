# Daimon demo

A 90-second tour of the v0.1 surface: identity provisioning, encrypted
long-term memory, three provider adapters, conversational REPL with
multi-turn history that survives provider switches.

## Watch

If `demo.cast` is in this directory, play it locally:

```
brew install asciinema     # one-time
asciinema play demo.cast
```

Or, if uploaded to asciinema.org, the README at the repo root embeds the
player directly.

A GIF version (`demo.gif`) is provided for environments that can't run
asciinema. The GIF is a re-render of `demo.cast`, not a separate recording.

## Re-record

The script is in [`script.md`](script.md) — every command is there
verbatim with expected output, narration, and pacing notes. Total runtime
~90 seconds at typing pace.

Pre-flight from the repo root:

```
make build                                    # bin/daimon, bin/daimond
pkill -f bin/daimond                          # clear any stale daemon
rm -rf /tmp/daimon-demo                       # fresh state
export DAIMON_HOME=/tmp/daimon-demo
export DAIMOND_BIN=$(pwd)/bin/daimond
export PATH=$(pwd)/bin:$PATH
curl -sS http://localhost:11434/api/tags      # confirm Ollama is up
clear
asciinema rec demo/demo.cast \
  --title "Daimon v0.1 — sovereign agent in 90 seconds" \
  --idle-time-limit 1.5 \
  --command "$SHELL -l"
```

Then follow [`script.md`](script.md) command-for-command. End the
recording with Ctrl+D or `exit`.

## What's in scope for v0.1

- **All three providers**, contingent on keys: `ANTHROPIC_API_KEY` and
  `OPENAI_API_KEY` set in the recording shell, plus `ollama serve`
  running locally with at least `llama3.2:latest` pulled.
- **Memory primitives**: `write`, `list`, `search`. The substring
  fallback works without any embedder; pulling `nomic-embed-text` enables
  semantic search.
- **Chat REPL** with multi-turn JSONL history at
  `$DAIMON_HOME/chat-sessions/<name>.jsonl`, persisted across CLI
  invocations.
- **Cross-provider continuity**: switching `--provider` between turns
  threads the prior assistant content into the next provider's
  `messages[]`, no re-prompt.

## What's deferred to v0.1.1

- **Streaming** in the REPL. Today the response lands as one block.
  v0.1.1 lands a `daimon.provider.stream` RPC and a `--stream` flag.
  Cosmetic — the demo script reads identically with or without it.
- **Voiceover video.** This directory ships an asciicast first because
  it can be re-recorded in 90 s after every v0.1.x change. A 2-minute
  voice-narrated video is the v0.1.1 follow-up; the same `script.md`
  becomes its narration.
- **The `inject_context` retrieval beat.** When `nomic-embed-text` is
  pulled and the daemon is restarted, `daimon chat` defaults to
  injecting matched memories into every turn (announced on stderr).
  v0.1's primary demo narrative carries on conversational history alone,
  but a v0.1.1 cut can highlight the SPEC §11 retrieval path.

## What's in this directory

| File | Purpose |
|---|---|
| `script.md` | The line-by-line script with narration and pacing notes. The source of truth for both the asciicast and any future video. |
| `demo.cast` | The asciinema recording itself, when present. JSON, one event per line — editable post-recording for trimming. |
| `demo.gif` | Optional GIF re-render via `agg`. Embedded in the root README for environments without asciinema. |
| `README.md` | This file. |

## Recording philosophy

Single take preferred. Typos are texture, pauses are pacing. Re-record
from scratch rather than editing a marginal take — the script is short
enough that a fresh pass is cheaper than fixing a flawed one.

The asciicast is the v0.1 demo artifact. The same script supports a
voice-narrated video later without re-architecting anything.
