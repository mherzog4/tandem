// Package hostlink is the host daemon's connection to the relay. It
// registers a session, mints the E2E session key, and streams the PTY
// tap to guests as sealed binary frames (FR5). The relay never sees
// plaintext or the key: the key rides the join link's URL fragment.
package hostlink

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"
	"github.com/mherzog4/tandem/internal/e2e"
)

type Link struct {
	conn    *websocket.Conn
	cipher  *e2e.Cipher
	JoinURL string // includes #k=<session key> for guests
	// Incoming carries decrypted guest->host frames (CRDT ops in M1).
	// Frames that fail to open are dropped: they are relay noise or
	// forgery, never trusted input. Unread frames are dropped when the
	// buffer fills so a stalled consumer can't block the read loop.
	Incoming chan []byte
}

// Connect dials the relay's /ws/host endpoint, waits for the session
// hello, and mints a fresh session key (a new key every session is the
// rotation policy). relayURL is the ws:// or wss:// base.
func Connect(ctx context.Context, relayURL string) (*Link, error) {
	conn, _, err := websocket.Dial(ctx, relayURL+"/ws/host", nil)
	if err != nil {
		return nil, fmt.Errorf("dial relay: %w", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		conn.Close(websocket.StatusProtocolError, "no hello")
		return nil, fmt.Errorf("read session hello: %w", err)
	}
	var hello struct {
		Type    string `json:"type"`
		JoinURL string `json:"joinURL"`
	}
	if err := json.Unmarshal(data, &hello); err != nil || hello.Type != "session" {
		conn.Close(websocket.StatusProtocolError, "bad hello")
		return nil, fmt.Errorf("bad session hello %q", data)
	}

	key := e2e.NewKey()
	cipher, err := e2e.NewCipher(key)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "cipher init")
		return nil, err
	}

	l := &Link{
		conn:     conn,
		cipher:   cipher,
		JoinURL:  hello.JoinURL + "#" + e2e.FragmentParam + "=" + e2e.EncodeKey(key),
		Incoming: make(chan []byte, 64),
	}
	go l.readLoop()
	return l, nil
}

func (l *Link) readLoop() {
	for {
		typ, data, err := l.conn.Read(context.Background())
		if err != nil {
			close(l.Incoming)
			return
		}
		if typ != websocket.MessageBinary {
			continue // presence and other relay text frames, ignored for now
		}
		plain, err := l.cipher.Open(data)
		if err != nil {
			continue // tampered or foreign frame: drop, never deliver
		}
		select {
		case l.Incoming <- plain:
		default: // ponytail: drop on backpressure; revisit if M1 CRDT ops ever hit this
		}
	}
}

// Write seals one frame and sends it to the relay for broadcast to
// guests. Satisfies io.Writer so it can sit behind the PTY tap.
func (l *Link) Write(p []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.conn.Write(ctx, websocket.MessageBinary, l.cipher.Seal(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (l *Link) Close() error {
	return l.conn.Close(websocket.StatusNormalClosure, "session ended")
}
