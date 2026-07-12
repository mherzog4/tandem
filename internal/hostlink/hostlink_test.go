package hostlink

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mherzog4/tandem/internal/e2e"
	"github.com/mherzog4/tandem/internal/relay"
)

type testSession struct {
	wsBase string
	link   *Link
	cipher *e2e.Cipher
	id     string
}

func newTestSession(t *testing.T) *testSession {
	t.Helper()
	srv := httptest.NewServer(relay.NewServer("http://relay.test"))
	t.Cleanup(srv.Close)
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	link, err := Connect(ctx, wsBase)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Close() })

	frag := link.JoinURL[strings.Index(link.JoinURL, "#")+1:]
	key, err := e2e.DecodeKey(strings.TrimPrefix(frag, e2e.FragmentParam+"="))
	if err != nil {
		t.Fatalf("join URL fragment key: %v", err)
	}
	cipher, _ := e2e.NewCipher(key)
	id := link.JoinURL[strings.LastIndex(link.JoinURL, "/s/")+3 : strings.Index(link.JoinURL, "#")]
	return &testSession{wsBase: wsBase, link: link, cipher: cipher, id: id}
}

func (ts *testSession) joinGuest(t *testing.T, name string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guest, _, err := websocket.Dial(ctx, ts.wsBase+"/ws/join/"+ts.id+"?name="+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guest.Close(websocket.StatusNormalClosure, "") })
	return guest
}

// readFrame reads binary frames until one decrypts with the wanted kind.
func (ts *testSession) readFrame(t *testing.T, guest *websocket.Conn, wantKind byte) []byte {
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
		plain, err := ts.cipher.Open(data)
		if err != nil {
			t.Fatalf("guest cannot open frame: %v", err)
		}
		if plain[0] == wantKind {
			return plain[1:]
		}
	}
}

func TestEndToEndEncryption(t *testing.T) {
	ts := newTestSession(t)
	guest := ts.joinGuest(t, "g")

	if _, err := ts.link.Write([]byte("secret terminal bytes")); err != nil {
		t.Fatal(err)
	}
	body := ts.readFrame(t, guest, FramePTY)
	if string(body) != "secret terminal bytes" {
		t.Fatalf("guest got %q", body)
	}

	// Guest -> host: sealed frame decrypts into Incoming.
	ctx := context.Background()
	if err := guest.Write(ctx, websocket.MessageBinary, ts.cipher.Seal([]byte("crdt-op"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ts.link.Incoming:
		if string(got) != "crdt-op" {
			t.Fatalf("incoming = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for guest frame")
	}

	// Forged/unsealed guest frame never reaches Incoming.
	if err := guest.Write(ctx, websocket.MessageBinary, []byte("raw garbage")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ts.link.Incoming:
		t.Fatalf("forged frame delivered: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestGuestJoinReplaysScrollback: a guest arriving after output was
// produced still sees history (NFR3 guest-refresh case).
func TestGuestJoinReplaysScrollback(t *testing.T) {
	ts := newTestSession(t)
	if _, err := ts.link.Write([]byte("before-join output")); err != nil {
		t.Fatal(err)
	}

	guest := ts.joinGuest(t, "late")
	body := ts.readFrame(t, guest, FrameReplay)
	if !strings.Contains(string(body), "before-join output") {
		t.Fatalf("replay missing scrollback, got %q", body)
	}
}

// TestWriteNeverBlocks: writes with no relay consumer return promptly,
// so a relay outage can't freeze the host PTY tee.
func TestWriteNeverBlocks(t *testing.T) {
	ts := newTestSession(t)
	done := make(chan struct{})
	go func() {
		for i := 0; i < outboundBuf*3; i++ {
			_, _ = ts.link.Write([]byte("frame"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked under backpressure")
	}
}
