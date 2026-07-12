// Package broker is the host-side session brain: it consumes decrypted
// guest frames from the hostlink, applies composer edits to the
// authoritative document, and broadcasts authoritative state back.
//
// Everything a guest can say arrives here. The switch below is the
// message allowlist (FR8): unknown or malformed types are dropped and
// counted, never interpreted. Nothing in this package writes to the
// PTY — submission is the host daemon's job (issue #12).
package broker

import (
	"encoding/json"
	"sync/atomic"

	"github.com/mherzog4/tandem/internal/composer"
	"github.com/mherzog4/tandem/internal/hostlink"
)

type Broker struct {
	Doc  *composer.Doc
	link *hostlink.Link

	// Dropped counts guest frames rejected by the allowlist — a spike is
	// a probe signal worth surfacing later.
	Dropped atomic.Int64

	// OnChange, if set, fires with the new buffer text after every
	// applied edit (op, undo, flush). The mirror layer subscribes.
	OnChange func(text string)
}

func (b *Broker) changed() {
	if b.OnChange != nil {
		b.OnChange(b.Doc.Text())
	}
}

func New(link *hostlink.Link) *Broker {
	b := &Broker{Doc: composer.NewDoc(), link: link}
	link.OnGuestJoin = func(string) { b.sendSnapshot() }
	return b
}

// Run drains guest frames until the link closes. Call in a goroutine.
func (b *Broker) Run() {
	for frame := range b.link.Incoming {
		b.handle(frame)
	}
}

// guestMsg is the superset of fields any allowed guest message carries.
type guestMsg struct {
	Type   string       `json:"type"`
	Author string       `json:"author"`
	Pos    int          `json:"pos"`
	Op     *composer.Op `json:"op"`
}

func (b *Broker) handle(frame []byte) {
	if len(frame) < 2 || frame[0] != hostlink.FrameCtrl {
		b.Dropped.Add(1)
		return
	}
	var msg guestMsg
	if err := json.Unmarshal(frame[1:], &msg); err != nil {
		b.Dropped.Add(1)
		return
	}
	switch msg.Type {
	case "op":
		if msg.Op == nil || msg.Op.Author == "" {
			b.Dropped.Add(1)
			return
		}
		// Size caps: a hostile guest must not be able to balloon host
		// memory through the composer. 16 KiB per insert, 256 KiB doc.
		if len(msg.Op.Ins) > 16<<10 || len(b.Doc.Text())+len(msg.Op.Ins) > 256<<10 {
			b.Dropped.Add(1)
			return
		}
		applied, err := b.Doc.Apply(*msg.Op)
		if err != nil {
			b.Dropped.Add(1)
			return
		}
		_ = b.link.WriteControl(map[string]any{"type": "composer-op", "op": applied})
		b.changed()
	case "undo":
		if msg.Author == "" {
			b.Dropped.Add(1)
			return
		}
		if applied, ok := b.Doc.Undo(msg.Author); ok {
			_ = b.link.WriteControl(map[string]any{"type": "composer-op", "op": applied})
			b.changed()
		}
	case "cursor":
		if msg.Author == "" {
			b.Dropped.Add(1)
			return
		}
		// Relay cursor positions to everyone for colored carets.
		_ = b.link.WriteControl(map[string]any{"type": "cursor", "author": msg.Author, "pos": msg.Pos})
	default:
		b.Dropped.Add(1)
	}
}

func (b *Broker) sendSnapshot() {
	_ = b.link.WriteControl(map[string]any{"type": "composer-snapshot", "snapshot": b.Doc.Snapshot()})
}

// Flush returns the buffer for submission, records per-author stats,
// clears the document, and tells every client it was sent. Only the
// host daemon calls this — there is no guest message that reaches it.
func (b *Broker) Flush() (text string, stats map[string]int) {
	text = b.Doc.Text()
	if text == "" {
		return "", nil
	}
	stats = b.Doc.AuthorStats()
	cleared := b.Doc.Reset("host")
	_ = b.link.WriteControl(map[string]any{"type": "composer-op", "op": cleared})
	_ = b.link.WriteControl(map[string]any{"type": "submitted", "stats": stats})
	return text, stats
}
