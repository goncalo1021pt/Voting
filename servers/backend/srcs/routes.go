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

	// API routes
	switch {
	case path == "/categories" && r.Method == "GET":
		GetCategories(w, r)
	case path == "/categories" && r.Method == "POST":
		CreateCategory(w, r)
	case strings.HasPrefix(path, "/categories/") && r.Method == "GET":
		GetCategory(w, r)
	case path == "/votes" && r.Method == "POST":
		RecordVote(w, r)
	case strings.HasPrefix(path, "/results/") && r.Method == "GET":
		GetResults(w, r)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

// CORSMiddleware adds CORS headers to responses
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
