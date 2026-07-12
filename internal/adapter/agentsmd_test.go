package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAgents(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentsCreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureAgentsInclude(dir); err != nil {
		t.Fatal(err)
	}
	got := readAgents(t, dir)
	if !strings.Contains(got, "DOMAIN.md") || !strings.Contains(got, beginMark) {
		t.Fatalf("managed block missing:\n%s", got)
	}
	// AGENTS.md points at DOMAIN.md rather than @import-ing it.
	if strings.Contains(got, "@DOMAIN.md") {
		t.Fatalf("AGENTS.md should reference DOMAIN.md, not @import it:\n%s", got)
	}
}

func TestAgentsPreservesUserContentAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	user := "# Project agents\n\nExisting guidance.\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAgentsInclude(dir); err != nil {
		t.Fatal(err)
	}
	first := readAgents(t, dir)
	if !strings.HasPrefix(first, user) {
		t.Fatalf("user content disturbed:\n%s", first)
	}
	for range 3 {
		if err := EnsureAgentsInclude(dir); err != nil {
			t.Fatal(err)
		}
	}
	if readAgents(t, dir) != first {
		t.Fatal("not idempotent")
	}
	if strings.Count(readAgents(t, dir), beginMark) != 1 {
		t.Fatal("block duplicated")
	}
}

func TestAgentsMDEnvOverride(t *testing.T) {
	if Detect([]string{"myharness"}) != KindClipboard {
		t.Fatal("unknown agent should be clipboard by default")
	}
	t.Setenv("TANDEM_AGENTS_MD_AGENTS", "myharness")
	if Detect([]string{"myharness"}) != KindAgentsMD {
		t.Fatal("env-registered agent should use AGENTS.md")
	}
}
