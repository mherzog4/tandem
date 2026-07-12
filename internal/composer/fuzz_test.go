package composer

import (
	"testing"
	"unicode/utf8"
)

// FuzzApply feeds arbitrary ops (the shape a hostile guest controls) into
// a document and checks the invariants that must hold no matter what:
// Apply never panics, the rune and author slices stay the same length,
// and a successful apply advances the revision by exactly one.
func FuzzApply(f *testing.F) {
	f.Add("alice", 0, 0, 0, "hello")
	f.Add("bob", -1, 1<<30, -5, "")
	f.Add("", 3, -1, 1<<20, "wörld")

	f.Fuzz(func(t *testing.T, author string, baseRev, pos, del int, ins string) {
		if !utf8.ValidString(author) || !utf8.ValidString(ins) {
			return // JSON transport only ever carries valid UTF-8
		}
		d := NewDoc()
		// Apply the same op a few times so BaseRev can land in and out of
		// range against a growing history.
		for range 3 {
			before := len(d.history)
			applied, err := d.Apply(Op{Author: author, BaseRev: baseRev, Pos: pos, Del: del, Ins: ins})
			if len(d.runes) != len(d.authors) {
				t.Fatalf("runes/authors desync: %d vs %d", len(d.runes), len(d.authors))
			}
			if err == nil {
				if applied.Rev != before+1 {
					t.Fatalf("rev = %d, want %d", applied.Rev, before+1)
				}
				if utf8.RuneCountInString(d.Text()) != len(d.runes) {
					t.Fatalf("Text rune count %d != runes %d", utf8.RuneCountInString(d.Text()), len(d.runes))
				}
			}
		}
	})
}
