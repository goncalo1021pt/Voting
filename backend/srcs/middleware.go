package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
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

// sweep drops keys whose hits have all aged out, returning how many went. A key
// was only ever pruned when that same key came back, so every client that
// appeared once stayed resident for the life of the process — a slow leak that
// a long-running deployment turns into a real one.
func (rl *rateLimiter) sweep(now time.Time) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := now.Add(-rl.window)
	removed := 0
	for key, hits := range rl.hits {
		recent := hits[:0]
		for _, t := range hits {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(rl.hits, key)
			removed++
			continue
		}
		rl.hits[key] = recent
	}
	return removed
}

// size reports how many keys are being tracked.
func (rl *rateLimiter) size() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.hits)
}

// The limiters, sized by what each endpoint costs and what abusing it buys.
// All are process-local; with a single backend container that is the whole
// picture, and Cloudflare rate rules in front of the tunnel are the outer
// layer. Authenticated requests key on user id, so rotating IPs buys nothing
// and a shared NAT doesn't punish bystanders.
var (
	// Credential guessing. Per IP — there is no user yet.
	authLimiter = newRateLimiter(10, 5*time.Minute)

	// One create can write ~20k rows (100 categories × 200 options), so this
	// is the most expensive thing an authenticated user can ask for.
	createEventLimiter = newRateLimiter(10, 5*time.Minute)

	// Token guessing. Redemption is authenticated, so this is per user.
	redeemLimiter = newRateLimiter(20, 5*time.Minute)

	// Issuing invites is cheap but enumerable; a host inviting a room full of
	// people one at a time is the legitimate burst to accommodate.
	invitationLimiter = newRateLimiter(60, 5*time.Minute)

	// Voting is deliberately generous: a live event means every member submits
	// at once, and per-user keying means they don't share this budget.
	votingLimiter = newRateLimiter(60, 5*time.Minute)
)

// allLimiters is what the sweeper walks.
var allLimiters = []*rateLimiter{
	authLimiter, createEventLimiter, redeemLimiter, invitationLimiter, votingLimiter,
}

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

// limitKey identifies who to charge a request to. Authenticated callers key on
// their user id so rotating IPs buys no extra budget and everyone behind one
// NAT gets their own; anonymous callers fall back to the resolved client IP.
// It prefers the id RequireAuth already resolved; only routes that do their own
// auth (invitation redemption) pay for a lookup here, and only when a token is
// actually present.
func limitKey(r *http.Request) string {
	if userID, ok := authenticatedUserID(r); ok && userID > 0 {
		return "user:" + strconv.Itoa(userID)
	}
	if userID, err := GetUserFromToken(r); err == nil && userID > 0 {
		return "user:" + strconv.Itoa(userID)
	}
	return "ip:" + clientIP(r)
}

// RateLimit wraps a handler, rejecting callers over the limiter's budget with a
// 429 before any expensive work (DB writes, bcrypt) runs.
//
// On authenticated routes it is applied inside RequireAuth, so the limiter only
// ever sees callers who presented a valid session. That is deliberate: putting
// it outside would also throttle anonymous 401s, but limitKey would then have
// to resolve the session itself, and GetUserFromToken slides the session — a
// second UPDATE on every legitimate write. Anonymous floods cost one session
// lookup and get a 401; volumetric abuse is Cloudflare's job, and the paths
// that are expensive or worth guessing (auth, redemption) are limited by IP
// ahead of any auth check.
func RateLimit(rl *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(limitKey(r), time.Now()) {
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
			http.Error(w, "Too many requests, please try again later", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// startLimiterSweeper drops aged-out keys immediately and then every interval,
// until ctx is cancelled. Mirrors startSessionSweeper. The limiters are passed
// in rather than read from the package global so a caller — a test especially —
// can sweep its own set without mutating shared state under the goroutine.
func startLimiterSweeper(ctx context.Context, interval time.Duration, limiters []*rateLimiter) {
	sweep := func() {
		total := 0
		for _, rl := range limiters {
			total += rl.sweep(time.Now())
		}
		if total > 0 {
			log.Printf("Rate limiter sweep: dropped %d stale key(s)", total)
		}
	}
	go func() {
		sweep()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sweep()
			}
		}
	}()
}
