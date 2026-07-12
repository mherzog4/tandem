package mirror

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type capture struct {
	mu   sync.Mutex
	seqs []string
}

func (c *capture) submit(s string) {
	c.mu.Lock()
	c.seqs = append(c.seqs, s)
	c.mu.Unlock()
}
func (c *capture) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.seqs, "")
}

func wait() { time.Sleep(250 * time.Millisecond) }

func TestDiffKeystrokes(t *testing.T) {
	cases := []struct{ old, new, want string }{
		{"", "abc", "\x1b[200~abc\x1b[201~"},
		{"abc", "abx", "\x7f\x1b[200~x\x1b[201~"},
		{"abc", "ab", "\x7f"},
		{"abc", "abc", ""},
		{"abc", "xyz", "\x7f\x7f\x7f\x1b[200~xyz\x1b[201~"},
	}
	for _, c := range cases {
		got := diffKeystrokes([]rune(c.old), []rune(c.new))
		if got != c.want {
			t.Errorf("diff(%q→%q) = %q, want %q", c.old, c.new, got, c.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	got := string(sanitize("line1\nline2\t\x1b[A\x03end"))
	if got != "line1 line2 [Aend" {
		t.Fatalf("sanitize = %q", got)
	}
}

func TestUpdateDebouncesAndConverges(t *testing.T) {
	c := &capture{}
	m := New(c.submit, nil)
	m.Update("h")
	m.Update("he")
	m.Update("hello")
	wait()
	if got := c.all(); got != "\x1b[200~hello\x1b[201~" {
		t.Fatalf("burst mirrored as %q", got)
	}
	m.Update("help")
	wait()
	if got := c.all(); !strings.HasSuffix(got, "\x7f\x7f\x1b[200~p\x1b[201~") {
		t.Fatalf("edit mirrored as %q", got)
	}
}

func TestPausesWhileHostTypes(t *testing.T) {
	c := &capture{}
	var active atomic.Bool
	active.Store(true)
	m := New(c.submit, active.Load)
	m.Update("guest text")
	wait()
	if c.all() != "" {
		t.Fatalf("mirrored while host active: %q", c.all())
	}
	active.Store(false)
	wait()
	if !strings.Contains(c.all(), "guest text") {
		t.Fatalf("never mirrored after host idle: %q", c.all())
	}
}
