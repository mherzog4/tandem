# Agent compatibility matrix

Baseline for **every** terminal program: shared terminal view, gated
Composer, submit-time injection (`Ctrl-]` flushes the buffer into the
agent as a bracketed paste + Enter). No agent-specific integration
required (FR1).

Context injection (the confirmed Domain Board reaching the agent, FR15)
varies by agent — `tandem` auto-detects from the wrapped command name
(PRD §8.3):

| Agent | Composer → input line | Domain injection |
|-------|----------------------|------------------|
| Claude Code (`claude`, `claude-*`) | live with `--mirror` (opt-in), otherwise submit-time | managed `CLAUDE.md` include importing `DOMAIN.md` |
| Known agent CLIs (see list) | submit-time; `--mirror` may work but is untested | compact confirmed-card digest prepended to each submitted prompt (≤1 KiB, overflow points to `DOMAIN.md`) |
| Plain shells / anything else | submit-time | **clipboard mode**: `Ctrl-]` copies the composed prompt (with dirty-model note) to the host clipboard via OSC 52 — paste it wherever. The Board and `DOMAIN.md`/`domain.yaml` stay fully functional. |

**Prepend tier — recognized by binary name** (`internal/adapter/generic.go`):
`codex`, `gemini`, `aider`, `droid` (Factory), `cursor-agent`, `amp`,
`opencode`, `crush`, `goose`, `qwen`, `openhands`, `codebuff`, `plandex`/`pdx`,
`grok`, `auggie`, `forge`, `continue`/`cn`, `ra-aid`, `mentat`, `kode`.

Register a harness not on this list with **`TANDEM_PREPEND_AGENTS`**
(comma-separated binary names) — no code change needed:

```sh
TANDEM_PREPEND_AGENTS=myagent,teamtool tandem myagent
```

Clipboard mode uses the OSC 52 terminal escape, which most modern
terminals (iTerm2, kitty, tmux with `set-clipboard on`, Ghostty,
WezTerm) honor; if yours doesn't, the prompt is still in `DOMAIN.md`
provenance and the recap.

## How live mirroring works (`--mirror`)

The daemon converges the agent's input line on the Composer buffer:
backspaces to the common prefix, retype the suffix inside a bracketed
paste. Newlines/tabs flatten to spaces while composing (the real
newlines return at submit). Mirroring pauses while the host is typing
(1 s of keyboard idle required) so two writers never interleave, and
every mirror write passes the same Ed25519 signing chokepoint as
submissions (`docs/protocol.md`).

Known limits: mirroring assumes the agent's input widget behaves like a
line editor (chars append, 0x7f deletes). If a TUI misbehaves, drop the
flag — submit-time injection always works.

## Dictation (FR9)

Push-to-talk uses the browser's native `SpeechRecognition` (Chrome,
Edge, Safari; the mic button hides on Firefox). Final transcripts
insert at the guest's cursor through the normal composer-op path, so
attribution and undo work like typed text.

Trade-off vs the PRD's "Whisper-class model": speech is processed by
the browser vendor's recognizer rather than a model we choose, but no
audio ever crosses the relay and no API keys are required. For
privacy-sensitive teams a hosted/local Whisper backend can replace the
recognizer behind the same `insertDictation` seam — audio would then
travel sealed like every other frame.
