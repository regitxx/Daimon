"""Daimon — Python SDK streaming example.

Streams a short prompt through the local Ollama backend over a running
daimon, prints inter-delta timing, the terminal envelope's usage fields,
and an activity.verify summary. The sibling typescript_stream.mjs does
the same thing against the same daemon; together they demonstrate that
both SDKs are wire-compatible with the canonical
daimon.provider.stream surface.

Prerequisites:

  1. A daimon is running and unlocked. From a checkout of the repo:
       make build
       daimon init    (once — choose a password)
       daimon unlock  (auto-spawns daimond)

  2. Ollama is up locally with llama3.2:latest pulled. From a shell:
       ollama pull llama3.2:latest
       curl -fsS http://127.0.0.1:11434/api/tags | head -c 200

  3. The Python SDK is installed:
       pip install -e sdk/python

Run:

  python examples/streaming/python_stream.py
"""

from __future__ import annotations

import sys
import time

from daimon import Client, DaemonNotRunning


def main() -> int:
    try:
        client = Client()
    except DaemonNotRunning as e:
        print(f"py: daemon not running — {e}", file=sys.stderr)
        return 1

    me = client.identity.get()
    print(f"py: DID = {me['did']}")

    stream = client.provider.stream(
        provider="ollama",
        model="llama3.2:latest",
        messages=[
            {
                "role": "user",
                "content": "Reply with exactly: hello from python",
            }
        ],
    )

    t_open = time.monotonic()
    timings: list[float] = []
    deltas: list[str] = []
    for delta in stream:
        timings.append(time.monotonic() - t_open)
        deltas.append(delta)
        sys.stdout.write(delta)
        sys.stdout.flush()
    print()

    if not timings:
        # Fallback path: a non-streaming provider (Claude / OpenAI /
        # LM Studio) returns the full content in the terminal envelope
        # with zero deltas. The SDK surfaces this exactly the same way.
        print("py: stream emitted 0 deltas (terminal-only fallback)")
    else:
        gaps = [(b - a) * 1000 for a, b in zip(timings, timings[1:])]
        mean_gap = sum(gaps) / len(gaps) if gaps else 0
        print(
            f"py: {len(deltas)} deltas — first {timings[0] * 1000:.1f}ms, "
            f"last {timings[-1] * 1000:.1f}ms, mean inter-gap {mean_gap:.2f}ms"
        )

    env = stream.final
    if env is None:
        print("py: WARN — no terminal envelope")
    else:
        resp = env["response"]
        print(
            f"py: model={resp['model']} stop={resp['stop_reason']} "
            f"usage={resp['usage']}"
        )

    summary = client.activity.verify()
    print(f"py: activity.verify -> {summary}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
