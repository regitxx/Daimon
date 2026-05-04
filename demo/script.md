# Daimon v0.1 demo — script

> **What this is.** A line-by-line script for the v0.1 demo asciicast (or
> screencast). Every command is here exactly as it should be typed, with
> expected output, pacing notes, and a narration line that lands on top of
> the action when the recording later becomes a video with voiceover.
>
> **Target length.** ~90 seconds at typing pace for asciicast. ~2 minutes
> with narration on top. Single take preferred — typos and pauses are
> texture, not bugs.
>
> **What you need to record.** A working `bin/daimon` and `bin/daimond`
> (`make build`), a writable `$DAIMON_HOME` (the script uses
> `/tmp/daimon-demo`), and `ollama serve` running with at least
> `llama3.2:latest` pulled. Anthropic and OpenAI keys are optional — see
> §3 for how the cross-provider beat works on each.
>
> **Provider switch beat.** The pitch line is "switch Claude → OpenAI →
> Ollama mid-task with memory intact." If you have all three keys, Scene 5
> records that beat verbatim. If you only have Ollama, record Scene 5 with
> three Ollama turns at three different prompts (the wire-shape proof is
> identical) and tag the asciicast as "Ollama-only — cross-provider
> upgrade in v0.1.1".
>
> **Pre-recording checklist.**
>
> - `cd /Users/huckgod/Developer/network`
> - `make build` — confirm `bin/daimon` and `bin/daimond` are fresh.
> - `pkill -f bin/daimond` — kill any stale daemon.
> - `rm -rf /tmp/daimon-demo` — fresh state.
> - `export DAIMON_HOME=/tmp/daimon-demo`
> - `export DAIMOND_BIN=$(pwd)/bin/daimond`
> - `export PATH=$(pwd)/bin:$PATH` — so `daimon` resolves without `./bin/`.
> - `curl -sS http://localhost:11434/api/tags` — confirm Ollama is up.
> - Optional: `ollama pull nomic-embed-text` — enables semantic memory
>   retrieval. Without it, `inject_context` falls back to literal substring
>   match (still works for the keyword "Daimon"; degrades for natural-language
>   queries). The script does not depend on retrieval, so this is optional.
> - Terminal: ~100 cols × 30 rows, dark theme, monospace ≥14pt.
> - `clear` immediately before `asciinema rec`.

---

## Scene 1 — provision (10 s)

**Narration.** *"Every daimon starts with one command. `init` generates a
fresh Ed25519 identity, encrypts it under a password you choose, and
writes the keystore at mode 0600. Nothing leaves your machine."*

```
$ daimon init
```

Type the password twice when prompted (use something memorable for the
recording — e.g. `demo-pass-12345`). The terminal does not echo.

**Expected:**

```
Provisioning new daimon identity in /tmp/daimon-demo
The keystore will be encrypted under a password you supply.
There is no recovery — losing the password loses the identity.

Choose a password: Confirm password:
Identity provisioned.
  DID:      did:key:z6Mk…<your DID>
  Keystore: /tmp/daimon-demo/identity.keystore (mode 0600)

Next: run `daimon unlock` to start the daemon and load the identity.
```

**Pacing.** Pause one beat after the DID line — it is the most important
artifact in the recording.

---

## Scene 2 — unlock (10 s)

**Narration.** *"`unlock` auto-spawns the daemon, reads the password
once, and unlocks the keystore in memory. The daemon stays running until
you reboot."*

```
$ daimon unlock
```

Re-enter the password. Daemon spawns detached.

**Expected:**

```
Password:
Unlocked.
  DID: did:key:z6Mk…<same DID>
  Daemon: /tmp/daimon-demo/daimon.sock
```

```
$ daimon identity get
```

**Expected:**

```
DID:          did:key:z6Mk…<same DID>
Public key:   z6Mk…<same key>
DID methods:  [did:key]
```

**Pacing.** Identity-get is the post-unlock proof. One beat after.

---

## Scene 3 — memory (15 s)

**Narration.** *"The daimon holds your long-term memory: encrypted at
rest, AEAD-bound to your identity, queryable by semantic search or
substring fallback. Two writes."*

