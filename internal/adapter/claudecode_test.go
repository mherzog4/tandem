package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureClaudeInclude(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir)
	if !strings.Contains(got, "@DOMAIN.md") || !strings.Contains(got, beginMark) {
		t.Fatalf("block missing:\n%s", got)
	}
}

func TestPreservesUserContentAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	user := "# My project\n\nImportant user instructions.\n"
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(user), 0o644)

	if err := EnsureClaudeInclude(dir); err != nil {
		t.Fatal(err)
	}
	first := read(t, dir)
	if !strings.HasPrefix(first, user) {
		t.Fatalf("user content disturbed:\n%s", first)
	}

	// Repeated installs: byte-identical.
	for i := 0; i < 3; i++ {
		if err := EnsureClaudeInclude(dir); err != nil {
			t.Fatal(err)
		}
	}
	if read(t, dir) != first {
		t.Fatal("not idempotent")
	}
	if strings.Count(read(t, dir), beginMark) != 1 {
		t.Fatal("block duplicated")
	}
}

func TestRefreshesStaleBlock(t *testing.T) {
	dir := t.TempDir()
	stale := "user stuff\n" + beginMark + "\nOLD CONTENT\n" + endMark + "\nmore user stuff\n"
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(stale), 0o644)

	if err := EnsureClaudeInclude(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir)
	if strings.Contains(got, "OLD CONTENT") {
		t.Fatal("stale block survived")
	}
	if !strings.HasPrefix(got, "user stuff\n") || !strings.HasSuffix(got, "\nmore user stuff\n") {
		t.Fatalf("surrounding content disturbed:\n%s", got)
	}
}

func TestMangledBlockLeftAlone(t *testing.T) {
	dir := t.TempDir()
	mangled := "content\n" + beginMark + "\nno end marker anywhere"
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(mangled), 0o644)
	if err := EnsureClaudeInclude(dir); err != nil {
		t.Fatal(err)
	}
	if read(t, dir) != mangled {
		t.Fatal("mangled file was modified")
	}
}

func TestIsClaude(t *testing.T) {
	yes := [][]string{{"claude"}, {"/usr/local/bin/claude"}, {"claude", "--continue"}, {"claude-code"}}
	no := [][]string{{"codex"}, {"aider"}, {"sh", "-c", "claude"}, {}}
	for _, argv := range yes {
		if !IsClaude(argv) {
			t.Errorf("IsClaude(%v) = false", argv)
		}
	}
	for _, argv := range no {
		if IsClaude(argv) {
			t.Errorf("IsClaude(%v) = true", argv)
		}
	}
}
