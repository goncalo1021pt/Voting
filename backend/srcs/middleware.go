package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// maxRequestBytes caps the size of any request body. It is generous enough for
// the JSON event-import payload (many categories/options) while preventing a
// single request from exhausting memory.
const maxRequestBytes = 1 << 20 // 1 MiB

// MaxBytesMiddleware wraps every request body in an http.MaxBytesReader so a
// malicious or buggy client cannot stream an unbounded payload. Handlers that
// decode JSON will surface a clean 400 when the limit is exceeded.
func MaxBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// --- Rate limiting -----------------------------------------------------------

// rateLimiter is a tiny fixed-window, per-key request limiter. It is process
// local (no external dependency) and intended as a first line of defence
// against credential brute-forcing on the auth endpoints. Cloudflare rate
// rules in front of the tunnel provide defence-in-depth.
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		hits:   make(map[string][]time.Time),
		limit:  limit,
		window: window,
	}
}

// allow reports whether the key may make another request right now, recording
// the attempt if so. now is passed in to keep the logic testable.
func (rl *rateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := now.Add(-rl.window)
	recent := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= rl.limit {
		rl.hits[key] = recent
		return false
	}
	rl.hits[key] = append(recent, now)
	return true
}

// authLimiter throttles login/register: 10 attempts per IP per 5 minutes.
var authLimiter = newRateLimiter(10, 5*time.Minute)

// clientIP resolves the caller's address. Because the app is only reachable
// through the Cloudflare Tunnel (no open inbound ports), CF-Connecting-IP can
// be trusted when present; otherwise fall back to the socket address.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit wraps a handler, rejecting callers that exceed authLimiter with a
// 429 before any expensive work (DB lookup, bcrypt) runs.
func RateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authLimiter.allow(clientIP(r), time.Now()) {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "Too many requests, please try again later", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
