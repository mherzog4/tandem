// Package extract is the domain extractor sidecar (FR12): an LLM
// watcher over the rolling session transcript that proposes
// EventStorming cards into the Board's normal proposed state.
//
// PRD risk 3 is the design constraint — a chatty extractor gets
// ignored. Guards: confidence threshold (≥0.8), at most 3 proposals
// per tick, dedupe against every card already on the board, and
// provenance (the transcript quote) on every card so both parties can
// judge a proposal at a glance.
//
// The extractor consumes the REDACTED transcript stream (the same
// bytes guests see), so masked secrets never reach the model.
package extract

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/mherzog4/tandem/internal/board"
)

const (
	// WindowBytes of transcript context per tick.
	WindowBytes = 8 << 10
	// Interval between extraction ticks (skipped when nothing new).
	Interval = 45 * time.Second
	// MinConfidence below which proposals are dropped.
	MinConfidence = 0.8
	// MaxPerTick caps proposals per interval.
	MaxPerTick = 3
)

// Proposal is what the model returns per candidate card.
type Proposal struct {
	Type       string  `json:"type"`       // event | command | actor | term
	Text       string  `json:"text"`       // business wording
	Confidence float64 `json:"confidence"` // 0..1
	Quote      string  `json:"quote"`      // transcript provenance
}

// complete is the LLM call, injectable for tests.
type complete func(ctx context.Context, prompt string) (string, error)

// Extractor accumulates transcript and proposes cards on a timer.
type Extractor struct {
	mu     sync.Mutex
	buf    []byte
	dirty  bool
	closed chan struct{}

	llm     complete
	propose func([]board.Card) // receives accepted proposals
	cards   func() []board.Card
}

// New returns an Extractor if the environment provides credentials
// (ANTHROPIC_API_KEY), else nil — the feature is simply off.
// TANDEM_EXTRACTOR_MODEL overrides the model (default claude-opus-4-8).
func New(cards func() []board.Card, propose func([]board.Card)) *Extractor {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil
	}
	model := os.Getenv("TANDEM_EXTRACTOR_MODEL")
	if model == "" {
		model = "claude-opus-4-8"
	}
	client := anthropic.NewClient()
	llm := func(ctx context.Context, prompt string) (string, error) {
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(model),
			MaxTokens: 2048,
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
			},
		})
		if err != nil {
			return "", err
		}
		var out strings.Builder
		for _, block := range resp.Content {
			if t, ok := block.AsAny().(anthropic.TextBlock); ok {
				out.WriteString(t.Text)
			}
		}
		return out.String(), nil
	}
	return newWith(llm, cards, propose)
}

func newWith(llm complete, cards func() []board.Card, propose func([]board.Card)) *Extractor {
	return &Extractor{llm: llm, cards: cards, propose: propose, closed: make(chan struct{})}
}

// Write feeds transcript bytes (io.Writer, sits on the redacted tee).
func (e *Extractor) Write(p []byte) (int, error) {
	e.mu.Lock()
	e.buf = append(e.buf, p...)
	if over := len(e.buf) - WindowBytes; over > 0 {
		e.buf = e.buf[over:]
	}
	e.dirty = true
	e.mu.Unlock()
	return len(p), nil
}

// NoteComposer feeds submitted prompts into the transcript window too —
// the stakeholder's own words are the richest source of domain language.
func (e *Extractor) NoteComposer(text string) {
	_, _ = e.Write([]byte("\n[prompt submitted] " + text + "\n"))
}

// Run ticks until Close. Call in a goroutine.
func (e *Extractor) Run() {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-e.closed:
			return
		case <-ticker.C:
			e.tick(context.Background())
		}
	}
}

func (e *Extractor) Close() { close(e.closed) }

// tick runs one extraction pass. Exported for tests via Tick.
func (e *Extractor) tick(ctx context.Context) {
	e.mu.Lock()
	if !e.dirty {
		e.mu.Unlock()
		return
	}
	window := stripANSI(string(e.buf))
	e.dirty = false
	e.mu.Unlock()

	existing := e.cards()
	known := make(map[string]bool, len(existing))
	var knownList []string
	for _, c := range existing {
		known[normalize(c.Text)] = true
		knownList = append(knownList, c.Type+": "+c.Text)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := e.llm(ctx, buildPrompt(window, knownList))
	if err != nil {
		return // transient failure: next tick retries with fresh window
	}

	accepted := filter(parseProposals(raw), known)
	if len(accepted) > 0 {
		e.propose(accepted)
	}
}

// Tick exposes one synchronous pass for tests and evals.
func (e *Extractor) Tick(ctx context.Context) { e.tick(ctx) }

func buildPrompt(window string, known []string) string {
	var b strings.Builder
	b.WriteString(`You watch a pairing session between a software engineer and a business domain expert working with a coding agent. Extract NEW domain model elements from the transcript below into EventStorming cards.

Card types:
- "event": a domain event, past tense (e.g. "Claim Denied")
- "command": an action/intent (e.g. "Deny Claim")
- "actor": a role or actor (e.g. "Adjuster")
- "term": a business term or rule (e.g. "Reopen keeps the claim ID")

Rules:
- Only genuinely domain-specific elements. Never propose programming concepts, tool names, file names, or generic words.
- Use the domain expert's own wording.
- confidence: your certainty (0..1) this is a real, useful domain element. Be conservative.
- quote: the shortest transcript fragment that evidences the card.
- Propose at most 3 cards. Fewer good cards beat many weak ones. Propose nothing if nothing new appears.

Already on the board (do NOT repropose):
`)
	if len(known) == 0 {
		b.WriteString("(empty)\n")
	}
	for _, k := range known {
		b.WriteString("- " + k + "\n")
	}
	b.WriteString("\nTranscript window:\n---\n" + window + "\n---\n\n")
	b.WriteString(`Respond with ONLY a JSON array (no prose, no code fences): [{"type":"event","text":"...","confidence":0.9,"quote":"..."}]`)
	return b.String()
}

// parseProposals tolerates prose around the JSON: it scans for the
// first '[' and unmarshals from there.
func parseProposals(raw string) []Proposal {
	start := strings.Index(raw, "[")
	if start < 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw[start:]))
	var out []Proposal
	if err := dec.Decode(&out); err != nil {
		return nil
	}
	return out
}

func filter(props []Proposal, known map[string]bool) []board.Card {
	var out []board.Card
	for _, p := range props {
		if len(out) >= MaxPerTick {
			break
		}
		if p.Confidence < MinConfidence || p.Text == "" || !board.ValidType(p.Type) {
			continue
		}
		if known[normalize(p.Text)] {
			continue
		}
		known[normalize(p.Text)] = true
		out = append(out, board.Card{
			Type:       p.Type,
			Text:       p.Text,
			Author:     "extractor",
			State:      board.StateProposed,
			Provenance: strings.TrimSpace(p.Quote),
		})
	}
	return out
}

func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\a]*\a|[\x00-\x08\x0b-\x1f]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }
