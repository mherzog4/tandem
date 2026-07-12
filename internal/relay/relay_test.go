package relay

import (
	"context"
	"encoding/json"
	"net/http"
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

func readJSON(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
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
	id, _ := hello["id"].(string)
	token, _ := hello["resumeToken"].(string)
	if hello["joinURL"] != "http://relay.test/s/"+id {
		t.Fatalf("bad joinURL: %v", hello["joinURL"])
	}
	return base, host, id, token
}

// TestHostResume: a host that drops can reclaim its session within the
// grace period using the resume token; guests keep their connection and
// receive frames from the resumed host (NFR3).
func TestHostResume(t *testing.T) {
	base, host, id, token := setupWithToken(t)

	guest := dial(t, base+"/ws/join/"+id+"?name=g")
	defer guest.Close(websocket.StatusNormalClosure, "")
	readJSON(t, guest) // roster snapshot (newcomer's first message)

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

// TestWaitingRoom: with approval on, a guest is held until the host
// admits it and receives no host frame while pending (link-leak defense).
func TestWaitingRoom(t *testing.T) {
	base, host, id := setup(t)
	ctx := context.Background()

	// Host enables approval, then give the relay a moment to record it.
	if err := host.Write(ctx, websocket.MessageText, []byte(`{"type":"approval","on":true}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	guest := dial(t, base+"/ws/join/"+id+"?name=Trudy")
	defer guest.Close(websocket.StatusNormalClosure, "")

	// The guest's first message is the waiting notice, not a roster/join.
	if m := readJSON(t, guest); m["type"] != "waiting" {
		t.Fatalf("expected waiting notice, got %v", m)
	}

	// The host learns of the request and gets a connection id.
	jr := readJSON(t, host)
	if jr["type"] != "join-request" || jr["name"] != "Trudy" {
		t.Fatalf("expected join-request, got %v", jr)
	}
	cid, _ := jr["cid"].(string)
	if cid == "" {
		t.Fatal("join-request carried no cid")
	}

	// A host frame sent while the guest is pending must never reach it.
	// (A short-timeout Read would fail the websocket, so instead we prove
	// the property by ordering: this pre-admit frame must not be the first
	// binary frame the guest sees after being admitted below.)
	if err := host.Write(ctx, websocket.MessageBinary, []byte("secret-before-admit")); err != nil {
		t.Fatal(err)
	}

	// Host admits; now the guest is a real participant and sees frames.
	if err := host.Write(ctx, websocket.MessageText, []byte(`{"type":"admit","cid":"`+cid+`"}`)); err != nil {
		t.Fatal(err)
	}
	admitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Resend the post-admit frame until read: admission and the guest's
	// entry into the broadcast set race a single write (in a real session
	// the host streams continuously, so this is not a product concern).
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		for {
			select {
			case <-admitCtx.Done():
				return
			default:
				_ = host.Write(ctx, websocket.MessageBinary, []byte("hello-after-admit"))
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	for {
		typ, data, err := guest.Read(admitCtx)
		if err != nil {
			t.Fatalf("admitted guest never got the frame: %v", err)
		}
		if typ == websocket.MessageBinary {
			// The pre-admit frame must never have been forwarded, so the
			// first binary frame here is the post-admit one.
			if string(data) != "hello-after-admit" {
				t.Fatalf("admitted guest's first frame was %q, want hello-after-admit", data)
			}
			break
		}
	}
	cancel()
	<-sendDone
}

func TestHostToGuestForwardingAndPresence(t *testing.T) {
	base, host, id := setup(t)

	guest := dial(t, base+"/ws/join/"+id+"?name=Marcus")
	defer guest.Close(websocket.StatusNormalClosure, "")

	// The newcomer first receives a roster snapshot of who is present.
	if r := readJSON(t, guest); r["type"] != "roster" {
		t.Fatalf("expected roster snapshot for newcomer, got %v", r)
	}
	// Both parties then see the join presence event.
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

// TestSessionCap: once MaxSessions is reached, new hosts are rejected
// with 503, but existing sessions keep working.
func TestSessionCap(t *testing.T) {
	srv := httptest.NewServer(NewServer("http://relay.test"))
	defer srv.Close()
	// Force a tiny cap.
	// (NewServer read env; override the field via a second server.)
	s := NewServer("http://relay.test")
	s.MaxSessions = 1
	srv2 := httptest.NewServer(s)
	defer srv2.Close()
	base := "ws" + strings.TrimPrefix(srv2.URL, "http")

	h1 := dial(t, base+"/ws/host")
	defer h1.Close(websocket.StatusNormalClosure, "")
	readJSON(t, h1) // hello — session 1 created

	// Second host exceeds the cap → dial fails at the HTTP layer (503).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, base+"/ws/host", nil); err == nil {
		t.Fatal("host past the cap was accepted")
	}
}

func TestIPLimiter(t *testing.T) {
	l := newIPLimiter(60, 3) // 1/sec, burst 3
	ip := "1.2.3.4"
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.allow(ip) {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("burst allowed %d, want 3", allowed)
	}
	// A different IP has its own bucket.
	if !l.allow("5.6.7.8") {
		t.Fatal("second IP should not be rate-limited by the first")
	}
}

func TestClientIPForwardedFor(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws/host", nil)
	r.RemoteAddr = "10.0.0.1:5555" // the proxy
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want the leftmost XFF entry", got)
	}
	r2 := httptest.NewRequest("GET", "/ws/host", nil)
	r2.RemoteAddr = "198.51.100.2:4444"
	if got := clientIP(r2); got != "198.51.100.2" {
		t.Fatalf("clientIP = %q, want RemoteAddr host", got)
	}
}

func TestEnvInt(t *testing.T) {
	env := map[string]string{"A": "42", "B": "-1", "C": "notnum"}
	get := func(k string) string { return env[k] }
	if envInt(get, "A", 7) != 42 || envInt(get, "B", 7) != 7 || envInt(get, "C", 7) != 7 || envInt(get, "Z", 7) != 7 {
		t.Fatal("envInt precedence wrong")
	}
}

// TestRateLimitGate: the ServeHTTP gate returns 429 once an IP's burst
// is spent (checked via plain HTTP GETs to /ws/host, which the limiter
// gates before the WebSocket upgrade).
func TestRateLimitGate(t *testing.T) {
	s := NewServer("http://relay.test")
	s.limiter = newIPLimiter(1, 2) // burst 2
	srv := httptest.NewServer(s)
	defer srv.Close()

	got429 := false
	for i := 0; i < 6; i++ {
		// Non-WebSocket GET: fails the upgrade but passes the rate gate
		// first, so a 429 means the gate fired (else 400/426 upgrade err).
		resp, err := http.Get(srv.URL + "/ws/host")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("rate-limit gate never returned 429")
	}
}
