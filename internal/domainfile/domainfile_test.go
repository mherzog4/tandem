package domainfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mherzog4/tandem/internal/board"
)

func sampleCards() []board.Card {
	return []board.Card{
		{ID: "e1", Type: board.TypeEvent, Text: "Claim Filed", Author: "marcus", State: board.StateConfirmed},
		{ID: "e2", Type: board.TypeEvent, Text: "Claim Denied", Author: "marcus", State: board.StateConfirmed, CodeName: "ClaimDenied"},
		{ID: "e3", Type: board.TypeEvent, Text: "still proposed", Author: "priya", State: board.StateProposed},
		{ID: "t1", Type: board.TypeTerm, Text: "Reopen keeps the claim ID", Author: "marcus", State: board.StateConfirmed},
	}
}

func TestRenderOnlyConfirmed(t *testing.T) {
	md := RenderMarkdown(sampleCards())
	if strings.Contains(md, "still proposed") {
		t.Fatal("proposed card serialized")
	}
	if !strings.Contains(md, "- Claim Denied (code: `ClaimDenied`)") {
		t.Fatalf("alias missing:\n%s", md)
	}
	// Event order preserved.
	if strings.Index(md, "Claim Filed") > strings.Index(md, "Claim Denied") {
		t.Fatal("event order lost")
	}

	y := RenderYAML(sampleCards())
	if !strings.Contains(y, "version: 1") || !strings.Contains(y, `code: "ClaimDenied"`) {
		t.Fatalf("yaml:\n%s", y)
	}
	if strings.Contains(y, "still proposed") {
		t.Fatal("proposed card in yaml")
	}
}

func TestWriteFilesStable(t *testing.T) {
	dir := t.TempDir()

	// Nothing confirmed and no pre-existing files: never litter.
	wrote, err := WriteFiles(dir, []board.Card{{ID: "x", Type: board.TypeEvent, Text: "p", Author: "a", State: board.StateProposed}})
	if err != nil || wrote {
		t.Fatalf("wrote=%v err=%v for unconfirmed board", wrote, err)
	}
	if _, err := os.Stat(filepath.Join(dir, YAMLName)); err == nil {
		t.Fatal("file created with nothing confirmed")
	}

	// First confirmed write creates both files.
	wrote, err = WriteFiles(dir, sampleCards())
	if err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}

	// Same content again: no write (stable serialization, FR14).
	wrote, err = WriteFiles(dir, sampleCards())
	if err != nil || wrote {
		t.Fatalf("re-serialization not stable: wrote=%v err=%v", wrote, err)
	}

	// Demoting a card updates the files.
	cards := sampleCards()
	cards[1].State = board.StateProposed
	wrote, _ = WriteFiles(dir, cards)
	if !wrote {
		t.Fatal("demotion did not rewrite")
	}
	md, _ := os.ReadFile(filepath.Join(dir, MarkdownName))
	if strings.Contains(string(md), "Claim Denied") {
		t.Fatal("demoted card still serialized")
	}
}

// TestRoundTrip: WriteFiles then Load recovers the confirmed cards with
// order, aliases, and authors intact (FR20).
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteFiles(dir, sampleCards()); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 { // proposed card excluded
		t.Fatalf("loaded %d cards: %+v", len(got), got)
	}
	if got[0].Text != "Claim Filed" || got[1].Text != "Claim Denied" {
		t.Fatalf("event order lost: %+v", got)
	}
	if got[1].CodeName != "ClaimDenied" || got[1].Author != "marcus" || got[1].State != board.StateConfirmed {
		t.Fatalf("fields lost: %+v", got[1])
	}
	if got[2].Type != board.TypeTerm {
		t.Fatalf("type lost: %+v", got[2])
	}
}

func TestLoadMissingAndMalformed(t *testing.T) {
	if cards, err := Load(t.TempDir()); err != nil || cards != nil {
		t.Fatalf("missing file: cards=%v err=%v", cards, err)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, YAMLName), []byte("version: 99\nnot yaml at all {{{\nevents:\n  - id: \"ok1\"\n    name: \"Survivor\"\n  - broken entry\n  - id: \"\"\n    name: \"no id\"\n"), 0o644)
	cards, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Text != "Survivor" {
		t.Fatalf("tolerant parse got %+v", cards)
	}
}
