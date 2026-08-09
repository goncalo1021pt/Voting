package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// The leak: keys were only pruned when the same key came back, so a client
// seen once stayed resident for the life of the process.
func TestSweepDropsKeysThatHaveAgedOut(t *testing.T) {
	rl := newRateLimiter(10, time.Minute)
	base := time.Unix(1000, 0)

	for i := 0; i < 500; i++ {
		rl.allow(fakeIP(i%250), base)
	}
	if got := rl.size(); got != 250 {
		t.Fatalf("tracked %d keys, want 250", got)
	}

	// Still inside the window: nothing should be dropped.
	if removed := rl.sweep(base.Add(30 * time.Second)); removed != 0 {
		t.Errorf("swept %d keys while still in-window, want 0", removed)
	}
	if got := rl.size(); got != 250 {
		t.Errorf("tracked %d keys after an in-window sweep, want 250", got)
	}

	// Past the window: all of them.
	if removed := rl.sweep(base.Add(2 * time.Minute)); removed != 250 {
		t.Errorf("swept %d keys, want 250", removed)
	}
	if got := rl.size(); got != 0 {
		t.Errorf("tracked %d keys after sweeping, want 0", got)
	}
}

// A sweep must not hand a still-throttled caller a fresh budget.
func TestSweepKeepsActiveKeysAndTheirBudget(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	base := time.Unix(1000, 0)

	rl.allow("ip:1.2.3.4", base)
	rl.allow("ip:1.2.3.4", base)
	if rl.allow("ip:1.2.3.4", base) {
		t.Fatal("third request should have been denied")
	}

	rl.sweep(base.Add(10 * time.Second))

	if rl.size() != 1 {
		t.Errorf("tracked %d keys, want the active one kept", rl.size())
	}
	if rl.allow("ip:1.2.3.4", base.Add(10*time.Second)) {
		t.Error("sweep handed a throttled caller a fresh budget")
	}
}

// Partial expiry: old hits go, recent ones stay, and the key survives.
func TestSweepPrunesOldHitsWithinAKey(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	base := time.Unix(1000, 0)

	rl.allow("ip:1.2.3.4", base)
	rl.allow("ip:1.2.3.4", base.Add(60*time.Second))

	// 70s in, only the second hit is still inside the 60s window.
	rl.sweep(base.Add(70 * time.Second))

	if rl.size() != 1 {
		t.Fatalf("tracked %d keys, want 1", rl.size())
	}
	if n := len(rl.hits["ip:1.2.3.4"]); n != 1 {
		t.Errorf("kept %d hits, want 1", n)
	}
}

// Authenticated callers key on user id, so rotating IPs earns no extra budget.
func TestLimitKeyPrefersTheAuthenticatedUser(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	req := httptest.NewRequest("POST", "/events", nil)
	req.RemoteAddr = "192.168.0.110:5000"
	req = req.WithContext(context.WithValue(req.Context(), userIDContextKey, 42))

	if got := limitKey(req); got != "user:42" {
		t.Errorf("limitKey = %q, want %q", got, "user:42")
	}
}

func TestLimitKeyFallsBackToIPWhenAnonymous(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	req := httptest.NewRequest("POST", "/auth/login", nil)
	req.RemoteAddr = "192.168.0.110:5000"

	if got := limitKey(req); got != "ip:192.168.0.110" {
		t.Errorf("limitKey = %q, want %q", got, "ip:192.168.0.110")
	}
}

