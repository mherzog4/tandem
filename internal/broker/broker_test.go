package broker

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mherzog4/tandem/internal/e2e"
	"github.com/mherzog4/tandem/internal/hostlink"
	"github.com/mherzog4/tandem/internal/relay"
)

func setup(t *testing.T) (guest *websocket.Conn, cipher *e2e.Cipher, b *Broker) {
	t.Helper()
	srv := httptest.NewServer(relay.NewServer("http://relay.test"))
	t.Cleanup(srv.Close)
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	link, err := hostlink.Connect(ctx, wsBase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Close() })
	b = New(link)
	go b.Run()

	frag := link.JoinURL[strings.Index(link.JoinURL, "#")+1:]
	key, _ := e2e.DecodeKey(strings.TrimPrefix(frag, e2e.FragmentParam+"="))
	cipher, _ = e2e.NewCipher(key)
	id := link.JoinURL[strings.LastIndex(link.JoinURL, "/s/")+3 : strings.Index(link.JoinURL, "#")]
	guest, _, err = websocket.Dial(ctx, wsBase+"/ws/join/"+id+"?name=m", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guest.Close(websocket.StatusNormalClosure, "") })
	return guest, cipher, b
}

func send(t *testing.T, guest *websocket.Conn, cipher *e2e.Cipher, msg string) {
	t.Helper()
	frame := append([]byte{hostlink.FrameCtrl}, []byte(msg)...)
	if err := guest.Write(context.Background(), websocket.MessageBinary, cipher.Seal(frame)); err != nil {
		t.Fatal(err)
	}
}

// readCtrl reads control frames until one whose type matches.
func readCtrl(t *testing.T, guest *websocket.Conn, cipher *e2e.Cipher, wantType string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		typ, data, err := guest.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ != websocket.MessageBinary {
			continue
		}
		plain, err := cipher.Open(data)
		if err != nil || plain[0] != hostlink.FrameCtrl {
			continue
		}
		var m map[string]any
		if json.Unmarshal(plain[1:], &m) == nil && m["type"] == wantType {
			return m
		}
	}
}

func TestOpAppliedAndBroadcast(t *testing.T) {
	guest, cipher, b := setup(t)
	readCtrl(t, guest, cipher, "composer-snapshot") // join snapshot

	send(t, guest, cipher, `{"type":"op","op":{"author":"m","baseRev":0,"pos":0,"ins":"a claim can be reopened"}}`)
	m := readCtrl(t, guest, cipher, "composer-op")
	op := m["op"].(map[string]any)
	if op["ins"] != "a claim can be reopened" || op["rev"].(float64) != 1 {
		t.Fatalf("bad broadcast op: %v", op)
	}
	if b.Doc.Text() != "a claim can be reopened" {
		t.Fatalf("doc = %q", b.Doc.Text())
	}
}

func TestUndoAndCursorRelay(t *testing.T) {
	guest, cipher, b := setup(t)
	readCtrl(t, guest, cipher, "composer-snapshot")

	send(t, guest, cipher, `{"type":"op","op":{"author":"m","baseRev":0,"pos":0,"ins":"oops"}}`)
	readCtrl(t, guest, cipher, "composer-op")
	send(t, guest, cipher, `{"type":"undo","author":"m"}`)
	readCtrl(t, guest, cipher, "composer-op")
	if b.Doc.Text() != "" {
		t.Fatalf("doc after undo = %q", b.Doc.Text())
	}

	send(t, guest, cipher, `{"type":"cursor","author":"m","pos":3}`)
	c := readCtrl(t, guest, cipher, "cursor")
	if c["author"] != "m" || c["pos"].(float64) != 3 {
		t.Fatalf("cursor relay = %v", c)
	}
}

