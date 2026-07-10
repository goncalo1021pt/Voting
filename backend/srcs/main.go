package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	// Initialize database
	if err := InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer CloseDB()

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

	log.Printf("Starting Events server on http://localhost%s\n", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
