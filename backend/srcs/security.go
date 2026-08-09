package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
)

const scriptNonceContextKey contextKey = iota + 1

// newScriptNonce returns a fresh CSP nonce. A failure to read randomness must
// not produce a guessable nonce, so it returns "" and the caller omits the
// nonce source entirely — the inline script stops running, which is the safe
// direction to fail.
func newScriptNonce() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf[:])
}

// scriptNonce returns the nonce assigned to this request by
// SecurityHeadersMiddleware, or "" if there is none.
func scriptNonce(r *http.Request) string {
	if nonce, ok := r.Context().Value(scriptNonceContextKey).(string); ok {
		return nonce
	}
	return ""
}

// contentSecurityPolicy assembles the CSP for one response.
//
// Scripts are locked to same-origin plus a per-request nonce: the session token
// lives in localStorage, so an XSS is a full account takeover and this is the
// backstop that keeps that class of bug contained.
//
// Styles allow 'unsafe-inline' — the frontend sets 14 style attributes through
// el(), and browsers without style-src-attr support would break on a stricter
// policy. Inline CSS is a far smaller risk than inline script, and none of it
// can reach the token.
func contentSecurityPolicy(nonce string) string {
	script := "'self'"
	if nonce != "" {
		script += " 'nonce-" + nonce + "'"
	}

	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		// styles.css @imports the Google Fonts stylesheet, which in turn pulls
		// the font files from gstatic.
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		// Two SVG noise textures are inlined as data: URIs in styles.css.
		"img-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"object-src 'none'",
	}, "; ")
}

// SecurityHeadersMiddleware sets the response headers that constrain what a
// page served from this origin is allowed to do.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newScriptNonce()
		if nonce != "" {
			r = r.WithContext(context.WithValue(r.Context(), scriptNonceContextKey, nonce))
		}

		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy(nonce))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// Redundant with frame-ancestors for current browsers, kept for older
		// ones that never implemented it.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")

		next.ServeHTTP(w, r)
	})
}

// --- CORS ---------------------------------------------------------------

// allowedOrigins is the parsed ALLOWED_ORIGINS list, set at startup.
var allowedOrigins []string

// InitAllowedOrigins reads the cross-origin allowlist. It is empty by default:
// the backend serves the frontend on this same origin and app.js fetches
// relative paths, so nothing legitimate needs CORS. The previous
// `Access-Control-Allow-Origin: *` on every response — authenticated ones
// included — was pure attack surface.
func InitAllowedOrigins() {
	var origins []string
	for _, entry := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			origins = append(origins, entry)
		}
	}
	allowedOrigins = origins
}

// originAllowed reports whether origin is on the allowlist.
func originAllowed(origin string) bool {
	for _, allowed := range allowedOrigins {
		if allowed == origin {
			return true
		}
	}
	return false
}

// CORSMiddleware grants cross-origin access only to configured origins, and
// echoes the specific origin rather than a wildcard so credentials remain
// meaningful. With no allowlist configured it emits no CORS headers at all,
// which is what same-origin traffic needs.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" && originAllowed(origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			// The response varies by Origin, so caches must not reuse one
			// origin's response for another.
			h.Add("Vary", "Origin")
			// PUT is deliberately absent: no route implements it.
			h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			// So a cross-origin caller can still read the ID to quote in a
			// bug report; response headers are otherwise hidden from JS.
			h.Set("Access-Control-Expose-Headers", "X-Request-Id")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
