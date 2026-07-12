package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readJSON(t *testing.T, c *websocket.Conn) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("bad json %q: %v", data, err)
	}
	return m
}

func setup(t *testing.T) (base string, host *websocket.Conn, sessionID string) {
	t.Helper()
	srv := httptest.NewServer(NewServer("http://relay.test"))
	t.Cleanup(srv.Close)
	base = "ws" + strings.TrimPrefix(srv.URL, "http")
	host = dial(t, base+"/ws/host")
	t.Cleanup(func() { host.Close(websocket.StatusNormalClosure, "") })
	hello := readJSON(t, host)
	if hello["type"] != "session" || hello["id"] == "" {
		t.Fatalf("bad hello: %v", hello)
	}
	if hello["joinURL"] != "http://relay.test/s/"+hello["id"] {
		t.Fatalf("bad joinURL: %v", hello["joinURL"])
	}
	return base, host, hello["id"]
}

func TestHostToGuestForwardingAndPresence(t *testing.T) {
	base, host, id := setup(t)

	guest := dial(t, base+"/ws/join/"+id+"?name=Marcus")
	defer guest.Close(websocket.StatusNormalClosure, "")

	// Both parties see the join presence event.
	for _, c := range []*websocket.Conn{host, guest} {
		p := readJSON(t, c)
		if p["type"] != "presence" || p["event"] != "join" || p["name"] != "Marcus" {
			t.Fatalf("bad presence: %v", p)
		}
	}

	// Opaque binary frame host -> guest, verbatim.
	ctx := context.Background()
	payload := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := host.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatal(err)
	}
	typ, got, err := guest.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(got) != string(payload) {
		t.Fatalf("guest got typ=%v data=%x err=%v", typ, got, err)
	}

	// Guest -> host, verbatim.
	if err := guest.Write(ctx, websocket.MessageBinary, []byte("crdt-op")); err != nil {
		t.Fatal(err)
	}
	_, got, err = host.Read(ctx)
	if err != nil || string(got) != "crdt-op" {
		t.Fatalf("host got %q err=%v", got, err)
	}
}

func TestSessionCapAndUnknownSession(t *testing.T) {
	base, _, id := setup(t)

	g1 := dial(t, base+"/ws/join/"+id+"?name=a")
	defer g1.Close(websocket.StatusNormalClosure, "")
	g2 := dial(t, base+"/ws/join/"+id+"?name=b")
	defer g2.Close(websocket.StatusNormalClosure, "")

	// Third guest exceeds MaxParticipants (host + 2 guests) and is closed.
	g3 := dial(t, base+"/ws/join/"+id+"?name=c")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := g3.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("want policy violation close, got %v", err)
			}
			break
		}
	}

	// Unknown session ID is a 404 at the HTTP layer (dial fails).
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	if _, _, err := websocket.Dial(dctx, base+"/ws/join/nope", nil); err == nil {
		t.Fatal("dial to unknown session should fail")
	}
}