// TestAllowlistDropsGarbage: unknown types, malformed JSON, non-ctrl
// frames, and anonymous ops are counted and never crash or mutate.
func TestAllowlistDropsGarbage(t *testing.T) {
	guest, cipher, b := setup(t)
	readCtrl(t, guest, cipher, "composer-snapshot")

	garbage := []string{
		`{"type":"submit"}`,
		`{"type":"exec","cmd":"rm -rf /"}`,
		`not json at all`,
		`{"type":"op"}`,
		`{"type":"op","op":{"baseRev":0,"pos":0,"ins":"anon"}}`,
		`{"type":"undo"}`,
	}
	for _, g := range garbage {
		send(t, guest, cipher, g)
	}
	// Raw PTY-kind frame from a guest is also dropped.
	frame := append([]byte{hostlink.FramePTY}, []byte("fake terminal output")...)
	_ = guest.Write(context.Background(), websocket.MessageBinary, cipher.Seal(frame))

	deadline := time.Now().Add(5 * time.Second)
	want := int64(len(garbage) + 1)
	for b.Dropped.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("dropped = %d, want %d", b.Dropped.Load(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.Doc.Text() != "" {
		t.Fatalf("garbage mutated doc: %q", b.Doc.Text())
	}
}

// TestOversizedInsertDropped: memory-abuse ops are rejected by the cap.
func TestOversizedInsertDropped(t *testing.T) {
	guest, cipher, b := setup(t)
	readCtrl(t, guest, cipher, "composer-snapshot")

	big := strings.Repeat("x", 17<<10)
	send(t, guest, cipher, `{"type":"op","op":{"author":"m","baseRev":0,"pos":0,"ins":"`+big+`"}}`)
	deadline := time.Now().Add(5 * time.Second)
	for b.Dropped.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("oversized op not dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.Doc.Text() != "" {
		t.Fatal("oversized op mutated doc")
	}
}

func TestHighlightAndReactRelay(t *testing.T) {
	guest, cipher, b := setup(t)
	readCtrl(t, guest, cipher, "composer-snapshot")

	send(t, guest, cipher, `{"type":"highlight","author":"m","x":0.5,"y":0.25}`)
	h := readCtrl(t, guest, cipher, "highlight")
	if h["x"].(float64) != 0.5 || h["author"] != "m" {
		t.Fatalf("highlight relay = %v", h)
	}

	send(t, guest, cipher, `{"type":"react","author":"m","emoji":"👍"}`)
	r := readCtrl(t, guest, cipher, "react")
	if r["emoji"] != "👍" {
		t.Fatalf("react relay = %v", r)
	}

	// Out-of-bounds highlight and oversized emoji are dropped.
	before := b.Dropped.Load()
	send(t, guest, cipher, `{"type":"highlight","author":"m","x":7.5,"y":0.2}`)
	send(t, guest, cipher, `{"type":"react","author":"m","emoji":"aaaaaaaaaaaaaaaaaa"}`)
	deadline := time.Now().Add(5 * time.Second)
	for b.Dropped.Load() < before+2 {
		if time.Now().After(deadline) {
			t.Fatalf("junk not dropped: %d", b.Dropped.Load()-before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBoardLifecycleViaBroker(t *testing.T) {
	guest, cipher, b := setup(t)
	readCtrl(t, guest, cipher, "composer-snapshot")

	// The join itself broadcasts an (empty) board snapshot; scan until
	// the state we caused appears.
	boardUntil := func(pred func([]any) bool) []any {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if time.Now().After(deadline) {
				t.Fatal("timeout waiting for board state")
			}
			st := readCtrl(t, guest, cipher, "board-state")
			cards, _ := st["cards"].([]any)
			if cards == nil {
				cards = []any{}
			}
			if pred(cards) {
				return cards
			}
		}
	}

	send(t, guest, cipher, `{"type":"board-add","cardType":"event","text":"ClaimDenied","author":"m"}`)
	cards := boardUntil(func(c []any) bool { return len(c) == 1 })
	id := cards[0].(map[string]any)["id"].(string)

	send(t, guest, cipher, `{"type":"board-edit","id":"`+id+`","text":"ClaimDenied by adjuster","author":"p"}`)
	boardUntil(func(c []any) bool {
		return len(c) == 1 && c[0].(map[string]any)["text"] == "ClaimDenied by adjuster"
	})

	send(t, guest, cipher, `{"type":"board-del","id":"`+id+`","author":"m"}`)
	boardUntil(func(c []any) bool { return len(c) == 0 })

	// Junk: bad type, empty text.
	before := b.Dropped.Load()
	send(t, guest, cipher, `{"type":"board-add","cardType":"stickynote","text":"x","author":"m"}`)
	send(t, guest, cipher, `{"type":"board-add","cardType":"event","text":"","author":"m"}`)
	deadline := time.Now().Add(5 * time.Second)
	for b.Dropped.Load() < before+2 {
		if time.Now().After(deadline) {
			t.Fatal("board junk not dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestConfirmRequiresHostToken: guests cannot confirm or alias; the
// host capability token gates both (FR13).
func TestConfirmRequiresHostToken(t *testing.T) {
	guest, cipher, b := setup(t)
	readCtrl(t, guest, cipher, "composer-snapshot")

	send(t, guest, cipher, `{"type":"board-add","cardType":"event","text":"ClaimDenied","author":"m"}`)
	var id string
	deadline := time.Now().Add(5 * time.Second)
	for id == "" {
		if time.Now().After(deadline) {
			t.Fatal("no card")
		}
		st := readCtrl(t, guest, cipher, "board-state")
		if cards, _ := st["cards"].([]any); len(cards) == 1 {
			id = cards[0].(map[string]any)["id"].(string)
		}
	}

	// No token / wrong token: dropped, card stays proposed.
	before := b.Dropped.Load()
	send(t, guest, cipher, `{"type":"board-confirm","id":"`+id+`","author":"m"}`)
	send(t, guest, cipher, `{"type":"board-confirm","id":"`+id+`","author":"m","token":"guessed"}`)
	send(t, guest, cipher, `{"type":"board-alias","id":"`+id+`","text":"Sneaky","author":"m","token":""}`)
	for b.Dropped.Load() < before+3 {
		if time.Now().After(deadline) {
			t.Fatal("unauthorized confirms not dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.Board.Cards()[0].State != "proposed" {
		t.Fatal("card confirmed without host token")
	}

	// Correct token confirms and sets the alias.
	send(t, guest, cipher, `{"type":"board-confirm","id":"`+id+`","author":"host","token":"`+b.HostToken+`"}`)
	send(t, guest, cipher, `{"type":"board-alias","id":"`+id+`","text":"ClaimDeniedEvent","author":"host","token":"`+b.HostToken+`"}`)
	for {
		if time.Now().After(deadline) {
			t.Fatal("confirm with token failed")
		}
		c := b.Board.Cards()[0]
		if c.State == "confirmed" && c.CodeName == "ClaimDeniedEvent" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}
