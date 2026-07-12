// Package recap builds the post-session recap (FR18): submitted
// prompts by author, the domain model diff, per-author authorship
// stats (success metric 2: stakeholder authorship share), and commits
// made in the wrapped repo during the session window.
package recap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mherzog4/tandem/internal/board"
)

type submission struct {
	Text  string
	Stats map[string]int
	At    time.Time
}

type Recorder struct {
	mu      sync.Mutex
	started time.Time
	subs    []submission
	initial map[string]board.Card // card ID -> state at session start
}

// New snapshots the session start. startCards is the preloaded board.
func New(startCards []board.Card) *Recorder {
	initial := make(map[string]board.Card, len(startCards))
	for _, c := range startCards {
		initial[c.ID] = c
	}
	return &Recorder{started: time.Now(), initial: initial}
}

// RecordSubmit captures one flushed prompt and its authorship stats.
func (r *Recorder) RecordSubmit(text string, stats map[string]int) {
	r.mu.Lock()
	r.subs = append(r.subs, submission{Text: text, Stats: stats, At: time.Now()})
	r.mu.Unlock()
}

// Render produces the recap markdown from the final board state and
// the wrapped repo's git history.
func (r *Recorder) Render(endCards []board.Card, repoDir string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var b strings.Builder
	b.WriteString("# Tandem session recap\n\n")
	fmt.Fprintf(&b, "Session: %s — %s\n\n",
		r.started.Format("2006-01-02 15:04"), time.Now().Format("15:04 MST"))

	// Authorship (success metric 2).
	totals := map[string]int{}
	grand := 0
	for _, s := range r.subs {
		for a, n := range s.Stats {
			totals[a] += n
			grand += n
		}
	}
	b.WriteString("## Prompt authorship\n\n")
	if grand == 0 {
		b.WriteString("No prompts were submitted from the Composer.\n")
	} else {
		authors := make([]string, 0, len(totals))
		for a := range totals {
			authors = append(authors, a)
		}
		sort.Slice(authors, func(i, j int) bool { return totals[authors[i]] > totals[authors[j]] })
		for _, a := range authors {
			fmt.Fprintf(&b, "- **%s**: %d chars (%.0f%%)\n", a, totals[a], 100*float64(totals[a])/float64(grand))
		}
	}

	// Domain model diff.
	b.WriteString("\n## Domain model changes\n\n")
	var added, confirmed, changed []string
	for _, c := range endCards {
		label := fmt.Sprintf("[%s] %s", c.Type, c.Text)
		if c.CodeName != "" {
			label += " (code: `" + c.CodeName + "`)"
		}
		old, existed := r.initial[c.ID]
		switch {
		case !existed:
			if c.State == board.StateConfirmed {
				label += " ✓ confirmed"
			}
			added = append(added, label)
		case old.Text != c.Text || old.CodeName != c.CodeName:
			changed = append(changed, label)
		case old.State != c.State && c.State == board.StateConfirmed:
			confirmed = append(confirmed, label)
		}
	}
	if len(added)+len(changed)+len(confirmed) == 0 {
		b.WriteString("No board changes this session.\n")
	}
	writeList := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString(title + "\n")
		for _, i := range items {
			b.WriteString("- " + i + "\n")
		}
		b.WriteString("\n")
	}
	writeList("**Added**", added)
	writeList("**Reworded / re-aliased**", changed)
	writeList("**Confirmed**", confirmed)

	// Commits during the session window.
	b.WriteString("## Commits during the session\n\n")
	commits := gitLogSince(repoDir, r.started)
	if len(commits) == 0 {
		b.WriteString("None found.\n")
	}
	for _, c := range commits {
		b.WriteString("- " + c + "\n")
	}

	// Prompts, in order, with attribution.
	b.WriteString("\n## Prompts sent to the agent\n\n")
	if len(r.subs) == 0 {
		b.WriteString("None.\n")
	}
	for i, s := range r.subs {
		var who []string
		for a := range s.Stats {
			who = append(who, a)
		}
		sort.Strings(who)
		fmt.Fprintf(&b, "### %d. %s — %s\n\n", i+1, s.At.Format("15:04"), strings.Join(who, ", "))
		b.WriteString("> " + strings.ReplaceAll(strings.TrimSpace(s.Text), "\n", "\n> ") + "\n\n")
	}
	return b.String()
}

// WriteFile renders and writes the recap into dir, returning the path.
func (r *Recorder) WriteFile(endCards []board.Card, dir string) (string, string, error) {
	md := r.Render(endCards, dir)
	name := "tandem-recap-" + r.started.Format("2006-01-02-1504") + ".md"
	path := filepath.Join(dir, name)
	return path, md, os.WriteFile(path, []byte(md), 0o644)
}

// gitLogSince lists commits in dir since t; empty when dir is not a
// repo or git is unavailable.
func gitLogSince(dir string, t time.Time) []string {
	cmd := exec.Command("git", "log", "--since="+t.Format(time.RFC3339),
		"--pretty=format:%h %s (%an)")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}
