package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func securityHeadersOn(t *testing.T, req *http.Request) http.Header {
	t.Helper()

	rec := httptest.NewRecorder()
	SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)
	return rec.Header()
}

func TestSecurityHeadersAreSet(t *testing.T) {
	h := securityHeadersOn(t, httptest.NewRequest("GET", "/", nil))

	want := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "same-origin",
		"X-Frame-Options":            "DENY",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for header, value := range want {
		if got := h.Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if h.Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy not set")
	}
}

// The CSP has to permit exactly what the shipped frontend needs and no more.
func TestContentSecurityPolicyCoversTheFrontendsNeeds(t *testing.T) {
	csp := contentSecurityPolicy("abc123")

	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self' 'nonce-abc123'",
		"https://fonts.googleapis.com", // styles.css @imports it
		"font-src 'self' https://fonts.gstatic.com",
		"img-src 'self' data:", // inlined SVG noise textures
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing %q\ngot: %s", directive, csp)
		}
	}

	// The whole point: an injected inline script must not run.
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("script-src must not allow 'unsafe-inline'")
	}
}

// If randomness fails the nonce is empty, and the policy must then omit the
// nonce source rather than emit 'nonce-' and let anything through.
func TestContentSecurityPolicyWithoutNonce(t *testing.T) {
	csp := contentSecurityPolicy("")

	if strings.Contains(csp, "nonce-") {
		t.Errorf("empty nonce should not appear as a source: %s", csp)
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("script-src should still be present: %s", csp)
	}
}

// Each response gets its own nonce, and the served HTML carries the same one
// the header advertises — otherwise the inline theme script is blocked.
func TestServedIndexCarriesTheHeadersNonce(t *testing.T) {
	prev := frontendDir
	frontendDir = "../../frontend"
	t.Cleanup(func() { frontendDir = prev })

	handler := SecurityHeadersMiddleware(http.HandlerFunc(RouteHandler))

	seen := make(map[string]bool)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		csp := rec.Header().Get("Content-Security-Policy")
		start := strings.Index(csp, "'nonce-")
		if start < 0 {
			t.Fatalf("no nonce in CSP: %s", csp)
		}
		nonce := csp[start+len("'nonce-"):]
		nonce = nonce[:strings.Index(nonce, "'")]

		if seen[nonce] {
			t.Error("nonce was reused across responses")
		}
		seen[nonce] = true

		if !strings.Contains(rec.Body.String(), `<script nonce="`+nonce+`">`) {
			t.Errorf("served HTML does not carry nonce %q", nonce)
		}
		// The external script tag must not have been rewritten.
		if !strings.Contains(rec.Body.String(), `src="/app.js`) {
			t.Error("external script tag was mangled")
		}
	}
}

func TestCORSSilentByDefault(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "")
	InitAllowedOrigins()

	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want none", got)
	}
}

func TestCORSEchoesOnlyAllowedOrigins(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://vote.fontao.net, https://staging.example")
	InitAllowedOrigins()
	t.Cleanup(func() { t.Setenv("ALLOWED_ORIGINS", ""); InitAllowedOrigins() })

	tests := []struct {
		origin string
		want   string
	}{
		{"https://vote.fontao.net", "https://vote.fontao.net"},
		{"https://staging.example", "https://staging.example"},
		{"https://evil.example", ""},
		{"", ""},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/events", nil)
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		rec := httptest.NewRecorder()
		CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.want {
			t.Errorf("origin %q: ACAO = %q, want %q", tt.origin, got, tt.want)
		}
		// A wildcard must never be emitted, whatever the allowlist says.
		if rec.Header().Get("Access-Control-Allow-Origin") == "*" {
			t.Error("wildcard origin emitted")
		}
	}
}

// Responses differ by Origin, so a shared cache must not serve one origin's
// response to another.
func TestCORSSetsVaryOnAllowedOrigin(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://vote.fontao.net")
	InitAllowedOrigins()
	t.Cleanup(func() { t.Setenv("ALLOWED_ORIGINS", ""); InitAllowedOrigins() })

	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Origin", "https://vote.fontao.net")
	rec := httptest.NewRecorder()
	CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", rec.Header().Get("Vary"))
	}
}

// PUT was advertised but no route implements it.
func TestCORSDoesNotAdvertisePUT(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://vote.fontao.net")
	InitAllowedOrigins()
	t.Cleanup(func() { t.Setenv("ALLOWED_ORIGINS", ""); InitAllowedOrigins() })

	req := httptest.NewRequest("OPTIONS", "/events", nil)
	req.Header.Set("Origin", "https://vote.fontao.net")
	rec := httptest.NewRecorder()
	CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)

	if methods := rec.Header().Get("Access-Control-Allow-Methods"); strings.Contains(methods, "PUT") {
		t.Errorf("Allow-Methods = %q, should not advertise PUT", methods)
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
}
