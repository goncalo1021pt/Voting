package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var frontendDir = "frontend"

// assetVersion fingerprints the static assets so their URLs change whenever
// their contents do. Set once at startup by InitAssetVersion.
var assetVersion = "dev"

// InitAssetVersion hashes the fingerprinted assets into a short token. A change
// to either file yields a new token, which busts the browser cache on the next
// deploy without needing a manual hard refresh.
func InitAssetVersion() {
	h := sha256.New()
	for _, name := range []string{"styles.css", "app.js"} {
		if b, err := os.ReadFile(filepath.Join(frontendDir, name)); err == nil {
			h.Write(b)
		}
	}
	assetVersion = hex.EncodeToString(h.Sum(nil))[:10]
}

// injectAssetVersion appends the ?v=<version> fingerprint to the stylesheet and
// script references in the HTML shell.
func injectAssetVersion(html, version string) string {
	html = strings.Replace(html, `href="/styles.css"`, `href="/styles.css?v=`+version+`"`, 1)
	html = strings.Replace(html, `src="/app.js"`, `src="/app.js?v=`+version+`"`, 1)
	return html
}

// serveIndex writes index.html with fingerprinted asset URLs and marks the HTML
// itself no-cache, so a redeployed build is picked up immediately (no hard
// refresh) while the fingerprinted assets stay cacheable long-term.
func serveIndex(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(filepath.Join(frontendDir, "index.html"))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, injectAssetVersion(string(b), assetVersion))
}

// serveFrontend serves a file from frontend/ if it exists, otherwise falls
// back to index.html (so client-side routes resolve on hard refresh).
func serveFrontend(w http.ResponseWriter, r *http.Request) {
	clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if clean == "" || clean == "." || strings.HasPrefix(clean, "..") {
		clean = "index.html"
	}
	full := filepath.Join(frontendDir, clean)
	info, err := os.Stat(full)

	// index.html (explicit or SPA fallback) is rendered dynamically so its
	// asset URLs carry the current fingerprint.
	if err != nil || info.IsDir() || clean == "index.html" {
		serveIndex(w, r)
		return
	}

	// A fingerprinted asset is safe to cache forever (its URL changes when the
	// content does); an un-fingerprinted request revalidates each load.
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, full)
}

// RouteHandler handles all incoming requests
func RouteHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Auth routes (public)
	switch {
	case path == "/auth/register" && r.Method == "POST":
		RateLimit(RegisterHandler)(w, r)
	case path == "/auth/login" && r.Method == "POST":
		RateLimit(LoginHandler)(w, r)
	case path == "/auth/logout" && r.Method == "POST":
		LogoutHandler(w, r)
	case path == "/auth/me" && r.Method == "GET":
		RequireAuth(MeHandler)(w, r)

	// Event routes (check most specific first)
	case strings.HasPrefix(path, "/events/") && strings.Contains(path, "/results/") && r.Method == "GET":
		GetEventResultsHandler(w, r)
	case strings.HasPrefix(path, "/events/") && strings.HasSuffix(path, "/results") && r.Method == "GET":
		GetAllEventResultsHandler(w, r)
	case strings.HasPrefix(path, "/events/") && strings.HasSuffix(path, "/invitations") && r.Method == "POST":
		RequireAuth(CreateInvitationHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && strings.HasSuffix(path, "/invitations") && r.Method == "GET":
		RequireAuth(ListInvitationsHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && strings.Contains(path, "/invitations/") && r.Method == "DELETE":
		RequireAuth(RevokeInvitationHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && strings.HasSuffix(path, "/members") && r.Method == "GET":
		RequireAuth(ListMembersHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && strings.Contains(path, "/members/") && r.Method == "DELETE":
		RequireAuth(RemoveMemberHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && strings.HasSuffix(path, "/join") && r.Method == "POST":
		RequireAuth(JoinEventHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && strings.HasSuffix(path, "/close") && r.Method == "POST":
		RequireAuth(CloseEventHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && r.Method == "DELETE":
		RequireAuth(DeleteEventHandler)(w, r)
	case strings.HasPrefix(path, "/events/") && r.Method == "GET":
		GetEventHandler(w, r)
	case path == "/events" && r.Method == "GET":
		GetEventsHandler(w, r)
	case path == "/events" && r.Method == "POST":
		RequireAuth(CreateEventHandler)(w, r)
	case strings.HasPrefix(path, "/invitations/") && r.Method == "POST":
		RedeemInvitationHandler(w, r)

	// Voting routes
	case path == "/votes" && r.Method == "POST":
		RequireAuth(RecordVoteHandler)(w, r)

	default:
		// Anything else: serve the frontend (file or SPA fallback) for GET,
		// otherwise 404.
		if r.Method == http.MethodGet {
			serveFrontend(w, r)
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

// CORSMiddleware adds CORS headers to responses
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
