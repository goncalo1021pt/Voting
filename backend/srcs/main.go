package main

import (
	"log"
	"net/http"
)

func main() {
	// Initialize database
	if err := InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer CloseDB()

	http.HandleFunc("/", RouteHandler)

	port := ":8080"
	log.Printf("Starting voting server on http://localhost%s\n", port)

	if err := http.ListenAndServe(port, CORSMiddleware(http.DefaultServeMux)); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
