package recap

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mherzog4/tandem/internal/board"
)

func TestRecapContents(t *testing.T) {
	start := []board.Card{
		{ID: "e1", Type: board.TypeEvent, Text: "Claim Filed", Author: "m", State: board.StateConfirmed},
		{ID: "e2", Type: board.TypeEvent, Text: "Old Wording", Author: "m", State: board.StateProposed},
		{ID: "t1", Type: board.TypeTerm, Text: "Reopen Window", Author: "m", State: board.StateProposed},
	}
	r := New(start)
	r.RecordSubmit("a claim can be reopened within 90 days\nmodel this as ClaimDenied",
		map[string]int{"Marcus": 39, "Priya": 25})
	r.RecordSubmit("now add tests", map[string]int{"Priya": 13})

	end := []board.Card{
		start[0],
		{ID: "e2", Type: board.TypeEvent, Text: "New Wording", Author: "p", State: board.StateProposed},
		{ID: "t1", Type: board.TypeTerm, Text: "Reopen Window", Author: "m", State: board.StateConfirmed, CodeName: "ReopenWindow"},
		{ID: "n1", Type: board.TypeActor, Text: "Adjuster", Author: "extractor", State: board.StateProposed},
	}
	md := r.Render(end, t.TempDir())

	for _, want := range []string{
		"**Marcus**: 39 chars (51%)",
		"**Priya**: 38 chars (49%)",
		"[actor] Adjuster",    // added
		"[event] New Wording", // changed
		"> a claim can be reopened within 90 days",
		"2. ", // second prompt present
	} {
		if !strings.Contains(md, want) {
			t.Errorf("recap missing %q\n---\n%s", want, md)
		}
	}
	// t1 changed alias+state: counts as reworded/re-aliased (alias change).
	if !strings.Contains(md, "Reopen Window (code: `ReopenWindow`)") {
		t.Errorf("alias change not reflected:\n%s", md)
	}
}

func TestGitCommitsInWindow(t *testing.T) {
	dir := t.TempDir()
	dateEnv := ""
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if dateEnv != "" {
			cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+dateEnv, "GIT_COMMITTER_DATE="+dateEnv)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(dir+"/f", []byte("before"), 0o644)
	run("add", "f")
	dateEnv = "2020-01-01T00:00:00" // firmly before the session window
	run("commit", "-qm", "before session")
	dateEnv = ""

	r := New(nil)

	os.WriteFile(dir+"/f", []byte("during"), 0o644)
	run("add", "f")
	run("commit", "-qm", "during session")

	md := r.Render(nil, dir)
	if !strings.Contains(md, "during session") {
		t.Errorf("in-window commit missing:\n%s", md)
	}
	if strings.Contains(md, "before session") {
		t.Errorf("pre-session commit leaked in:\n%s", md)
	}

	// Non-repo dir: graceful.
	if md := r.Render(nil, t.TempDir()); !strings.Contains(md, "None found.") {
		t.Errorf("non-repo dir not graceful:\n%s", md)
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	r := New(nil)
	path, md, err := r.WriteFile(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != md {
		t.Fatalf("file mismatch: %v", err)
	}
	if !strings.HasPrefix(md, "# Tandem session recap") {
		t.Fatal("bad recap header")
	}
}
