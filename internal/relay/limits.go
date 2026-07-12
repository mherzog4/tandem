package relay

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"
)

// Defaults for a public relay; overridable via env in NewServer.
const (
	DefaultMaxSessions  = 200 // concurrent sessions cap
	DefaultConnPerMinIP = 30  // new connections per minute per IP
	DefaultConnBurstIP  = 10  // burst allowance per IP
	pingInterval        = 30 * time.Second
	pingTimeout         = 10 * time.Second
)

// ipLimiter rate-limits new connections per client IP. Idle limiters are
// swept so the map doesn't grow unbounded on a public endpoint.
type ipLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipEntry
	perMin   rate.Limit
	burst    int
}

type ipEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

func newIPLimiter(perMin, burst int) *ipLimiter {
	l := &ipLimiter{
		limiters: make(map[string]*ipEntry),
		perMin:   rate.Limit(float64(perMin) / 60.0),
		burst:    burst,
	}
	go l.sweep()
	return l
}

// allow reports whether a new connection from ip is permitted now.
func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	e := l.limiters[ip]
	if e == nil {
		e = &ipEntry{lim: rate.NewLimiter(l.perMin, l.burst)}
		l.limiters[ip] = e
	}
	e.seen = time.Now()
	l.mu.Unlock()
	return e.lim.Allow()
}

// sweep drops limiters unused for 10 minutes.
func (l *ipLimiter) sweep() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		l.mu.Lock()
		for ip, e := range l.limiters {
			if e.seen.Before(cutoff) {
				delete(l.limiters, ip)
			}
		}
		l.mu.Unlock()
	}
}

// clientIP extracts the caller's IP, honoring X-Forwarded-For since the
// relay runs behind a proxy (Railway) — RemoteAddr would otherwise be the
// proxy for every client, collapsing them into one limiter.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Leftmost entry is the original client.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// keepalive pings conn periodically; on a failed ping (dead peer) it
// closes the conn, which unblocks the read loop and cleans up the
// session. Returns when the conn is gone.
func keepalive(conn interface {
	Ping(ctx context.Context) error
	Close(code websocket.StatusCode, reason string) error
}) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for range t.C {
		ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
		err := conn.Ping(ctx)
		cancel()
		if err != nil {
			_ = conn.Close(websocket.StatusGoingAway, "ping timeout")
			return
		}
	}
}

func envInt(getenv func(string) string, key string, def int) int {
	if v := getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}
