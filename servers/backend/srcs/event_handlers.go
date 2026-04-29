package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// GetEventsHandler lists all public events and user's events
func GetEventsHandler(w http.ResponseWriter, r *http.Request) {
	// Get user from token if provided
	var userID int
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		var err error
		userID, err = GetUserFromToken(r)
		if err != nil {
			userID = 0 // Anonymous user
		}
	}

	events, err := GetEventsFromDB(userID)
	if err != nil {
		http.Error(w, "Failed to fetch events", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// CreateEventHandler creates a new event
func CreateEventHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req CreateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Name == "" {
		http.Error(w, "Event name is required", http.StatusBadRequest)
		return
	}

	if req.Visibility != "public" && req.Visibility != "invite-only" {
		req.Visibility = "invite-only"
	}

	// Create event
	event, err := CreateEventInDB(userID, req.Name, req.Description, req.Visibility, req.Categories)
	if err != nil {
		http.Error(w, "Failed to create event", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(event)
}

// GetEventHandler retrieves a specific event
func GetEventHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	eventIDStr := strings.TrimPrefix(path, "/events/")

	eventID, err := strconv.Atoi(eventIDStr)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	event, err := GetEventFromDB(eventID)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(event)
}

// CreateInvitationHandler creates an invitation to an event
func CreateInvitationHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract event ID from path like /events/1/invitations
	path := r.URL.Path
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	// Verify user is the event host
	isHost, err := IsEventHostFromDB(eventID, userID)
	if err != nil || !isHost {
		http.Error(w, "Only event host can create invitations", http.StatusForbidden)
		return
	}

	// Generate token
	token := generateInvitationToken()

	// Create invitation
	invitation, err := CreateInvitationInDB(eventID, userID, token)
	if err != nil {
		http.Error(w, "Failed to create invitation", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(invitation)
}

// RedeemInvitationHandler redeems an invitation and joins an event
func RedeemInvitationHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract token from path like /invitations/abc123
	path := r.URL.Path
	token := strings.TrimPrefix(path, "/invitations/")

	// Redeem invitation
	eventID, err := RedeemInvitationInDB(token, userID)
	if err != nil {
		http.Error(w, "Invalid or already redeemed invitation", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, fmt.Sprintf(`{"event_id":%d,"message":"Successfully joined event"}`, eventID))
}

// GetEventResultsHandler gets voting results for an event
func GetEventResultsHandler(w http.ResponseWriter, r *http.Request) {
	// Extract event ID from path like /events/1/results/2
	path := r.URL.Path
	parts := strings.Split(path, "/")

	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	categoryID, err := strconv.Atoi(parts[4])
	if err != nil {
		http.Error(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	results, err := GetEventResultsFromDB(eventID, categoryID)
	if err != nil {
		http.Error(w, "Failed to fetch results", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// RecordVoteHandler records a vote from an authenticated user
func RecordVoteHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req VoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.CategoryID == 0 || req.OptionID == 0 {
		http.Error(w, "Category ID and Option ID are required", http.StatusBadRequest)
		return
	}

	// Record vote
	vote, err := RecordVoteInDB(userID, req.CategoryID, req.OptionID)
	if err != nil {
		if err == ErrAlreadyVoted {
			http.Error(w, "You have already voted in this category", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to record vote", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(vote)
}

// generateInvitationToken creates a random invitation token
func generateInvitationToken() string {
	randomBytes := make([]byte, 32)
	rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}
