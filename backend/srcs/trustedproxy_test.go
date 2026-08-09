package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// withTrustedProxies installs a CIDR set for the duration of a test.
func withTrustedProxies(t *testing.T, spec string) {
	t.Helper()

	prev := trustedProxies
	t.Setenv("TRUSTED_PROXY_CIDRS", spec)
	if err := InitTrustedProxies(); err != nil {
		t.Fatalf("InitTrustedProxies(%q): %v", spec, err)
	}
	t.Cleanup(func() { trustedProxies = prev })
}

func TestInitTrustedProxiesRejectsBadCIDR(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.0/8,not-a-cidr")
	if err := InitTrustedProxies(); err == nil {
		t.Fatal("expected an error for an invalid CIDR, got nil")
	}
}

func TestInitTrustedProxiesDefaultCoversDockerBridge(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	if err := InitTrustedProxies(); err != nil {
		t.Fatalf("InitTrustedProxies with default: %v", err)
	}
	t.Cleanup(func() { InitTrustedProxies() })

	// The address cloudflared's traffic arrives from, via the bridge gateway.
	if !isTrustedProxy("172.20.0.1") {
		t.Error("docker bridge gateway should be trusted by default")
	}
	if !isTrustedProxy("127.0.0.1") {
		t.Error("loopback should be trusted by default")
	}
	// The LAN must not be: traffic from there did not come via Cloudflare.
	if isTrustedProxy("192.168.0.110") {
		t.Error("LAN address should not be trusted by default")
	}
}

func TestClientIPHonoursHeaderOnlyFromTrustedPeer(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	tests := []struct {
		name       string
		remoteAddr string
		header     string
		want       string
	}{
		{"trusted peer, header believed", "172.20.0.1:5000", "203.0.113.9", "203.0.113.9"},
		{"untrusted LAN peer, header ignored", "192.168.0.110:5000", "203.0.113.9", "192.168.0.110"},
		{"untrusted peer, no header", "192.168.0.110:5000", "", "192.168.0.110"},
		{"trusted peer, no header", "172.20.0.1:5000", "", "172.20.0.1"},
		{"trusted peer, junk header falls back", "172.20.0.1:5000", "not-an-ip", "172.20.0.1"},
		{"trusted peer, header with spaces falls back", "172.20.0.1:5000", "1.1.1.1 forged", "172.20.0.1"},
		{"trusted peer, IPv6 header", "172.20.0.1:5000", "2001:db8::1", "2001:db8::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.header != "" {
				req.Header.Set("CF-Connecting-IP", tt.header)
			}
			if got := clientIP(req); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The bug this closes: rotating a spoofed CF-Connecting-IP gave an untrusted
// caller a fresh rate-limit bucket per request, so the auth limiter never
// fired and credential guessing was unbounded.
func TestSpoofedHeaderCannotEvadeRateLimitFromUntrustedPeer(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	limiter := newRateLimiter(3, time.Minute)
	now := time.Now()

	allowed := 0
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/auth/login", nil)
		req.RemoteAddr = "192.168.0.110:5000" // untrusted LAN peer
		req.Header.Set("CF-Connecting-IP", fakeIP(i))
		if limiter.allow(clientIP(req), now) {
			allowed++
		}
	}

	if allowed != 3 {
		t.Errorf("untrusted peer got %d requests through a limit of 3", allowed)
	}
}

// A trusted proxy really is speaking for different clients, so distinct
// forwarded IPs must still get their own buckets.
func TestTrustedProxyKeepsPerClientBuckets(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	limiter := newRateLimiter(3, time.Minute)
	now := time.Now()

	allowed := 0
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/auth/login", nil)
		req.RemoteAddr = "172.20.0.1:5000" // the tunnel
		req.Header.Set("CF-Connecting-IP", fakeIP(i))
		if limiter.allow(clientIP(req), now) {
			allowed++
		}
	}

	if allowed != 10 {
		t.Errorf("distinct clients behind the tunnel got %d/10 through", allowed)
	}
}

func fakeIP(i int) string {
	return fmt.Sprintf("10.0.0.%d", i+1)
}

// RateLimit itself must key off clientIP, not the raw header.
func TestRateLimitMiddlewareUsesResolvedIP(t *testing.T) {
	withTrustedProxies(t, "172.16.0.0/12")

	limiter := newRateLimiter(2, time.Minute)
	handler := RateLimit(limiter, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	statuses := make([]int, 0, 4)
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("POST", "/auth/login", nil)
		req.RemoteAddr = "192.168.0.110:5000"
		req.Header.Set("CF-Connecting-IP", fakeIP(i)) // rotating, but ignored
		rec := httptest.NewRecorder()
		handler(rec, req)
		statuses = append(statuses, rec.Code)
	}

	if statuses[2] != http.StatusTooManyRequests || statuses[3] != http.StatusTooManyRequests {
		t.Errorf("expected 429 on the 3rd and 4th request, got %v", statuses)
	}
}
