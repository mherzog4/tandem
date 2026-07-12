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
	base, host, sessionID, _ = setupWithToken(t)
	return
}

func setupWithToken(t *testing.T) (base string, host *websocket.Conn, sessionID, resumeToken string) {
	t.Helper()
	srv := httptest.NewServer(NewServer("http://relay.test"))
	t.Cleanup(srv.Close)
	base = "ws" + strings.TrimPrefix(srv.URL, "http")
	host = dial(t, base+"/ws/host")
	t.Cleanup(func() { host.Close(websocket.StatusNormalClosure, "") })
	hello := readJSON(t, host)
	if hello["type"] != "session" || hello["id"] == "" || hello["resumeToken"] == "" {
		t.Fatalf("bad hello: %v", hello)
	}
	if hello["joinURL"] != "http://relay.test/s/"+hello["id"] {
		t.Fatalf("bad joinURL: %v", hello["joinURL"])
	}
	return base, host, hello["id"], hello["resumeToken"]
}

// TestHostResume: a host that drops can reclaim its session within the
// grace period using the resume token; guests keep their connection and
// receive frames from the resumed host (NFR3).
func TestHostResume(t *testing.T) {
	base, host, id, token := setupWithToken(t)

	guest := dial(t, base+"/ws/join/"+id+"?name=g")
	defer guest.Close(websocket.StatusNormalClosure, "")
	readJSON(t, guest) // presence join

	// Host connection blips.
	host.Close(websocket.StatusAbnormalClosure, "network blip")

	// Wrong token cannot hijack the session.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, base+"/ws/host?resume="+id+"&token=wrong", nil); err == nil {
		t.Fatal("resume with wrong token accepted")
	}

	// Correct token reclaims the same session ID.
	host2 := dial(t, base+"/ws/host?resume="+id+"&token="+token)
	defer host2.Close(websocket.StatusNormalClosure, "")
	hello2 := readJSON(t, host2)
	if hello2["id"] != id {
		t.Fatalf("resumed session id = %q, want %q", hello2["id"], id)
	}

	// Frames from the resumed host still reach the surviving guest.
	if err := host2.Write(ctx, websocket.MessageBinary, []byte("after-resume")); err != nil {
		t.Fatal(err)
	}
	for {
		typ, data, err := guest.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageBinary {
			if string(data) != "after-resume" {
				t.Fatalf("guest got %q", data)
			}
			break
		}
	}
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

// TestGuestIsolation: a guest's frames go to the host only — never to
// other guests (the star topology is layer 1 of the gated-input
// guarantee, see docs/protocol.md).
func TestGuestIsolation(t *testing.T) {
	base, _, id := setup(t)
	g1 := dial(t, base+"/ws/join/"+id+"?name=g1")
	defer g1.Close(websocket.StatusNormalClosure, "")
	g2 := dial(t, base+"/ws/join/"+id+"?name=g2")
	defer g2.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	if err := g1.Write(ctx, websocket.MessageBinary, []byte("guest-secret")); err != nil {
		t.Fatal(err)
	}
	// g2 must see presence text frames at most — never g1's binary frame.
	readCtx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	for {
		typ, data, err := g2.Read(readCtx)
		if err != nil {
			return // timeout: nothing leaked
		}
		if typ == websocket.MessageBinary {
			t.Fatalf("guest frame leaked to another guest: %q", data)
		}
	}
}

// TestLargeReplayFrame: a 1 MiB host frame (scrollback replay scale)
// traverses the relay without tripping read limits.
func TestLargeReplayFrame(t *testing.T) {
	base, host, id := setup(t)
	guest := dial(t, base+"/ws/join/"+id+"?name=g")
	defer guest.Close(websocket.StatusNormalClosure, "")
	guest.SetReadLimit(4 << 20) // browsers impose no read limit; the Go test client does

	big := make([]byte, 1<<20)
	for i := range big {
		big[i] = byte(i)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := host.Write(ctx, websocket.MessageBinary, big); err != nil {
		t.Fatal(err)
	}
	for {
		typ, data, err := guest.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageBinary {
			if len(data) != len(big) {
				t.Fatalf("got %d bytes, want %d", len(data), len(big))
			}
			return
		}
	}
}

// TestEmailAllowlist: once the host registers an allowlist, joins are
// rejected at the HTTP layer unless the claimed email matches (FR22).
func TestEmailAllowlist(t *testing.T) {
	base, host, id := setup(t)

	ctx := context.Background()
	if err := host.Write(ctx, websocket.MessageText, []byte(`{"type":"allowlist","emails":["Marcus@Example.com "]}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // relay processes the instruction

	dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// No email / wrong email: rejected.
	if _, _, err := websocket.Dial(dctx, base+"/ws/join/"+id+"?name=g", nil); err == nil {
		t.Fatal("join without email accepted")
	}
	if _, _, err := websocket.Dial(dctx, base+"/ws/join/"+id+"?name=g&email=evil@x.com", nil); err == nil {
		t.Fatal("join with wrong email accepted")
	}
	// Matching email (case/space-insensitive): accepted.
	g := dial(t, base+"/ws/join/"+id+"?name=g&email=marcus%40example.com")
	g.Close(websocket.StatusNormalClosure, "")
}
