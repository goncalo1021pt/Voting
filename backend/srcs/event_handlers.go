package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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
		serverError(w, r, "Failed to fetch events", err)
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
	if msg, ok := validateEventShape(req); !ok {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	if req.Visibility != "public" && req.Visibility != "invite-only" {
		req.Visibility = "invite-only"
	}

	if req.ResultsVisibility == "" {
		req.ResultsVisibility = "after_conclusion"
	} else if req.ResultsVisibility != "after_conclusion" && req.ResultsVisibility != "live" {
		http.Error(w, "results_visibility must be 'after_conclusion' or 'live'", http.StatusBadRequest)
		return
	}

	// Create event
	event, err := CreateEventInDB(userID, req.Name, req.Description, req.Visibility, req.ResultsVisibility, req.RequireFullBallot, req.Categories)
	if err != nil {
		serverError(w, r, "Failed to create event", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(event)
}

// redactEventForViewer strips members-only content from an invite-only event
// when the viewer is neither the host nor a member. The remaining shell is
// exactly what the frontend needs to render its access wall — name and state,
// but no description, ballot, or participation numbers.
func redactEventForViewer(event *Event, viewerID int) *Event {
	if event.Visibility != "invite-only" || viewerID == event.HostID || event.IsMember {
		return event
	}
	return &Event{
		ID:         event.ID,
		HostID:     event.HostID,
		Name:       event.Name,
		Visibility: event.Visibility,
		IsActive:   event.IsActive,
		CreatedAt:  event.CreatedAt,
	}
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

	// Enrich with caller-specific membership and vote data.
	viewerID := 0
	if userID, err := GetUserFromToken(r); err == nil && userID > 0 {
		viewerID = userID
		if isMember, err := IsEventMemberFromDB(eventID, userID); err == nil {
			event.IsMember = isMember
		}
		if votes, err := GetUserVotesForEventFromDB(eventID, userID); err == nil {
			event.MyVotes = votes
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(redactEventForViewer(event, viewerID))
}

// maxInvitationTTLHours caps invitation expiry at one year.
const maxInvitationTTLHours = 8760

// parseInvitationExpiry reads the optional CreateInvitation request body and
// returns the requested time-to-live in hours, or nil when the body is empty
// or omits expires_in_hours (meaning the invitation never expires).
func parseInvitationExpiry(body io.Reader) (*int, error) {
	var req struct {
		ExpiresInHours *int `json:"expires_in_hours"`
	}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil // no body: no expiry
		}
		return nil, fmt.Errorf("invalid request body")
	}
	if req.ExpiresInHours == nil {
		return nil, nil
	}
	if *req.ExpiresInHours < 1 || *req.ExpiresInHours > maxInvitationTTLHours {
		return nil, fmt.Errorf("expires_in_hours must be between 1 and %d", maxInvitationTTLHours)
	}
	return req.ExpiresInHours, nil
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
	if !requireHost(w, r, eventID, userID, "create invitations") {
		return
	}

	ttlHours, err := parseInvitationExpiry(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var expiresAt *time.Time
	if ttlHours != nil {
		t := time.Now().Add(time.Duration(*ttlHours) * time.Hour)
		expiresAt = &t
	}

	// Generate token
	token := generateInvitationToken()

	// Create invitation
	invitation, err := CreateInvitationInDB(eventID, userID, token, expiresAt)
	if err != nil {
		serverError(w, r, "Failed to create invitation", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(invitation)
}

// ListInvitationsHandler returns every invitation for an event. Host-only.
func ListInvitationsHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /events/{id}/invitations
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if !requireHost(w, r, eventID, userID, "view invitations") {
		return
	}

	invitations, err := ListInvitationsForEventFromDB(eventID)
	if err != nil {
		serverError(w, r, "Failed to fetch invitations", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(invitations)
}

// RevokeInvitationHandler deletes an unredeemed invitation. Host-only.
func RevokeInvitationHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /events/{id}/invitations/{token}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}
	token := parts[4]
	if token == "" {
		http.Error(w, "Invalid token", http.StatusBadRequest)
		return
	}

	if !requireHost(w, r, eventID, userID, "revoke invitations") {
		return
	}

	if err := DeleteInvitationFromDB(eventID, token); err != nil {
		switch {
		case errors.Is(err, ErrInvitationNotFound):
			http.Error(w, "Invitation not found", http.StatusNotFound)
		case errors.Is(err, ErrInvitationRedeemed):
			http.Error(w, "Invitation has already been redeemed", http.StatusConflict)
		default:
			serverError(w, r, "Failed to revoke invitation", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, fmt.Sprintf(`{"event_id":%d,"token":%q,"message":"Invitation revoked"}`, eventID, token))
}

// ListMembersHandler returns everyone who has joined an event. Host-only.
func ListMembersHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /events/{id}/members
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if !requireHost(w, r, eventID, userID, "view members") {
		return
	}

	members, err := ListMembersForEventFromDB(eventID)
	if err != nil {
		serverError(w, r, "Failed to fetch members", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members)
}

// RemoveMemberHandler removes a member from an event. Host-only. Votes the
// member already cast are left in place.
func RemoveMemberHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /events/{id}/members/{userId}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}
	memberID, err := strconv.Atoi(parts[4])
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	if !requireHost(w, r, eventID, userID, "remove members") {
		return
	}

	if err := RemoveMemberFromDB(eventID, memberID); err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			http.Error(w, "Event not found", http.StatusNotFound)
		case errors.Is(err, ErrMemberNotFound):
			http.Error(w, "Member not found", http.StatusNotFound)
		case errors.Is(err, ErrCannotRemoveHost):
			http.Error(w, "The host cannot be removed from their own event", http.StatusConflict)
		default:
			serverError(w, r, "Failed to remove member", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, fmt.Sprintf(`{"event_id":%d,"user_id":%d,"message":"Member removed"}`, eventID, memberID))
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
		switch {
		case errors.Is(err, ErrInvitationNotFound):
			http.Error(w, "Invalid invitation", http.StatusNotFound)
		case errors.Is(err, ErrInvitationRedeemed):
			http.Error(w, "Invitation has already been redeemed", http.StatusConflict)
		case errors.Is(err, ErrInvitationExpired):
			http.Error(w, "Invitation has expired", http.StatusGone)
		case errors.Is(err, ErrEventClosed):
			// Same 410 the public-join path returns for a closed event.
			http.Error(w, "Event is closed", http.StatusGone)
		default:
			serverError(w, r, "Failed to redeem invitation", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, fmt.Sprintf(`{"event_id":%d,"message":"Successfully joined event"}`, eventID))
}

// authorizeResultsView gates results viewing using the same rules for both
// the per-category and full-event endpoints. Writes the HTTP error and
// returns false when the caller may not see results.
func authorizeResultsView(w http.ResponseWriter, r *http.Request, eventID int) bool {
	hostID, isActive, visibility, resultsVisibility, err := GetEventVisibilityStateFromDB(eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			http.Error(w, "Event not found", http.StatusNotFound)
			return false
		}
		serverError(w, r, "Failed to fetch event", err)
		return false
	}

	viewerID, _ := GetUserFromToken(r)
	if viewerID != 0 && viewerID == hostID {
		return true
	}
	if resultsVisibility == "after_conclusion" && isActive {
		http.Error(w, "Results are hidden until the event is closed", http.StatusForbidden)
		return false
	}
	if visibility != "public" {
		if viewerID == 0 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return false
		}
		isMember, mErr := IsEventMemberFromDB(eventID, viewerID)
		if mErr != nil {
			serverError(w, r, "Failed to verify membership", mErr)
			return false
		}
		if !isMember {
			http.Error(w, "You are not a member of this event", http.StatusForbidden)
			return false
		}
	}
	return true
}

// GetEventResultsHandler gets voting results for one category in an event.
func GetEventResultsHandler(w http.ResponseWriter, r *http.Request) {
	// Path: /events/{id}/results/{catId}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
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

	if !authorizeResultsView(w, r, eventID) {
		return
	}

	results, err := GetEventResultsFromDB(eventID, categoryID)
	if err != nil {
		serverError(w, r, "Failed to fetch results", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// GetAllEventResultsHandler returns results for every category in the event.
func GetAllEventResultsHandler(w http.ResponseWriter, r *http.Request) {
	// Path: /events/{id}/results
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if !authorizeResultsView(w, r, eventID) {
		return
	}

	resp, err := GetAllEventResultsFromDB(eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			http.Error(w, "Event not found", http.StatusNotFound)
			return
		}
		serverError(w, r, "Failed to fetch results", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
		switch {
		case errors.Is(err, ErrAlreadyVoted):
			http.Error(w, "You have already voted in this category", http.StatusConflict)
		case errors.Is(err, ErrEventClosed):
			http.Error(w, "Event is closed", http.StatusGone)
		case errors.Is(err, ErrFullBallotRequired):
			http.Error(w, "This event requires a complete ballot; submit all categories to /events/{id}/ballot", http.StatusConflict)
		case errors.Is(err, ErrNotMember):
			http.Error(w, "You must join the event before voting", http.StatusForbidden)
		case errors.Is(err, ErrCategoryNotFound):
			http.Error(w, "Category not found", http.StatusNotFound)
		case errors.Is(err, ErrOptionNotFound):
			http.Error(w, "Option not found", http.StatusNotFound)
		default:
			serverError(w, r, "Failed to record vote", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(vote)
}

// requireHost writes the right error and returns false when the caller may not
// act as host of this event.
//
// The three outcomes were previously collapsed into one 403: a nonexistent
// event answered "only the host can do that" — which tells a stranger the id is
// real — and a database failure did too, silently, with nothing logged.
func requireHost(w http.ResponseWriter, r *http.Request, eventID, userID int, action string) bool {
	isHost, err := IsEventHostFromDB(eventID, userID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			http.Error(w, "Event not found", http.StatusNotFound)
			return false
		}
		serverError(w, r, "Failed to verify event host", err)
		return false
	}
	if !isHost {
		http.Error(w, "Only event host can "+action, http.StatusForbidden)
		return false
	}
	return true
}

// RecordBallotHandler records a whole ballot atomically: POST /events/{id}/ballot.
// It is the only way to vote on a require_full_ballot event, and the path the
// frontend uses for every event.
func RecordBallotHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /events/{id}/ballot
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	var req BallotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	for _, v := range req.Votes {
		if v.CategoryID == 0 || v.OptionID == 0 {
			http.Error(w, "Each vote needs a category_id and an option_id", http.StatusBadRequest)
			return
		}
	}

	votes, err := RecordBallotInDB(userID, eventID, req.Votes)
	if err != nil {
		switch {
		case errors.Is(err, ErrBallotEmpty):
			http.Error(w, "Ballot contains no votes", http.StatusBadRequest)
		case errors.Is(err, ErrDuplicateCategory):
			http.Error(w, "Ballot has more than one vote for the same category", http.StatusBadRequest)
		case errors.Is(err, ErrBallotIncomplete):
			http.Error(w, "This event requires a vote in every category", http.StatusUnprocessableEntity)
		case errors.Is(err, ErrAlreadyVoted):
			http.Error(w, "You have already voted in one of these categories", http.StatusConflict)
		case errors.Is(err, ErrEventNotFound):
			http.Error(w, "Event not found", http.StatusNotFound)
		case errors.Is(err, ErrEventClosed):
			http.Error(w, "Event is closed", http.StatusGone)
		case errors.Is(err, ErrNotMember):
			http.Error(w, "You must join the event before voting", http.StatusForbidden)
		case errors.Is(err, ErrCategoryNotFound):
			http.Error(w, "Category not found in this event", http.StatusNotFound)
		case errors.Is(err, ErrOptionNotFound):
			http.Error(w, "Option not found in this category", http.StatusNotFound)
		default:
			serverError(w, r, "Failed to record ballot", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(votes)
}

// Bounds on an event payload, enforced before any DB work. These cap resource
// use from a single create/import request; the per-field limits also match the
// VARCHAR(255) columns in the schema.
const (
	maxNameLen       = 255
	maxDescLen       = 5000
	maxCategories    = 100
	maxOptionsPerCat = 200
)

// validateEventShape checks the size/shape of a CreateEventRequest. It returns
// a human-readable reason and false when the payload is out of bounds.
func validateEventShape(req CreateEventRequest) (string, bool) {
	if len(req.Name) > maxNameLen {
		return "Event name is too long", false
	}
	if len(req.Description) > maxDescLen {
		return "Event description is too long", false
	}
	if len(req.Categories) > maxCategories {
		return fmt.Sprintf("Too many categories (max %d)", maxCategories), false
	}
	for _, cat := range req.Categories {
		if cat.Name == "" {
			return "Category name is required", false
		}
		if len(cat.Name) > maxNameLen {
			return "Category name is too long", false
		}
		if len(cat.Description) > maxDescLen {
			return "Category description is too long", false
		}
		if len(cat.Options) > maxOptionsPerCat {
			return fmt.Sprintf("Too many options in a category (max %d)", maxOptionsPerCat), false
		}
		for _, opt := range cat.Options {
			if len(opt) > maxNameLen {
				return "Option name is too long", false
			}
		}
	}
	return "", true
}

// generateInvitationToken creates a random invitation token
func generateInvitationToken() string {
	randomBytes := make([]byte, 32)
	rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}

// JoinEventHandler lets an authenticated user join a public event.
func JoinEventHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /events/{id}/join
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if err := JoinPublicEventInDB(eventID, userID); err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			http.Error(w, "Event not found", http.StatusNotFound)
		case errors.Is(err, ErrEventNotPublic):
			http.Error(w, "Event is invite-only", http.StatusForbidden)
		case errors.Is(err, ErrEventClosed):
			http.Error(w, "Event is closed", http.StatusGone)
		default:
			serverError(w, r, "Failed to join event", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, fmt.Sprintf(`{"event_id":%d,"message":"Successfully joined event"}`, eventID))
}

// DeleteEventHandler permanently removes an event. Only the host may delete.
func DeleteEventHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if err := DeleteEventInDB(eventID, userID); err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			http.Error(w, "Event not found", http.StatusNotFound)
		case errors.Is(err, ErrNotHost):
			http.Error(w, "Only the host can delete this event", http.StatusForbidden)
		default:
			serverError(w, r, "Failed to delete event", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, fmt.Sprintf(`{"event_id":%d,"message":"Event deleted"}`, eventID))
}

// CloseEventHandler lets the host of an event mark it inactive.
func CloseEventHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Path: /events/{id}/close
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	eventID, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if err := CloseEventInDB(eventID, userID); err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			http.Error(w, "Event not found", http.StatusNotFound)
		case errors.Is(err, ErrNotHost):
			http.Error(w, "Only the host can close this event", http.StatusForbidden)
		default:
			serverError(w, r, "Failed to close event", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, fmt.Sprintf(`{"event_id":%d,"message":"Event closed"}`, eventID))
}
