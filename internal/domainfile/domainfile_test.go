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