```
$ daimon memory write --kind fact "I'm Johannes, building Daimon — a protocol giving every human one sovereign agent for life."
```

**Expected:** one ULID alone on stdout, e.g. `01KQREGDS532PBG30M79ZDE168`.

```
$ daimon memory write --kind preference "I prefer terse answers — no preamble, no apology."
```

**Expected:** another ULID.

```
$ daimon memory list
```

**Expected:**

```
ID                          KIND        CREATED                    CONTENT
01KQRE…                     preference  2026-05-04T10:54:03+08:00  I prefer terse answers — no preamble, no apology.
01KQRE…                     fact        2026-05-04T10:54:03+08:00  I'm Johannes, building Daimon — a protocol giving every hum…
```

```
$ daimon memory search "Daimon"
```

**Expected:** the fact row, with `SCORE` column populated (1.000 on
substring match, cosine on a real embedder).

**Pacing.** Three writes, one list, one search. Don't pause between
writes — the rhythm matters more than any individual ULID.

---

## Scene 4 — providers (10 s)

**Narration.** *"Three providers ship in v0.1: Claude, OpenAI, and any
local model behind Ollama. Each adapter consumes the same normalised
message array, so the daimon can route any request to any of them
without translation."*

```
$ daimon provider list
```

**Expected (with all three keys set):**

```
NAME    CONFIGURED  MODELS
claude  yes         claude-opus-4-7, claude-sonnet-4-6, claude-haiku-4-5-20251001
openai  yes         gpt-5, gpt-5-mini, gpt-4.1
ollama  no          llama3.2:latest
```

**Note.** `ollama` shows `CONFIGURED: no` because there is no API key —
it's a local socket, not a credential-gated service. Models listed reflect
what is actually pulled on this machine (`/api/tags` is harvested live at
daemon start).

**Pacing.** One beat after the table prints. The list is the visual
inventory of what the next scene is about to demonstrate.

---

## Scene 5 — chat with persistent memory (30 s)

**Narration.** *"Now the conversation. `daimon chat` opens a REPL that
threads multi-turn history through every provider call. Switch the
`--provider` flag mid-session, the next model sees everything that came
before."*

### 5a — REPL turn 1 (Claude or Ollama)

```
$ daimon chat --provider claude
```

(Or `--provider ollama --model llama3.2:latest` if no Anthropic key.)

**Type:**

```
> I'm Johannes. I'm building Daimon — a protocol giving every human one sovereign agent for life.
```

**Expected response prefix:** `[claude/claude-haiku-4-5-…]:` (or
`[ollama/llama3.2:latest]:`) followed by an acknowledgement.

**Type:**

```
> Summarise that mission as a six-word slogan.
```

**Expected response:** a six-ish-word slogan from the model. Real example
from a recorded run: `"Empowering Humans with Personalized Life Agents."`

**Type:**

```
> /exit
```

The session is now saved as `$DAIMON_HOME/chat-sessions/current.jsonl`.

### 5b — provider switch (the money beat)

**Narration.** *"Same session file. New provider. Watch."*

```
$ daimon chat --provider openai --once "Now translate that slogan to French."
```

(If no OpenAI key: use `--provider ollama --model llama3.2:latest` —
the wire-shape proof is identical, just less dramatic. v0.1.1 records
this beat with all three keys.)

**Expected.** OpenAI (or Ollama) returns a French translation of the
slogan it didn't generate — proving the previous turn's assistant content
threaded into the new provider's `messages[]` automatically. Real recorded
example with Ollama on both turns: `"Équipage les Humains d'Agents
Personnalisés de Vie"`.

### 5c — third provider (Ollama)

**Narration.** *"And one more switch — to a model running locally on this
laptop, no cloud at all."*

```
$ daimon chat --provider ollama --model llama3.2:latest --once "Rephrase as a haiku."
```

**Expected.** A haiku that references the same slogan-translated-to-French
arc. The narrative thread is intact across three providers without a single
prompt re-explaining what we are doing.

