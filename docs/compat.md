# Agent compatibility matrix

Baseline for **every** terminal program: shared terminal view, gated
Composer, submit-time injection (`Ctrl-]` flushes the buffer into the
agent as a bracketed paste + Enter). No agent-specific integration
required (FR1).

Context injection (the confirmed Domain Board reaching the agent, FR15)
varies by agent — `tandem` auto-detects from the wrapped command name
(PRD §8.3):

**Default: live mirroring is ON.** The guest's Composer text appears live in
the engineer's agent input line; the engineer reviews it and presses `Ctrl-]`
to run it (the guest can never execute — their terminal has no stdin path).
`--no-mirror` turns this off and falls back to the compose-then-submit model.

| Agent | With mirror (default) | Domain injection |
|-------|----------------------|------------------|
| Claude Code (`claude`, `claude-*`) | clean | managed `CLAUDE.md` include importing `DOMAIN.md` |
| Known agent CLIs (see list) | clean | confirmed-card digest prepended at run (≤1 KiB) |
| Plain shells / anything else | clean | with `--no-mirror`, `Ctrl-]` copies the prompt to the host clipboard (OSC 52) |

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

## How live mirroring works (default; `--no-mirror` to disable)

The daemon converges the agent's input line on the Composer buffer:
backspaces to the common prefix, then types the new suffix as raw
keystrokes (not a bracketed paste), so it renders cleanly on every
agent. Newlines/tabs flatten to spaces (single line). Mirroring pauses while the host is typing
(1 s of keyboard idle required) so two writers never interleave, and
every mirror write passes the same Ed25519 signing chokepoint as
submissions (`docs/protocol.md`).

**Run = `Ctrl-]`.** It erases the live preview (backspaces), retypes the
authoritative signed buffer as raw keystrokes on one line, and presses Enter,
then clears the Composer. Raw (not bracketed paste) so the run is clean on
every agent; no text is doubled. (With `--no-mirror`, submit uses bracketed
paste, which preserves multi-line prompts on agents that strip the markers.)

Known limit: mirroring assumes the agent's input behaves like a line editor
(characters append, `0x7f` deletes). That holds for Claude Code and plain
shells alike. If a particular TUI mangles it, `--no-mirror` falls back to the
compose-then-submit model.

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
