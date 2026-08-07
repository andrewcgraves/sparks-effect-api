package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// clientIP extracts the request's client IP the way Railway's single-hop edge
// proxy presents it. X-Forwarded-For gains exactly one entry per hop it
// passes through; with one trusted proxy in front of this process, that
// proxy appends the real client IP as the last entry regardless of whatever a
// client put in the header itself, so the last entry is the only one worth
// trusting (SPA-198 — naive parsing here, e.g. taking the first entry, lets a
// client spoof its own rate-limit bucket by sending its own
// X-Forwarded-For).
//
// Falls back to RemoteAddr when the header is absent, which is the case for
// local dev with no proxy in front.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipRateLimiterIdleSweepThreshold is how many tracked IPs accumulate before a
// call to allow bothers sweeping idle ones. Below it, walking the map to find
// stale entries costs more than just letting it grow a little further.
const ipRateLimiterIdleSweepThreshold = 1000

// ipRateLimiter throttles requests per client IP with an independent token
// bucket per IP. It is deliberately in-process rather than backed by new
// infra (Redis, etc.) — the traffic this guards is small enough that a plain
// map with a size-triggered sweep is the "LRU" the SPA-198 spike asked for,
// without a new dependency to run and operate.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	lastSeen map[string]time.Time
	rate     rate.Limit
	burst    int
	idleTTL  time.Duration
}

func newIPRateLimiter(r rate.Limit, burst int, idleTTL time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		lastSeen: make(map[string]time.Time),
		rate:     r,
		burst:    burst,
		idleTTL:  idleTTL,
	}
}

// allow reports whether ip may make one more request now, consuming a token
// from its bucket if so.
func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, ok := l.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(l.rate, l.burst)
		l.limiters[ip] = lim
	}
	l.lastSeen[ip] = time.Now()

	if len(l.limiters) >= ipRateLimiterIdleSweepThreshold {
		l.evictIdleLocked()
	}

	return lim.Allow()
}

// evictIdleLocked drops buckets idle past idleTTL. It never evicts a bucket
// that could still be mid-throttle: idleTTL is chosen well past the time the
// bucket would have fully refilled anyway, so eviction only ever removes
// state that carried no information left to lose.
func (l *ipRateLimiter) evictIdleLocked() {
	cutoff := time.Now().Add(-l.idleTTL)
	for ip, seen := range l.lastSeen {
		if seen.Before(cutoff) {
			delete(l.limiters, ip)
			delete(l.lastSeen, ip)
		}
	}
}

// rateLimit wraps next with limiter, answering 429 for an IP over its rate
// rather than calling next.
func rateLimit(limiter *ipRateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(clientIP(r)) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	}
}