**Pacing.** Each `--once` call takes ~2–4 seconds round-trip. Don't
type the next command until the response renders — the silence sells the
"this is real" feeling.

---

## Scene 6 — close (5 s)

**Narration.** *"One identity. One memory. Three providers. Your daimon
on disk, encrypted, signed, yours. That's v0.1. Spec, code, and the
asciicast for this video are at github.com/regitxx/Daimon."*

Optional final command (sells the "your data" point):

```
$ ls -la /tmp/daimon-demo
```

**Expected.**

```
drwx------  …  /tmp/daimon-demo
-rw-------  …  identity.keystore
-rw-------  …  memory.db
-rw-------  …  activity.log
-rw-------  …  daimon.sock
drwx------  …  chat-sessions/
```

The point: every byte of the principal's state lives in this one
directory, every file is mode 0600 (or 0700 for the dirs), nothing in
the filesystem is shared with the cloud.

```
$ clear
```

End recording.

---

## Total runtime

| Scene | Action | Time |
|---|---|---|
| 1 | `daimon init` (with two password prompts) | 10 s |
| 2 | `daimon unlock` + `daimon identity get` | 10 s |
| 3 | three memory writes + list + search | 15 s |
| 4 | `daimon provider list` | 10 s |
| 5 | chat REPL + two `--once` provider switches | 30 s |
| 6 | filesystem peek + clear | 5 s |
| | **Total** | **~80 s** |

Add ~10 s of cushion for typing pauses and one-beat pacing → **90 s
asciicast**, comfortable.

---

## Recording commands

### asciicast (preferred for v0.1)

```
asciinema rec demo.cast \
  --title "Daimon v0.1 — sovereign agent in 90 seconds" \
  --idle-time-limit 1.5 \
  --command "$SHELL -l"
```

`--idle-time-limit 1.5` clamps long pauses (e.g. between commands) to
1.5 s in the recording without affecting the typing speed during commands.
Result is tighter pacing without losing the natural rhythm.

To trim or re-time after recording, edit `demo.cast` directly — it's
JSON, one event per line.

To export a GIF for embedding (after install: `brew install agg`):

```
agg --speed 1.0 --theme monokai demo.cast demo.gif
```

For an embeddable SVG (smaller, scales perfectly):

```
agg --renderer fontdue --output-format svg demo.cast demo.svg
```

### screencast / video (v0.1.1 follow-up)

QuickTime → File → New Screen Recording → record full terminal at
1080p. Voice over the asciicast script (it's the same script either
way). Edit in iMovie or Descript; export H.264 1080p to
`demo/demo.mp4`. Don't bake the recording into the README — link to a
GitHub release asset or YouTube upload, since binary video bloats the
repo.

---

## What if it goes wrong on the first take

- **`daimon init` says "keystore already exists".** You forgot the
  `rm -rf /tmp/daimon-demo` step from the checklist. Recover: `rm -rf`,
  re-run.
- **`daimon unlock` says "wrong password or corrupted keystore".** The
  password you typed in `init` doesn't match the one you typed in
  `unlock`. Recover: `pkill -f bin/daimond && rm -rf /tmp/daimon-demo`,
  start over.
- **`daimon provider list` is missing claude / openai.** API keys not
  set in this shell. Either set them and restart the daemon
  (`pkill -f bin/daimond && daimon unlock`), or accept the Ollama-only
  recording for v0.1 and tag the asciicast accordingly.
- **`daimon chat --provider claude` returns an auth error.** Same as
  above — key missing or wrong. The whole adapter is gated on credential
  presence at daemon start; a missing key shows up as `CONFIGURED: no`
  in `provider list` not as a runtime failure.
- **The model gives a boring or off-topic reply.** Re-record. The
  models are non-deterministic and you will not get the exact same
  output twice. Pick the take where the model's reply is on-topic and
  short enough to fit a 90-s budget.
- **Typo mid-recording.** Backspace and continue — typos are texture.
  But if a typo derails the next command's prompt, abort and re-record.

The script is short enough that re-recording from scratch is cheaper
than editing a marginal take.
