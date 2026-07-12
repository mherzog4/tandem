// Package relay implements the stateless session relay (PRD §8.1 #2).
//
// The relay forwards frames between one host and its guests and holds no
// session content: binary WebSocket messages are opaque payloads passed
// through verbatim (ciphertext once issue #4 lands). The only messages
// the relay itself originates are text-frame presence events. Nothing is
// persisted; a relay restart drops sessions and hosts reconnect (NFR3 is
// handled host-side).
package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/mherzog4/tandem/web"
)

// MaxParticipants counts the host plus guests (PRD non-goal 2: no more
// than three participants per session).
const MaxParticipants = 3

// Presence is the only relay-originated message, sent as a text frame so
// clients can distinguish it from opaque binary payloads.
type Presence struct {
	Type  string `json:"type"`  // always "presence"
	Event string `json:"event"` // "join" or "leave"
	Name  string `json:"name"`
}

// HostGracePeriod is how long a session outlives a disconnected host so
// a network blip doesn't kill guest links (NFR3). The host reclaims the
// session with the resume token from its hello.
const HostGracePeriod = 2 * time.Minute

type session struct {
	mu          sync.Mutex
	host        *websocket.Conn            // nil while the host is disconnected
	guests      map[*websocket.Conn]string // conn -> display name
	resumeToken string
	reapTimer   *time.Timer // armed while host is disconnected
	// allowedEmails, when non-empty, restricts who may join (FR22).
	// Claimed, not verified — see docs/protocol.md.
	allowedEmails map[string]bool
}

// Server is an http.Handler serving the relay protocol.
type Server struct {
	mu       sync.Mutex
	sessions map[string]*session
	// BaseURL is the externally visible URL used to build join links.
	BaseURL string
	// MaxSessions caps concurrent sessions (0 = unlimited).
	MaxSessions int
	limiter     *ipLimiter
	// Logf logs operational events; defaults to the stdlib logger.
	Logf func(format string, args ...any)
}

