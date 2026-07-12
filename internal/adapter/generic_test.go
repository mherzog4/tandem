package adapter

import (
	"strings"
	"testing"

	"github.com/mherzog4/tandem/internal/board"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		argv []string
		want Kind
	}{
		{[]string{"claude"}, KindClaude},
		{[]string{"/usr/local/bin/claude", "--continue"}, KindClaude},
		{[]string{"codex"}, KindPrepend},
		{[]string{"gemini", "chat"}, KindPrepend},
		{[]string{"/opt/bin/aider"}, KindPrepend},
		{[]string{"droid"}, KindPrepend},        // Factory
		{[]string{"cursor-agent"}, KindPrepend}, // Cursor
		{[]string{"amp"}, KindPrepend},          // Sourcegraph Amp
		{[]string{"opencode", "run"}, KindPrepend},
		{[]string{"/usr/bin/goose"}, KindPrepend},
		{[]string{"crush"}, KindPrepend},
		{[]string{"bash"}, KindClipboard},
		{[]string{"python", "repl.py"}, KindClipboard},
		{nil, KindClipboard},
	}
	for _, c := range cases {
		if got := Detect(c.argv); got != c.want {
			t.Errorf("Detect(%v) = %d, want %d", c.argv, got, c.want)
		}
	}
}

func TestDetectEnvOverride(t *testing.T) {
	// An unknown harness falls back to clipboard...
	if Detect([]string{"myagent"}) != KindClipboard {
		t.Fatal("unknown agent should be clipboard by default")
	}
	// ...unless registered via the env var.
	t.Setenv("TANDEM_PREPEND_AGENTS", "myagent, another-agent")
	if Detect([]string{"myagent"}) != KindPrepend {
		t.Fatal("env-registered agent should be prepend")
	}
	if Detect([]string{"/path/to/another-agent"}) != KindPrepend {
		t.Fatal("env-registered agent (with path) should be prepend")
	}
	if Detect([]string{"bash"}) != KindClipboard {
		t.Fatal("env override should not affect other commands")
	}
}

func confirmed(cards ...board.Card) []board.Card {
	for i := range cards {
		cards[i].State = board.StateConfirmed
	}
	return cards
}

func TestDigestConfirmedOnly(t *testing.T) {
	cards := []board.Card{
		{Type: board.TypeEvent, Text: "ClaimDenied", State: board.StateConfirmed, CodeName: "ClaimDeniedEvent"},
		{Type: board.TypeTerm, Text: "Proposed thing", State: board.StateProposed},
		{Type: board.TypeActor, Text: "Adjuster", State: board.StateConfirmed},
	}
	d := Digest(cards, 1024)
	if !strings.Contains(d, "event: ClaimDenied (code: ClaimDeniedEvent)") {
		t.Fatalf("digest missing confirmed card:\n%s", d)
	}
	if strings.Contains(d, "Proposed thing") {
		t.Fatal("proposed card in digest")
	}
	if !strings.Contains(d, "actor: Adjuster") {
		t.Fatalf("digest:\n%s", d)
	}
	// Empty when nothing confirmed.
	if Digest([]board.Card{{Type: board.TypeEvent, Text: "x", State: board.StateProposed}}, 1024) != "" {
		t.Fatal("digest non-empty with no confirmed cards")
	}
}

func TestDigestBudgetDropsWholeCards(t *testing.T) {
	var many []board.Card
	for i := 0; i < 50; i++ {
		many = append(many, board.Card{Type: board.TypeTerm, Text: strings.Repeat("x", 20), State: board.StateConfirmed})
	}
	d := Digest(many, 200)
	if len(d) > 260 { // header + a couple cards + overflow note, never a truncated card mid-line
		t.Fatalf("digest exceeded budget: %d bytes", len(d))
	}
	if !strings.Contains(d, "more in DOMAIN.md") {
		t.Fatalf("overflow note missing:\n%s", d)
	}
	// Every line is a complete card line (no partial trailing card).
	for _, line := range strings.Split(strings.TrimSpace(d), "\n") {
		if strings.HasPrefix(line, "- ") && !strings.Contains(line, "term:") {
			t.Fatalf("truncated card line: %q", line)
		}
	}
}

func TestPrependPrompt(t *testing.T) {
	digest := Digest(confirmed(board.Card{Type: board.TypeEvent, Text: "E"}), 1024)
	got := PrependPrompt(KindPrepend, digest, "do the thing")
	if !strings.HasPrefix(got, "[Tandem domain model") || !strings.HasSuffix(got, "do the thing") {
		t.Fatalf("prepend = %q", got)
	}
	// Non-prepend kinds pass through unchanged.
	if PrependPrompt(KindClaude, digest, "x") != "x" {
		t.Fatal("Claude prompt should be unchanged")
	}
	if PrependPrompt(KindPrepend, "", "x") != "x" {
		t.Fatal("empty digest should not prepend")
	}
}
