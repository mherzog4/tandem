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
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"sync/atomic"

	"github.com/mherzog4/tandem/internal/board"
	"github.com/mherzog4/tandem/internal/composer"
	"github.com/mherzog4/tandem/internal/hostlink"
)

type Broker struct {
	Doc   *composer.Doc
	Board *board.Board
	link  *hostlink.Link

	// HostToken is the capability that gates host-only board actions
	// (confirm, alias). It is printed only on the host's own link and
	// never sent to guests (FR13: host confirms).
	HostToken string

	// Dropped counts guest frames rejected by the allowlist — a spike is
	// a probe signal worth surfacing later.
	Dropped atomic.Int64

	// OnChange, if set, fires with the new buffer text after every
	// applied edit (op, undo, flush). The mirror layer subscribes.
	OnChange func(text string)

	// OnBoardChange fires with the full card list after every board
	// mutation. The serializer (issue #18) subscribes.
	OnBoardChange func(cards []board.Card)
}

func (b *Broker) changed() {
	if b.OnChange != nil {
		b.OnChange(b.Doc.Text())
	}
}

func New(link *hostlink.Link) *Broker {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		panic(err)
	}
	b := &Broker{
		Doc:       composer.NewDoc(),
		Board:     board.New(),
		link:      link,
		HostToken: base64.RawURLEncoding.EncodeToString(tok),
	}
	link.OnGuestJoin = func(string) { b.sendSnapshot(); b.sendBoard() }
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
	X      float64      `json:"x"`
	Y      float64      `json:"y"`
	Emoji  string       `json:"emoji"`
	// Board fields (issue #17).
	ID       string `json:"id"`
	CardType string `json:"cardType"`
	Text     string `json:"text"`
	ToIndex  int    `json:"toIndex"`
	Token    string `json:"token"`
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
	case "highlight":
		// Temporary pointer ring on the terminal (FR10). Coordinates are
		// fractions of the terminal box; clamp so a hostile guest can't
		// paint outside it.
		if msg.Author == "" || msg.X < 0 || msg.X > 1 || msg.Y < 0 || msg.Y > 1 {
			b.Dropped.Add(1)
			return
		}
		_ = b.link.WriteControl(map[string]any{"type": "highlight", "author": msg.Author, "x": msg.X, "y": msg.Y})
	case "react":
		// Emoji reactions (FR10). Length-capped; content is rendered as
		// text by clients, never as markup.
		if msg.Author == "" || msg.Emoji == "" || len([]rune(msg.Emoji)) > 8 {
			b.Dropped.Add(1)
			return
		}
		_ = b.link.WriteControl(map[string]any{"type": "react", "author": msg.Author, "emoji": msg.Emoji})
	case "board-add":
		// Cards capped at 2 KiB of text and 200 cards total.
		if len(msg.Text) > 2<<10 || len(b.Board.Cards()) >= 200 {
			b.Dropped.Add(1)
			return
		}
		if _, ok := b.Board.Add(msg.CardType, msg.Text, msg.Author); !ok {
			b.Dropped.Add(1)
			return
		}
		b.sendBoard()
	case "board-edit":
		if len(msg.Text) > 2<<10 || !b.Board.Edit(msg.ID, msg.Text, msg.Author) {
			b.Dropped.Add(1)
			return
		}
		b.sendBoard()
	case "board-move":
		if !b.Board.Move(msg.ID, msg.ToIndex) {
			b.Dropped.Add(1)
			return
		}
		b.sendBoard()
	case "board-del":
		if !b.Board.Delete(msg.ID) {
			b.Dropped.Add(1)
			return
		}
		b.sendBoard()
	case "board-confirm":
		if !b.isHost(msg.Token) || !b.Board.Confirm(msg.ID) {
			b.Dropped.Add(1)
			return
		}
		b.sendBoard()
	case "board-alias":
		if !b.isHost(msg.Token) || len(msg.Text) > 128 || !b.Board.SetAlias(msg.ID, msg.Text) {
			b.Dropped.Add(1)
			return
		}
		b.sendBoard()
	default:
		b.Dropped.Add(1)
	}
}

// isHost checks the capability token in constant time.
func (b *Broker) isHost(token string) bool {
	return len(token) == len(b.HostToken) &&
		subtle.ConstantTimeCompare([]byte(token), []byte(b.HostToken)) == 1
}

func (b *Broker) sendBoard() {
	_ = b.link.WriteControl(map[string]any{"type": "board-state", "cards": b.Board.Cards()})
	if b.OnBoardChange != nil {
		b.OnBoardChange(b.Board.Cards())
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