// NewServer builds a relay. Caps are read from the environment for a
// public deployment: TANDEM_MAX_SESSIONS, TANDEM_CONN_PER_MIN,
// TANDEM_CONN_BURST (all optional).
func NewServer(baseURL string) *Server {
	return &Server{
		sessions:    make(map[string]*session),
		BaseURL:     strings.TrimSuffix(baseURL, "/"),
		MaxSessions: envInt(os.Getenv, "TANDEM_MAX_SESSIONS", DefaultMaxSessions),
		limiter: newIPLimiter(
			envInt(os.Getenv, "TANDEM_CONN_PER_MIN", DefaultConnPerMinIP),
			envInt(os.Getenv, "TANDEM_CONN_BURST", DefaultConnBurstIP),
		),
		Logf: log.Printf,
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Per-IP connection rate limit on the WebSocket endpoints (a public
	// relay is an open endpoint). Static assets and /healthz are exempt.
	if strings.HasPrefix(r.URL.Path, "/ws/") {
		if ip := clientIP(r); !s.limiter.allow(ip) {
			s.logf("relay: rate-limited %s from %s", r.URL.Path, ip)
			http.Error(w, "too many connections, slow down", http.StatusTooManyRequests)
			return
		}
	}

	switch {
	case r.URL.Path == "/ws/host":
		s.serveHost(w, r)
	case strings.HasPrefix(r.URL.Path, "/ws/join/"):
		s.serveGuest(w, r, strings.TrimPrefix(r.URL.Path, "/ws/join/"))
	case strings.HasPrefix(r.URL.Path, "/s/"):
		// Guest client page. The session key rides the URL fragment,
		// which the browser never sends here.
		http.ServeFileFS(w, r, web.Assets, "index.html")
	case r.URL.Path == "/player":
		// Local replay tool: loads a .cast file the host opens; no
		// session state involved.
		http.ServeFileFS(w, r, web.Assets, "player.html")
	case strings.HasPrefix(r.URL.Path, "/static/"):
		http.StripPrefix("/static/", http.FileServerFS(web.Assets)).ServeHTTP(w, r)
	case r.URL.Path == "/healthz":
		fmt.Fprintln(w, "ok")
	default:
		http.NotFound(w, r)
	}
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) serveHost(w http.ResponseWriter, r *http.Request) {
	resumeID := r.URL.Query().Get("resume")
	resumeToken := r.URL.Query().Get("token")

	var (
		id   string
		sess *session
	)
	if resumeID != "" {
		// Reclaim an existing session within the grace period. Token
		// must match: the session ID alone is in every guest's URL and
		// must not grant host powers.
		s.mu.Lock()
		cand := s.sessions[resumeID]
		s.mu.Unlock()
		if cand == nil {
			http.Error(w, "no such session", http.StatusNotFound)
			return
		}
		cand.mu.Lock()
		ok := cand.host == nil && cand.resumeToken == resumeToken && resumeToken != ""
		cand.mu.Unlock()
		if !ok {
			http.Error(w, "resume rejected", http.StatusForbidden)
			return
		}
		id, sess = resumeID, cand
	}

	// Global session cap, checked before accepting a NEW session (resumes
	// reclaim an existing one and are exempt).
	if sess == nil && s.MaxSessions > 0 {
		s.mu.Lock()
		full := len(s.sessions) >= s.MaxSessions
		s.mu.Unlock()
		if full {
			s.logf("relay: session cap %d reached, rejecting host from %s", s.MaxSessions, clientIP(r))
			http.Error(w, "relay at capacity, try again later", http.StatusServiceUnavailable)
			return
		}
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// Host frames include scrollback replays (up to ~2 MiB plaintext
	// plus seal overhead); the library default of 32 KiB would kill the
	// connection mid-replay. Guests keep the small default — they only
	// ever send composer ops.
	conn.SetReadLimit(4 << 20)
	// Keepalive: detect a dead host (network gone, no TCP FIN) so its
	// session doesn't linger. A live-but-quiet host (agent thinking)
	// answers pings and is not reaped.
	go keepalive(conn)

	if sess == nil {
		id = newSessionID()
		sess = &session{host: conn, guests: make(map[*websocket.Conn]string), resumeToken: newSessionID()}
		s.mu.Lock()
		s.sessions[id] = sess
		s.mu.Unlock()
		s.logf("relay: session %s created from %s", id, clientIP(r))
	} else {
		sess.mu.Lock()
		if sess.reapTimer != nil {
			sess.reapTimer.Stop()
			sess.reapTimer = nil
		}
		sess.host = conn
		sess.mu.Unlock()
	}

	// On disconnect, keep the session for the grace period instead of
	// tearing it down; only reap (and drop guests) if the host never
	// comes back.
	defer func() {
		sess.mu.Lock()
		sess.host = nil
		sess.reapTimer = time.AfterFunc(HostGracePeriod, func() {
			s.mu.Lock()
			delete(s.sessions, id)
			s.mu.Unlock()
			sess.mu.Lock()
			for g := range sess.guests {
				g.Close(websocket.StatusGoingAway, "host left")
			}
			sess.mu.Unlock()
			s.logf("relay: session %s reaped (host did not return)", id)
		})
		sess.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	// Tell the host its session ID, join link, and resume token.
	sess.mu.Lock()
	token := sess.resumeToken
	sess.mu.Unlock()
	hello, _ := json.Marshal(map[string]string{
		"type": "session", "id": id, "joinURL": s.BaseURL + "/s/" + id, "resumeToken": token,
	})
	ctx := r.Context()
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		return
	}

	// Host -> all guests, verbatim. One exception: a text frame of type
	// "allowlist" is a host->relay instruction (FR22), consumed here.
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			var msg struct {
				Type   string   `json:"type"`
				Emails []string `json:"emails"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "allowlist" {
				allowed := make(map[string]bool, len(msg.Emails))
				for _, e := range msg.Emails {
					allowed[strings.ToLower(strings.TrimSpace(e))] = true
				}
				sess.mu.Lock()
				sess.allowedEmails = allowed
				sess.mu.Unlock()
				continue
			}
		}
		sess.mu.Lock()
		for g := range sess.guests {
			_ = writeTimeout(g, typ, data)
		}
		sess.mu.Unlock()
	}
}

func (s *Server) serveGuest(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess == nil {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "guest"
	}

	// Email allowlist (FR22): rejected before the WebSocket upgrade.
	sess.mu.Lock()
	allowed := sess.allowedEmails
	sess.mu.Unlock()
	if len(allowed) > 0 {
		email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
		if !allowed[email] {
			http.Error(w, "not on this session's guest list", http.StatusForbidden)
			return
		}
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	go keepalive(conn) // reap a dead guest conn

	sess.mu.Lock()
	if 1+len(sess.guests) >= MaxParticipants {
		sess.mu.Unlock()
		conn.Close(websocket.StatusPolicyViolation, "session full")
		return
	}
	sess.guests[conn] = name
	// Presence events only flow forward, so tell the newcomer who is
	// already here (including itself) before announcing the join.
	names := make([]string, 0, len(sess.guests))
	for _, n := range sess.guests {
		names = append(names, n)
	}
	if rmsg, err := json.Marshal(map[string]any{"type": "roster", "names": names}); err == nil {
		_ = writeTimeout(conn, websocket.MessageText, rmsg)
	}
	s.broadcastPresenceLocked(sess, Presence{Type: "presence", Event: "join", Name: name})
	sess.mu.Unlock()
	s.logf("relay: guest %q joined %s", name, id)

	defer func() {
		sess.mu.Lock()
		delete(sess.guests, conn)
		s.broadcastPresenceLocked(sess, Presence{Type: "presence", Event: "leave", Name: name})
		sess.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
		s.logf("relay: guest %q left %s", name, id)
	}()

	// Guest -> host only, verbatim. Guests never reach each other directly.
	ctx := r.Context()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		sess.mu.Lock()
		host := sess.host
		sess.mu.Unlock()
		if host != nil {
			_ = writeTimeout(host, typ, data)
		}
	}
}

// broadcastPresenceLocked sends a presence event to the host and every
// guest. Caller holds sess.mu.
func (s *Server) broadcastPresenceLocked(sess *session, p Presence) {
	msg, _ := json.Marshal(p)
	if sess.host != nil {
		_ = writeTimeout(sess.host, websocket.MessageText, msg)
	}
	for g := range sess.guests {
		_ = writeTimeout(g, websocket.MessageText, msg)
	}
}

func writeTimeout(c *websocket.Conn, typ websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Write(ctx, typ, data)
}