// Two users behind one NAT must not share a budget — that was the practical
// cost of keying everything on IP.
func TestUsersBehindOneIPGetSeparateBudgets(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	limiter := newRateLimiter(1, time.Minute)
	handler := RateLimit(limiter, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	call := func(userID int) int {
		req := httptest.NewRequest("POST", "/events", nil)
		req.RemoteAddr = "192.168.0.110:5000"
		req = req.WithContext(context.WithValue(req.Context(), userIDContextKey, userID))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}

	if code := call(1); code != http.StatusOK {
		t.Errorf("user 1 first request = %d, want 200", code)
	}
	if code := call(2); code != http.StatusOK {
		t.Errorf("user 2 first request = %d, want 200 — budgets should not be shared", code)
	}
	if code := call(1); code != http.StatusTooManyRequests {
		t.Errorf("user 1 second request = %d, want 429", code)
	}
}

// Retry-After should reflect the limiter that actually rejected the request.
func TestRetryAfterMatchesTheLimitersWindow(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	limiter := newRateLimiter(1, 5*time.Minute)
	handler := RateLimit(limiter, func(w http.ResponseWriter, r *http.Request) {})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/events", nil)
		req.RemoteAddr = "192.168.0.110:5000"
		rec := httptest.NewRecorder()
		handler(rec, req)

		if i == 1 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("second request = %d, want 429", rec.Code)
			}
			if got := rec.Header().Get("Retry-After"); got != "300" {
				t.Errorf("Retry-After = %q, want %q", got, "300")
			}
		}
	}
}

// Every write endpoint the issue lists must actually be behind a limiter.
//
// Counting hits for the specific key, not the map size: register and login
// share authLimiter, so the second would find the key already there.
func TestWriteEndpointsAreRateLimited(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	withTrustedProxies(t, "172.16.0.0/12")

	var userID int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('limited', 'limited@example.com', 'x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, err := issueSession(userID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	tests := []struct {
		name    string
		limiter *rateLimiter
		path    string
		authed  bool
	}{
		// Public paths: charged per IP, before any auth check.
		{"register", authLimiter, "/auth/register", false},
		{"login", authLimiter, "/auth/login", false},
		{"redeem invitation", redeemLimiter, "/invitations/sometoken", false},
		// Behind RequireAuth: charged per user id. An anonymous request never
		// reaches these limiters, which is the documented ordering.
		{"create event", createEventLimiter, "/events", true},
		{"create invitation", invitationLimiter, "/events/1/invitations", true},
		{"vote", votingLimiter, "/votes", true},
		{"ballot", votingLimiter, "/events/1/ballot", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "ip:192.168.0.110"
			if tt.authed {
				key = "user:" + strconv.Itoa(userID)
			}

			tt.limiter.mu.Lock()
			before := len(tt.limiter.hits[key])
			tt.limiter.mu.Unlock()

			req := httptest.NewRequest("POST", tt.path, nil)
			req.RemoteAddr = "192.168.0.110:5000"
			if tt.authed {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			RouteHandler(httptest.NewRecorder(), req)

			tt.limiter.mu.Lock()
			after := len(tt.limiter.hits[key])
			tt.limiter.mu.Unlock()

			if after != before+1 {
				t.Errorf("POST %s charged %d hits to %q, want 1", tt.path, after-before, key)
			}
		})
	}
}

// The counterpart to the ordering above: an anonymous request to a protected
// write endpoint is turned away by RequireAuth, not by the limiter.
func TestAnonymousRequestStopsAtAuthNotTheLimiter(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	withTrustedProxies(t, "172.16.0.0/12")

	before := createEventLimiter.size()

	req := httptest.NewRequest("POST", "/events", nil)
	req.RemoteAddr = "192.168.0.110:5000"
	rec := httptest.NewRecorder()
	RouteHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if createEventLimiter.size() != before {
		t.Error("anonymous request consumed limiter budget it should never have reached")
	}
}

// The sweeper is what keeps the map bounded over a long-running process.
func TestLimiterSweeperRunsAndStops(t *testing.T) {
	rl := newRateLimiter(10, time.Millisecond)
	rl.allow("ip:1.2.3.4", time.Now().Add(-time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startLimiterSweeper(ctx, 5*time.Millisecond, []*rateLimiter{rl})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rl.size() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("stale key still present after sweeping, size = %d", rl.size())
}
