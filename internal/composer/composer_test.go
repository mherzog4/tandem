package composer

import (
	"testing"
)

func apply(t *testing.T, d *Doc, op Op) Applied {
	t.Helper()
	a, err := d.Apply(op)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSequentialEdits(t *testing.T) {
	d := NewDoc()
	apply(t, d, Op{Author: "priya", BaseRev: 0, Pos: 0, Ins: "hello"})
	apply(t, d, Op{Author: "marcus", BaseRev: 1, Pos: 5, Ins: " world"})
	if d.Text() != "hello world" {
		t.Fatalf("text = %q", d.Text())
	}
	snap := d.Snapshot()
	if snap.Rev != 2 || len(snap.Spans) != 2 ||
		snap.Spans[0] != (Span{Author: "priya", Len: 5}) ||
		snap.Spans[1] != (Span{Author: "marcus", Len: 6}) {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// TestConcurrentInserts: two authors edit from the same base revision;
// the transform keeps both edits, positions shifted.
func TestConcurrentInserts(t *testing.T) {
	d := NewDoc()
	apply(t, d, Op{Author: "priya", BaseRev: 0, Pos: 0, Ins: "claim rule"})
	// Both edit from rev 1 concurrently.
	apply(t, d, Op{Author: "priya", BaseRev: 1, Pos: 0, Ins: "the "})     // "the claim rule"
	a := apply(t, d, Op{Author: "marcus", BaseRev: 1, Pos: 10, Ins: "s"}) // meant after "rule"
	if d.Text() != "the claim rules" {
		t.Fatalf("text = %q", d.Text())
	}
	if a.Pos != 14 {
		t.Fatalf("transformed pos = %d, want 14", a.Pos)
	}
}

func TestConcurrentDeleteOverlap(t *testing.T) {
	d := NewDoc()
	apply(t, d, Op{Author: "p", BaseRev: 0, Pos: 0, Ins: "abcdef"})
	// Both delete "cd" region concurrently from rev 1.
	apply(t, d, Op{Author: "p", BaseRev: 1, Pos: 2, Del: 2}) // "abef"
	apply(t, d, Op{Author: "m", BaseRev: 1, Pos: 3, Del: 2}) // wanted to delete "de"
	if d.Text() != "abf" {
		t.Fatalf("text = %q, want abf (no double delete)", d.Text())
	}
}

func TestHostileOpClamped(t *testing.T) {
	d := NewDoc()
	apply(t, d, Op{Author: "p", BaseRev: 0, Pos: 0, Ins: "safe"})
	// Hostile positions/counts must not panic or corrupt.
	apply(t, d, Op{Author: "evil", BaseRev: 1, Pos: 9999, Del: 9999, Ins: "x"})
	apply(t, d, Op{Author: "evil", BaseRev: 2, Pos: -5, Del: -3, Ins: "y"})
	if _, err := d.Apply(Op{Author: "evil", BaseRev: 999, Pos: 0}); err == nil {
		t.Fatal("out-of-range baseRev accepted")
	}
	if d.Text() != "ysafex" {
		t.Fatalf("text = %q", d.Text())
	}
}

func TestUndoPerAuthor(t *testing.T) {
	d := NewDoc()
	apply(t, d, Op{Author: "priya", BaseRev: 0, Pos: 0, Ins: "keep "})
	apply(t, d, Op{Author: "marcus", BaseRev: 1, Pos: 5, Ins: "WRONG "})
	apply(t, d, Op{Author: "priya", BaseRev: 2, Pos: 11, Ins: "tail"})

	// Marcus undoes his own insert; Priya's edits survive.
	if _, ok := d.Undo("marcus"); !ok {
		t.Fatal("undo failed")
	}
	if d.Text() != "keep tail" {
		t.Fatalf("text = %q", d.Text())
	}
	// Nothing left for marcus to undo.
	if _, ok := d.Undo("marcus"); ok {
		t.Fatal("second undo should find nothing")
	}
	// Undo twice for priya unwinds her inserts newest-first.
	d.Undo("priya")
	if d.Text() != "keep " {
		t.Fatalf("text = %q", d.Text())
	}
}

func TestResetAndStats(t *testing.T) {
	d := NewDoc()
	apply(t, d, Op{Author: "priya", BaseRev: 0, Pos: 0, Ins: "abc"})
	apply(t, d, Op{Author: "marcus", BaseRev: 1, Pos: 3, Ins: "de"})
	stats := d.AuthorStats()
	if stats["priya"] != 3 || stats["marcus"] != 2 {
		t.Fatalf("stats = %v", stats)
	}
	d.Reset("host")
	if d.Text() != "" {
		t.Fatalf("text after reset = %q", d.Text())
	}
}

func TestUnicodeAttribution(t *testing.T) {
	d := NewDoc()
	apply(t, d, Op{Author: "p", BaseRev: 0, Pos: 0, Ins: "λ→"})
	apply(t, d, Op{Author: "m", BaseRev: 1, Pos: 1, Ins: "✓"})
	if d.Text() != "λ✓→" {
		t.Fatalf("text = %q", d.Text())
	}
	snap := d.Snapshot()
	if len(snap.Spans) != 3 || snap.Spans[1].Author != "m" {
		t.Fatalf("spans = %+v", snap.Spans)
	}
}

// TestRandomOpsNeverPanic hammers the doc with arbitrary ops (valid
// baseRevs, arbitrary positions/deletes) and checks invariants hold.
func TestRandomOpsNeverPanic(t *testing.T) {
	d := NewDoc()
	rng := uint32(42)
	next := func(n int) int { rng = rng*1664525 + 1013904223; return int(rng>>16) % n }
	authors := []string{"a", "b", "c"}
	for i := 0; i < 2000; i++ {
		rev := d.Snapshot().Rev
		op := Op{
			Author:  authors[next(3)],
			BaseRev: next(rev + 1),
			Pos:     next(50) - 5,
			Del:     next(10) - 2,
			Ins:     []string{"", "x", "λ✓", "word "}[next(4)],
		}
		if _, err := d.Apply(op); err != nil {
			t.Fatalf("op %d rejected: %v", i, err)
		}
		snap := d.Snapshot()
		total := 0
		for _, s := range snap.Spans {
			total += s.Len
		}
		if total != runeLen(snap.Text) {
			t.Fatalf("op %d: attribution desync: spans %d runes, text %d", i, total, runeLen(snap.Text))
		}
		if next(10) == 0 {
			d.Undo(authors[next(3)])
		}
	}
}
