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
		{"", "abc", "abc"},
		{"abc", "abx", "\x7fx"},
		{"abc", "ab", "\x7f"},
		{"abc", "abc", ""},
		{"abc", "xyz", "\x7f\x7f\x7fxyz"},
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
	if got := c.all(); got != "hello" {
		t.Fatalf("burst mirrored as %q", got)
	}
	m.Update("help")
	wait()
	if got := c.all(); !strings.HasSuffix(got, "\x7f\x7fp") {
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

func TestClearAndReset(t *testing.T) {
	c := &capture{}
	m := New(c.submit, nil)
	m.Update("hello")
	wait()
	// Live preview is "hello" (5 runes). ClearAndReset erases it.
	seq := m.ClearAndReset()
	if seq != "\x7f\x7f\x7f\x7f\x7f" {
		t.Fatalf("clear seq = %q, want 5 backspaces", seq)
	}
	// State forgotten: a following Update("") is a no-op, and a new
	// Update starts fresh from empty.
	before := c.all()
	m.Update("")
	wait()
	if c.all() != before {
		t.Fatalf("Update(\"\") after clear should be a no-op, got %q", c.all()[len(before):])
	}
	m.Update("hi")
	wait()
	if got := c.all(); !strings.HasSuffix(got, "hi") {
		t.Fatalf("post-clear compose = %q", got)
	}
}

func TestInSync(t *testing.T) {
	c := &capture{}
	m := New(c.submit, nil)
	m.Update("hello")
	if m.InSync("hello") {
		t.Fatal("reported sync before debounce flushed")
	}
	wait()
	if !m.InSync("hello") {
		t.Fatal("did not report sync after mirror flushed")
	}
	m.Update("hello world")
	if m.InSync("hello world") {
		t.Fatal("reported sync while update is dirty")
	}
	wait()
	m.Update("hello\n")
	wait()
	if !m.InSync("hello\n") {
		t.Fatal("sync check should use sanitized text")
	}
}
