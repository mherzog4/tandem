package extract

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mherzog4/tandem/internal/board"
)

type harness struct {
	mu       sync.Mutex
	b        *board.Board
	proposed [][]board.Card
	response string
	prompts  []string
	ext      *Extractor
}

func newHarness(response string) *harness {
	h := &harness{b: board.New(), response: response}
	h.ext = newWith(
		func(_ context.Context, prompt string) (string, error) {
			h.mu.Lock()
			h.prompts = append(h.prompts, prompt)
			r := h.response
			h.mu.Unlock()
			return r, nil
		},
		h.b.Cards,
		func(cards []board.Card) {
			h.mu.Lock()
			h.proposed = append(h.proposed, cards)
			h.mu.Unlock()
			for _, c := range cards {
				h.b.Propose(c)
			}
		},
	)
	return h
}

func TestProposalPipeline(t *testing.T) {
	h := newHarness(`Here are the cards:
[
  {"type":"event","text":"Claim Denied","confidence":0.95,"quote":"when the adjuster denies a claim"},
  {"type":"term","text":"Reopen Window","confidence":0.9,"quote":"reopened within 90 days"},
  {"type":"event","text":"Low Confidence Thing","confidence":0.5,"quote":"maybe"},
  {"type":"widget","text":"Bad Type","confidence":0.99,"quote":"x"},
  {"type":"actor","text":"","confidence":0.99,"quote":"x"}
]`)
	h.ext.Write([]byte("\x1b[1;32mthe adjuster\x1b[0m denies a claim; reopened within 90 days\n"))
	h.ext.Tick(context.Background())

	if len(h.proposed) != 1 || len(h.proposed[0]) != 2 {
		t.Fatalf("proposed = %+v", h.proposed)
	}
	got := h.proposed[0]
	if got[0].Text != "Claim Denied" || got[0].Author != "extractor" || got[0].State != board.StateProposed {
		t.Fatalf("card 0 = %+v", got[0])
	}
	if got[0].Provenance != "when the adjuster denies a claim" {
		t.Fatalf("provenance = %q", got[0].Provenance)
	}

	// ANSI stripped from the prompt window.
	if strings.Contains(h.prompts[0], "\x1b[") {
		t.Fatal("ANSI escapes leaked into the prompt")
	}
}

func TestDedupeAgainstBoardAndCap(t *testing.T) {
	h := newHarness(`[
  {"type":"event","text":"Already Known","confidence":0.95,"quote":"q"},
  {"type":"event","text":"new one","confidence":0.9,"quote":"q"},
  {"type":"event","text":"NEW ONE","confidence":0.9,"quote":"dupe within tick"},
  {"type":"term","text":"a","confidence":0.9,"quote":"q"},
  {"type":"term","text":"b","confidence":0.9,"quote":"q"},
  {"type":"term","text":"c","confidence":0.9,"quote":"q"}
]`)
	h.b.Add(board.TypeEvent, "Already Known", "marcus")

	h.ext.Write([]byte("transcript"))
	h.ext.Tick(context.Background())

	if len(h.proposed) != 1 {
		t.Fatalf("proposed batches = %d", len(h.proposed))
	}
	cards := h.proposed[0]
	if len(cards) != MaxPerTick {
		t.Fatalf("cap violated: %d cards", len(cards))
	}
	for _, c := range cards {
		if strings.EqualFold(c.Text, "Already Known") {
			t.Fatal("reproposed a known card")
		}
	}
	// Known cards are listed in the prompt for the model too.
	if !strings.Contains(h.prompts[0], "Already Known") {
		t.Fatal("existing board not in prompt")
	}
}

func TestQuietWhenNothingNew(t *testing.T) {
	h := newHarness(`[]`)
	// No writes: tick must not call the LLM at all.
	h.ext.Tick(context.Background())
	if len(h.prompts) != 0 {
		t.Fatal("LLM called with no new transcript")
	}

	// Garbage response: no proposals, no crash.
	h.response = "I could not find any JSON to give you, sorry!"
	h.ext.Write([]byte("some output"))
	h.ext.Tick(context.Background())
	if len(h.proposed) != 0 {
		t.Fatalf("proposed from garbage: %+v", h.proposed)
	}

	// Second tick without new writes: quiet again.
	before := len(h.prompts)
	h.ext.Tick(context.Background())
	if len(h.prompts) != before {
		t.Fatal("tick ran without new content")
	}
}

// TestLiveEval runs the fixture transcript against the real API.
// Skipped unless ANTHROPIC_API_KEY is set — run manually to measure
// extraction precision before shipping prompt changes:
//
//	ANTHROPIC_API_KEY=... go test ./internal/extract -run TestLiveEval -v
func TestLiveEval(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("no ANTHROPIC_API_KEY; live eval skipped")
	}
	var got [][]board.Card
	b := board.New()
	ext := New(b.Cards, func(cards []board.Card) { got = append(got, cards) })
	if ext == nil {
		t.Fatal("extractor disabled despite key")
	}
	ext.Write([]byte(`[prompt submitted] When an adjuster denies a claim, the customer gets an email
with the denial reason. A claim can be reopened within 90 days of denial — that
is not a new claim, the claim ID stays the same.
agent: I'll model this with a ClaimDenied event and a reopenClaim command...`))
	ext.Tick(context.Background())

	if len(got) == 0 {
		t.Fatal("no proposals from a transcript dense with domain language")
	}
	t.Logf("proposals:")
	for _, batch := range got {
		for _, c := range batch {
			t.Logf("  [%s] %q (evidence: %q)", c.Type, c.Text, c.Provenance)
		}
	}
}

func TestDriftDetection(t *testing.T) {
	h := newHarness(`{
  "cards": [],
  "conflicts": [
    {"cardText":"Claim Denied","usage":"agent renamed it ClaimRejected","quote":"emit ClaimRejected event","confidence":0.9},
    {"cardText":"Reopen Window","usage":"weak hunch","quote":"", "confidence":0.4},
    {"cardText":"","usage":"malformed","quote":"", "confidence":0.95}
  ]
}`)
	var drifts [][]Conflict
	h.ext.OnDrift = func(c []Conflict) { drifts = append(drifts, c) }

	// Confirmed card with alias must appear in the prompt for the model.
	c, _ := h.b.Add(board.TypeEvent, "Claim Denied", "marcus")
	h.b.Confirm(c.ID)
	h.b.SetAlias(c.ID, "ClaimDenied")

	h.ext.Write([]byte("agent: emitting ClaimRejected event"))
	h.ext.Tick(context.Background())

	if len(drifts) != 1 || len(drifts[0]) != 1 {
		t.Fatalf("drifts = %+v", drifts)
	}
	if drifts[0][0].CardText != "Claim Denied" {
		t.Fatalf("drift = %+v", drifts[0][0])
	}
	if !strings.Contains(h.prompts[0], "[confirmed]") || !strings.Contains(h.prompts[0], "code: ClaimDenied") {
		t.Fatal("confirmed state / alias missing from prompt")
	}
}

func TestObjectResponseCards(t *testing.T) {
	h := newHarness(`{"cards":[{"type":"term","text":"Reopen Window","confidence":0.9,"quote":"90 days"}],"conflicts":[]}`)
	h.ext.Write([]byte("x"))
	h.ext.Tick(context.Background())
	if len(h.proposed) != 1 || h.proposed[0][0].Text != "Reopen Window" {
		t.Fatalf("proposed = %+v", h.proposed)
	}
}
