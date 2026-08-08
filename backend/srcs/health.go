package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"
)

// healthPath is the health endpoint, kept in a constant because the access log
// special-cases it.
const healthPath = "/healthz"

// healthCheckTimeout bounds the database ping. A wedged database must make the
// check fail promptly rather than hang until Docker's own timeout fires.
const healthCheckTimeout = 2 * time.Second

// HealthHandler reports whether the backend can serve real traffic, which means
// reaching the database — a process that is listening but cannot query is not
// healthy in any useful sense.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Printf("req=%s health check failed: %v", requestID(r), err)
		http.Error(w, "database unreachable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, "ok\n")
}
