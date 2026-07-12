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

// TestEndToEndEncryption drives host -> relay -> guest and back,
// asserting the relay path carries ciphertext only and that a guest
// holding the fragment key can decrypt.
func TestEndToEndEncryption(t *testing.T) {
	srv := httptest.NewServer(relay.NewServer("http://relay.test"))
	defer srv.Close()
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	link, err := Connect(ctx, wsBase)
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()

	// Key is in the fragment, so it never reaches the relay in a request.
	frag := link.JoinURL[strings.Index(link.JoinURL, "#")+1:]
	keyStr := strings.TrimPrefix(frag, e2e.FragmentParam+"=")
	key, err := e2e.DecodeKey(keyStr)
	if err != nil {
		t.Fatalf("join URL fragment key: %v", err)
	}
	guestCipher, _ := e2e.NewCipher(key)

	// Guest joins via the relay path portion of the URL.
	id := link.JoinURL[strings.LastIndex(link.JoinURL, "/s/")+3 : strings.Index(link.JoinURL, "#")]
	guest, _, err := websocket.Dial(ctx, wsBase+"/ws/join/"+id+"?name=g", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close(websocket.StatusNormalClosure, "")

	// Host -> guest: sealed on the wire, opens with the fragment key.
	if _, err := link.Write([]byte("secret terminal bytes")); err != nil {
		t.Fatal(err)
	}
	var wire []byte
	for {
		typ, data, err := guest.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageBinary {
			wire = data
			break
		} // skip presence text frames
	}
	if strings.Contains(string(wire), "secret terminal bytes") {
		t.Fatal("plaintext on the wire")
	}
	plain, err := guestCipher.Open(wire)
	if err != nil || string(plain) != "secret terminal bytes" {
		t.Fatalf("guest decrypt: %q err=%v", plain, err)
	}

	// Guest -> host: sealed frame decrypts into Incoming.
	if err := guest.Write(ctx, websocket.MessageBinary, guestCipher.Seal([]byte("crdt-op"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-link.Incoming:
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
	case got := <-link.Incoming:
		t.Fatalf("forged frame delivered: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
}
