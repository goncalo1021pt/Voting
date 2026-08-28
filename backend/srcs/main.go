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

// startSessionSweeper deletes expired sessions immediately and then every
// interval, until ctx is cancelled.
func startSessionSweeper(ctx context.Context, interval time.Duration) {
	sweep := func() {
		if n, err := DeleteExpiredSessionsFromDB(); err != nil {
			log.Printf("Session sweep failed: %v", err)
		} else if n > 0 {
			log.Printf("Session sweep: removed %d expired session(s)", n)
		}
		// One-time OAuth codes live for two minutes and are deleted on use, so
		// what remains here is only ever abandoned sign-ins. Swept alongside
		// sessions rather than in a timer of its own.
		if n, err := DeleteExpiredOAuthExchangesFromDB(); err != nil {
			log.Printf("OAuth exchange sweep failed: %v", err)
		} else if n > 0 {
			log.Printf("OAuth exchange sweep: removed %d expired code(s)", n)
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

	// Bring the schema up to date before serving. Failing here is deliberate:
	// a backend running against a stale schema fails in worse, later ways.
	if err := RunMigrations(context.Background()); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	// Fingerprint static assets so redeploys bust the browser cache.
	InitAssetVersion()

	// Decide whose CF-Connecting-IP header we believe before serving anything;
	// a bad CIDR list should fail the boot, not silently untrust every peer.
	if err := InitTrustedProxies(); err != nil {
		return fmt.Errorf("failed to configure trusted proxies: %w", err)
	}

	// Empty unless ALLOWED_ORIGINS is set; the frontend is same-origin.
	InitAllowedOrigins()

	// Google sign-in is optional. Absent credentials leave it switched off and
	// the button hidden; a half-set trio fails the boot rather than surfacing
	// as a broken redirect in the middle of someone's login.
	if err := InitGoogleOAuth(); err != nil {
		return fmt.Errorf("failed to configure google sign-in: %w", err)
	}
	if googleEnabled() {
		log.Printf("Google sign-in enabled (redirect: %s)", googleCfg.redirectURL)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", RouteHandler)

	// Middleware chain (outermost first): log, set security headers, then CORS,
	// then cap request bodies. Logging wraps everything so the line reports the
	// status actually sent, including CORS preflights and oversized-body
	// rejections. Security headers sit outside CORS so even a preflight
	// response carries them.
	handler := LogMiddleware(SecurityHeadersMiddleware(CORSMiddleware(MaxBytesMiddleware(mux))))

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

	// Expired sessions are unusable but were never deleted — sweep them at
	// startup and then hourly so the table stops growing forever.
	startSessionSweeper(ctx, time.Hour)

	// Rate-limiter keys were only ever pruned when the same key came back, so
	// every client that appeared once stayed resident forever.
	startLimiterSweeper(ctx, 10*time.Minute, allLimiters)

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
