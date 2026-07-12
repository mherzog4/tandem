// Package composer implements the shared Prompt Composer document
// (FR6/FR7): the buffer both parties edit and only the host can flush
// to the agent.
//
// The PRD sketches this as a CRDT (Yjs), but the relay topology is a
// star — guests talk only to the host, never to each other — so every
// edit already funnels through one point. A host-authoritative
// operational-transform document exploits that: the host serializes
// concurrent ops (transforming positions against anything the sender
// hadn't seen), assigns revisions, and rebroadcasts. Guests apply
// authoritative ops in revision order and never diverge. Same
// Google-Docs UX; the single authority is also exactly the security
// stance FR21 wants.
//
// Attribution is tracked per rune (FR7): every insert records its
// author, so the recap can show who contributed which words, and undo
// is per author.
package composer

import (
	"fmt"
	"sync"
)

// Op is one edit: delete Del runes at Pos, then insert Ins there.
// BaseRev is the revision the sender had applied when producing the op.
type Op struct {
	Author  string `json:"author"`
	BaseRev int    `json:"baseRev"`
	Pos     int    `json:"pos"`
	Del     int    `json:"del"`
	Ins     string `json:"ins"`
}

// Applied is an op after transformation, stamped with its revision.
type Applied struct {
	Op
	Rev int `json:"rev"`

	undone bool // set when a per-author undo reverted this op
}

// Span is a run of consecutive runes by one author, for rendering
// attribution colors and computing per-author contribution stats.
type Span struct {
	Author string `json:"author"`
	Len    int    `json:"len"`
}

// Snapshot is the full document state sent to (re)joining clients.
type Snapshot struct {
	Rev   int    `json:"rev"`
	Text  string `json:"text"`
	Spans []Span `json:"spans"`
}

// Doc is the authoritative document. All methods are safe for
// concurrent use.
type Doc struct {
	mu      sync.Mutex
	runes   []rune
	authors []string  // parallel to runes
	history []Applied // all applied ops, oldest first
}

func NewDoc() *Doc { return &Doc{} }

// Apply transforms op against everything applied since op.BaseRev,
// applies it, and returns the authoritative form to broadcast.
func (d *Doc) Apply(op Op) (Applied, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if op.BaseRev < 0 || op.BaseRev > len(d.history) {
		return Applied{}, fmt.Errorf("baseRev %d out of range (rev %d)", op.BaseRev, len(d.history))
	}
	// Transform against concurrent ops the sender hadn't seen.
	for _, prior := range d.history[op.BaseRev:] {
		op = transform(op, prior.Op)
	}
	// Clamp defensively: a hostile guest crafts arbitrary numbers.
	if op.Pos < 0 {
		op.Pos = 0
	}
	if op.Pos > len(d.runes) {
		op.Pos = len(d.runes)
	}
	if op.Del < 0 {
		op.Del = 0
	}
	if op.Pos+op.Del > len(d.runes) {
		op.Del = len(d.runes) - op.Pos
	}

	d.splice(op.Pos, op.Del, op.Ins, op.Author)
	applied := Applied{Op: op, Rev: len(d.history) + 1}
	d.history = append(d.history, applied)
	return applied, nil
}

// Undo reverts the author's most recent op that still has effect,
// expressed as a fresh op so every client converges the same way.
// Returns false if the author has nothing to undo.
func (d *Doc) Undo(author string) (Applied, bool) {
	d.mu.Lock()
	var inverse Op
	found := false
	for i := len(d.history) - 1; i >= 0 && !found; i-- {
		h := d.history[i]
		if h.Author != author || h.undone {
			continue
		}
		// Build the inverse against the CURRENT document: shift the
		// original position through every later op, then base the
		// inverse at the current revision so Apply won't re-transform
		// what was already accounted for.
		inv := Op{Author: author, BaseRev: len(d.history), Pos: h.Pos}
		for _, later := range d.history[h.Rev:] {
			inv.Pos = transformPos(inv.Pos, later.Op)
		}
		insLen := runeLen(h.Ins)
		// The inverse deletes what was inserted; deleted text is not
		// restored (ponytail: insert-undo only — restoring deletions
		// needs tombstones; add if real sessions miss it).
		if insLen == 0 {
			continue
		}
		inv.Del = insLen
		if inv.Pos+inv.Del > len(d.runes) {
			inv.Del = len(d.runes) - inv.Pos
		}
		d.history[i].undone = true
		inverse = inv
		found = true
	}
	d.mu.Unlock()
	if !found {
		return Applied{}, false
	}
	applied, err := d.Apply(inverse)
	if err != nil {
		return Applied{}, false
	}
	return applied, true
}

