# Agent compatibility matrix

Baseline for **every** terminal program: shared terminal view, gated
Composer, submit-time injection (`Ctrl-]` flushes the buffer into the
agent as a bracketed paste + Enter). No agent-specific integration
required (FR1).

| Agent | Composer → input line | Notes |
|-------|----------------------|-------|
| Claude Code | live with `--mirror` (opt-in), otherwise submit-time | bracketed paste keeps slash menus/shortcuts from firing while mirroring |
| Codex CLI / Gemini CLI / Aider | submit-time (default); `--mirror` may work but is untested | prompt-prepend context injection lands with issue #26 |
| Plain shells (bash 4.4+, zsh) | submit-time | readline handles bracketed paste natively |
| Anything else | submit-time | worst case per PRD risk 1: the Composer panel is the source of truth |

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
