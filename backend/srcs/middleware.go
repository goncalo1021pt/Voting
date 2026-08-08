package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
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

// defaultTrustedProxyCIDRs covers loopback and the Docker bridge ranges — the
// only paths by which cloudflared, which runs on the host, reaches the
// container. Everything else, the LAN included, is untrusted: a request
// arriving straight from the LAN did not come through Cloudflare, so its
// CF-Connecting-IP is a claim rather than a fact.
const defaultTrustedProxyCIDRs = "127.0.0.0/8,::1/128,172.16.0.0/12"

// trustedProxies is the parsed form of TRUSTED_PROXY_CIDRS, set by
// InitTrustedProxies at startup.
var trustedProxies []*net.IPNet

// InitTrustedProxies parses the set of peer addresses whose forwarded-IP
// headers are believed. Override with TRUSTED_PROXY_CIDRS (comma-separated)
// when the deployment topology differs — in particular, tighten it if the
// backend port is ever published beyond loopback.
func InitTrustedProxies() error {
	spec := os.Getenv("TRUSTED_PROXY_CIDRS")
	if spec == "" {
		spec = defaultTrustedProxyCIDRs
	}

	var nets []*net.IPNet
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return fmt.Errorf("TRUSTED_PROXY_CIDRS: %q is not a valid CIDR: %w", entry, err)
		}
		nets = append(nets, network)
	}

	trustedProxies = nets
	return nil
}

// isTrustedProxy reports whether a connection from host may speak for someone
// else via CF-Connecting-IP.
func isTrustedProxy(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP resolves the caller's address, used for rate-limit keying and the
// access log.
//
// CF-Connecting-IP is only honoured when the peer is a trusted proxy. Trusting
// it unconditionally let anyone who could open a socket to the backend present
// a fresh IP per request and walk past the auth rate limiter — unlimited
// credential attempts. The value must also parse as an IP: it reaches the log
// and the limiter's key space, and neither should hold arbitrary client text.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if claimed := r.Header.Get("CF-Connecting-IP"); claimed != "" && isTrustedProxy(host) {
		if ip := net.ParseIP(strings.TrimSpace(claimed)); ip != nil {
			return ip.String()
		}
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