// Snapshot returns the current state for a joining client.
func (d *Doc) Snapshot() Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	spans := []Span{}
	for i, a := range d.authors {
		if i > 0 && spans[len(spans)-1].Author == a {
			spans[len(spans)-1].Len++
		} else {
			spans = append(spans, Span{Author: a, Len: 1})
		}
	}
	return Snapshot{Rev: len(d.history), Text: string(d.runes), Spans: spans}
}

// Text returns the current buffer contents.
func (d *Doc) Text() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.runes)
}

// Reset clears the document (after a submit) and returns the applied
// clear op to broadcast.
func (d *Doc) Reset(author string) Applied {
	d.mu.Lock()
	n := len(d.runes)
	rev := len(d.history)
	d.mu.Unlock()
	applied, _ := d.Apply(Op{Author: author, BaseRev: rev, Pos: 0, Del: n})
	return applied
}

// AuthorStats returns rune counts by author for the current text —
// feeds the stakeholder-authorship metric and the recap.
func (d *Doc) AuthorStats() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	stats := map[string]int{}
	for _, a := range d.authors {
		stats[a]++
	}
	return stats
}

func (d *Doc) splice(pos, del int, ins, author string) {
	insRunes := []rune(ins)
	newRunes := make([]rune, 0, len(d.runes)-del+len(insRunes))
	newRunes = append(newRunes, d.runes[:pos]...)
	newRunes = append(newRunes, insRunes...)
	newRunes = append(newRunes, d.runes[pos+del:]...)
	d.runes = newRunes

	newAuthors := make([]string, 0, cap(newRunes))
	newAuthors = append(newAuthors, d.authors[:pos]...)
	for range insRunes {
		newAuthors = append(newAuthors, author)
	}
	newAuthors = append(newAuthors, d.authors[pos+del:]...)
	d.authors = newAuthors
}

// transform rewrites op so it applies after prior. All range math runs
// in prior's original coordinate frame, then maps to the new frame.
// Insert-vs-insert ties break toward the prior op.
func transform(op, prior Op) Op {
	opStart, opEnd := op.Pos, op.Pos+op.Del

	// Delete vs prior delete: remove the already-deleted overlap.
	if prior.Del > 0 && op.Del > 0 {
		priorStart, priorEnd := prior.Pos, prior.Pos+prior.Del
		overlap := max(0, min(opEnd, priorEnd)-max(opStart, priorStart))
		op.Del -= overlap
		if opStart <= priorStart {
			op.Pos = opStart
		} else {
			op.Pos = max(opStart-prior.Del, priorStart)
		}
	} else if prior.Del > 0 {
		// Pure position shift for an insert-only op.
		op.Pos = transformPos(op.Pos, Op{Pos: prior.Pos, Del: prior.Del})
	}

	// Prior insert: shift if at/before us; if it lands strictly inside
	// our delete range, truncate the delete at the insertion point so we
	// never swallow another author's fresh text.
	// ponytail: truncation may leave residue right of the insert for the
	// user to re-delete; splitting the op would fix it if it matters.
	if prior.Ins != "" {
		insLen := runeLen(prior.Ins)
		if prior.Pos <= op.Pos {
			op.Pos += insLen
		} else if op.Del > 0 && prior.Pos < op.Pos+op.Del {
			op.Del = prior.Pos - op.Pos
		}
	}
	return op
}

// transformPos shifts a position to account for a prior op.
func transformPos(pos int, prior Op) int {
	if prior.Del > 0 && pos > prior.Pos {
		removed := min(prior.Del, pos-prior.Pos)
		pos -= removed
	}
	if prior.Ins != "" && pos >= prior.Pos {
		pos += runeLen(prior.Ins)
	}
	return pos
}

func runeLen(s string) int { return len([]rune(s)) }
