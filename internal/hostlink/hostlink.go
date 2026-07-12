// Package hostlink is the host daemon's connection to the relay. It
// registers a session and streams the PTY tap to guests as binary
// frames (plaintext until issue #4 adds E2E encryption).
package hostlink

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"
)

type Link struct {
	conn    *websocket.Conn
	JoinURL string
	// Incoming carries guest->host frames (CRDT ops in M1). Drained by
	// the daemon; unread frames are dropped when the buffer fills so a
	// stalled consumer can't block the relay read loop.
	Incoming chan []byte
}

// Connect dials the relay's /ws/host endpoint and waits for the session
// hello. relayURL is the ws:// or wss:// base, e.g. ws://localhost:8080.
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

	l := &Link{conn: conn, JoinURL: hello.JoinURL, Incoming: make(chan []byte, 64)}
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
		select {
		case l.Incoming <- data:
		default: // ponytail: drop on backpressure; revisit if M1 CRDT ops ever hit this
		}
	}
}

// Write sends one opaque frame to the relay for broadcast to guests.
// Satisfies io.Writer so it can sit behind the PTY tap.
func (l *Link) Write(p []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Copy: the PTY read loop reuses its buffer.
	frame := make([]byte, len(p))
	copy(frame, p)
	if err := l.conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (l *Link) Close() error {
	return l.conn.Close(websocket.StatusNormalClosure, "session ended")
}
