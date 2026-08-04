package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

// shutdownGrace is how long in-flight requests get to finish on SIGTERM.
// Docker force-kills after 10s (default stop_grace_period), so stay under it.
const shutdownGrace = 8 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

// run owns the server lifecycle. Errors return (rather than log.Fatalf inline)
// so deferred cleanup — closing the DB pool — actually runs.
func run() error {
	// Initialize database
	if err := InitDB(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer CloseDB()

	// Fingerprint static assets so redeploys bust the browser cache.
	InitAssetVersion()

	mux := http.NewServeMux()
	mux.HandleFunc("/", RouteHandler)

	// Middleware chain (outermost first): cap request bodies, then CORS.
	handler := CORSMiddleware(MaxBytesMiddleware(mux))

	port := ":8080"
	srv := &http.Server{
		Addr:    port,
		Handler: handler,
		// Timeouts guard against slow-client (Slowloris) resource exhaustion.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Serve until SIGINT/SIGTERM (docker stop, compose redeploy), then drain
	// in-flight requests instead of killing them mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("Starting Events server on http://localhost%s\n", port)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
	}

	log.Printf("Shutdown signal received, draining for up to %s…", shutdownGrace)
	drainCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("forced shutdown after grace period: %w", err)
	}
	log.Println("Server stopped cleanly")
	return nil
}
