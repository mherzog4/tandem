// Package mirror live-renders the Composer buffer into the wrapped
// agent's input line (FR6 "one source of truth", PRD risk 1).
//
// Strategy: keep the last string we typed into the agent; on every doc
// change, erase back to the common prefix with backspaces and retype
// the new suffix inside a bracketed paste (so TUIs treat it literally —
// no slash menus, no shortcut interpretation). Newlines become spaces
// while composing; the real newlines return at submit time, which uses
// its own bracketed-paste path.
//
// Fragility is managed, not denied (the PRD's own mitigation): the
// Composer panel stays the source of truth, mirroring is opt-in
// (--mirror), and it pauses whenever the host is typing so two writers
// never interleave on the input line. Every write goes through the
// signing chokepoint like any other network-derived input.
package mirror

import (
	"strings"
	"sync"
	"time"
)

// Submitter is the signed-injection facade (implemented by the daemon
// wiring a Signer to a ptywrap.Injector).
type Submitter func(raw string)

type Mirror struct {
	mu         sync.Mutex
	last       []rune
	submit     Submitter
	hostActive func() bool // true while the host is typing

	pending   string
	dirty     bool
	debounce  *time.Timer
	debounceD time.Duration
}

func New(submit Submitter, hostActive func() bool) *Mirror {
	return &Mirror{submit: submit, hostActive: hostActive, debounceD: 80 * time.Millisecond}
}

// Update schedules the input line to converge on text. Debounced so a
// burst of ops mirrors once.
func (m *Mirror) Update(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = text
	m.dirty = true
	if m.debounce == nil {
		m.debounce = time.AfterFunc(m.debounceD, m.flush)
	} else {
		m.debounce.Reset(m.debounceD)
	}
}

func (m *Mirror) flush() {
	m.mu.Lock()
	if !m.dirty {
		m.mu.Unlock()
		return
	}
	if m.hostActive != nil && m.hostActive() {
		// Host is typing: retry once they pause instead of interleaving.
		m.debounce.Reset(200 * time.Millisecond)
		m.mu.Unlock()
		return
	}
	target := sanitize(m.pending)
	seq := diffKeystrokes(m.last, target)
	m.last = target
	m.dirty = false
	m.mu.Unlock()

	if seq != "" {
		m.submit(seq)
	}
}

// Reset forgets the mirrored state (after a submit cleared the line).
func (m *Mirror) Reset() {
	m.mu.Lock()
	m.last = nil
	m.dirty = false
	m.mu.Unlock()
}

// ClearAndReset returns the keystrokes that erase the currently mirrored
// preview from the agent's input line (one backspace per rune) and
// forgets the mirrored state. Used at submit time so the authoritative
// paste replaces the live preview instead of doubling it.
func (m *Mirror) ClearAndReset() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seq := strings.Repeat("\x7f", len(m.last))
	m.last = nil
	m.dirty = false
	return seq
}

// sanitize replaces characters that a line editor would interpret while
// composing. Newlines flatten to spaces; other control runes drop.
func sanitize(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			out = append(out, ' ')
		case r < 0x20 || r == 0x7f:
			// control characters never reach the input line
		default:
			out = append(out, r)
		}
	}
	return out
}

// diffKeystrokes produces the byte sequence converging the input line
// from old to new: backspaces to the common prefix, then the new suffix
// typed raw. Raw (not bracketed paste) so the preview renders cleanly on
// every agent — shells don't strip paste markers, Claude Code does, and
// raw sidesteps both. sanitize() has already dropped control runes and
// flattened newlines, so the raw suffix carries no escape sequences.
// The authoritative submit (Ctrl-]) still uses bracketed paste + Enter.
func diffKeystrokes(old, new []rune) string {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("\x7f", len(old)-p))
	if p < len(new) {
		b.WriteString(string(new[p:]))
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}
