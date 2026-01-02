package main

import (
	"net/http"
	"strings"
)

// RouteHandler handles all incoming requests
func RouteHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Static files (HTML frontend)
	if path == "/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, "frontend/index.html")
		return
	}

	// Auth routes (public)
	switch {
	case path == "/auth/register" && r.Method == "POST":
		RegisterHandler(w, r)
	case path == "/auth/login" && r.Method == "POST":
		LoginHandler(w, r)
	case path == "/auth/logout" && r.Method == "POST":
		LogoutHandler(w, r)
	
	// Event routes (check most specific first)
	case strings.HasPrefix(path, "/events/") && strings.Contains(path, "/results/") && r.Method == "GET":
		GetEventResultsHandler(w, r)
	case strings.HasPrefix(path, "/events/") && strings.HasSuffix(path, "/invitations") && r.Method == "POST":
		RequireAuth(CreateInvitationHandler)(w, r)
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
	case strings.HasPrefix(path, "/events/") && strings.Contains(path, "/results/") && r.Method == "GET":
		GetEventResultsHandler(w, r)
	

	default:
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
