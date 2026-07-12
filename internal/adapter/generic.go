package adapter

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mherzog4/tandem/internal/board"
)

// Kind identifies how Tandem injects the domain model for a wrapped
// agent (PRD §8.3 compatibility matrix).
type Kind int

const (
	// KindClaude: full support — managed CLAUDE.md include (FR15).
	KindClaude Kind = iota
	// KindPrepend: Codex CLI / Gemini CLI / Aider — no native hook, so
	// a compact domain digest is prepended to each submitted prompt.
	KindPrepend
	// KindClipboard: anything else — the domain digest is offered for
	// the host to paste manually; the board stays fully functional.
	KindClipboard
)

// Detect classifies argv into an injection Kind.
func Detect(argv []string) Kind {
	if IsClaude(argv) {
		return KindClaude
	}
	if len(argv) == 0 {
		return KindClipboard
	}
	switch filepath.Base(argv[0]) {
	case "codex", "gemini", "aider":
		return KindPrepend
	}
	return KindClipboard
}

// Digest renders a compact, token-budgeted domain digest from confirmed
// cards, for prepending to a prompt (KindPrepend) or pasting
// (KindClipboard). Empty when no cards are confirmed. maxBytes caps the
// output; overflow is dropped with a note rather than truncated
// mid-card (no silent cap — FR15 digest must stay coherent).
func Digest(cards []board.Card, maxBytes int) string {
	var lines []string
	for _, c := range cards {
		if c.State != board.StateConfirmed {
			continue
		}
		line := "- " + c.Type + ": " + c.Text
		if c.CodeName != "" {
			line += " (code: " + c.CodeName + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Tandem domain model — name code constructs after these terms]\n")
	dropped := 0
	for i, l := range lines {
		if b.Len()+len(l)+1 > maxBytes {
			dropped = len(lines) - i
			break
		}
		b.WriteString(l + "\n")
	}
	if dropped > 0 {
		b.WriteString("(+" + strconv.Itoa(dropped) + " more in DOMAIN.md)\n")
	}
	return b.String()
}

// PrependPrompt returns the digest followed by the prompt when the
// agent needs prompt-prepend injection; otherwise the prompt unchanged.
func PrependPrompt(kind Kind, digest, prompt string) string {
	if kind == KindPrepend && digest != "" {
		return digest + "\n" + prompt
	}
	return prompt
}
