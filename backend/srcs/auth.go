package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// maxEmailLen matches the users.email column in the schema. Without this bound
// an over-long address reaches Postgres and comes back as a 500 instead of a
// straight answer about the input.
const maxEmailLen = 255

// normalizeEmail trims surrounding whitespace and reports whether what's left
// is a single, parseable address. Display-name forms ("Bob <bob@example.com>")
// parse fine but are rejected, so the address is the only thing ever stored.
func normalizeEmail(raw string) (string, bool) {
	email := strings.TrimSpace(raw)
	if email == "" || len(email) > maxEmailLen {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", false
	}
	return email, true
}

// Bounds on a username the user picks for themselves. The column holds 255
// characters; the tighter cap here is about the name staying readable in a
// member list and an avatar bubble.
const (
	minUsernameLen = 3
	maxUsernameLen = 32
)

// usernameShape is what a chosen name may look like: letters, digits, and
// interior spaces, dots, underscores or hyphens, starting and ending on a
// letter or digit.
//
// It is deliberately wider than what generateUsername derives from a Google
// profile — the point of letting people rename is that "goncalo1021pt" is a
// mailbox, not a name they'd introduce themselves by — while still narrow
// enough that a name cannot be padded out of shape or dressed up to read as
// something it is not.
var usernameShape = regexp.MustCompile(`^[\p{L}\p{N}](?:[\p{L}\p{N} ._-]*[\p{L}\p{N}])?$`)

// normalizeUsername trims the name and collapses runs of whitespace, then
// reports whether what is left is acceptable. The second return value is the
// reason to show the user when it is not.
func normalizeUsername(raw string) (string, string, bool) {
	username := strings.Join(strings.Fields(raw), " ")
	if n := utf8.RuneCountInString(username); n < minUsernameLen || n > maxUsernameLen {
		return "", fmt.Sprintf("Username must be between %d and %d characters", minUsernameLen, maxUsernameLen), false
	}
	if !usernameShape.MatchString(username) {
		return "", "Username may contain letters, digits, spaces, dots, underscores and hyphens, and must start and end with a letter or digit", false
	}
	return username, "", true
}

// RegisterHandler handles user registration
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Username == "" || req.Email == "" || req.Password == "" {
		http.Error(w, "Username, email, and password are required", http.StatusBadRequest)
		return
	}

	email, ok := normalizeEmail(req.Email)
	if !ok {
		http.Error(w, "Invalid email address", http.StatusBadRequest)
		return
	}

	if len(req.Password) < 6 {
		http.Error(w, "Password must be at least 6 characters", http.StatusBadRequest)
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		serverError(w, r, "Failed to hash password", err)
		return
	}

	// Create user in database
	user, err := CreateUserInDB(req.Username, email, string(hashedPassword))
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "Username or email already exists", http.StatusConflict)
			return
		}
		serverError(w, r, "Failed to create user", err)
		return
	}

	// Generate token and persist session
	token, err := issueSession(user.ID)
	if err != nil {
		serverError(w, r, "Failed to create session", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(AuthResponse{
		User:  *user,
		Token: token,
	})
}

// LoginHandler handles user login
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		http.Error(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	// Get user from database
	user, hashedPassword, err := GetUserByUsernameFromDB(req.Username)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(req.Password)); err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// Generate token and persist session
	token, err := issueSession(user.ID)
	if err != nil {
		serverError(w, r, "Failed to create session", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AuthResponse{
		User:  *user,
		Token: token,
	})
}

// extractBearerToken pulls the raw token out of the Authorization header.
func extractBearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("missing authorization header")
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return "", fmt.Errorf("invalid authorization header format")
	}
	return parts[1], nil
}

// GetUserFromToken validates the bearer token against the sessions table and
// slides its expiry. Returns the authenticated user ID.
func GetUserFromToken(r *http.Request) (int, error) {
	token, err := extractBearerToken(r)
	if err != nil {
		return 0, err
	}
	userID, err := VerifyAndSlideSessionInDB(token)
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// issueSession generates a 32-byte random token, persists a session row, and
// returns the token to hand back to the caller.
func issueSession(userID int) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	token := hex.EncodeToString(buf)
	if err := CreateSessionInDB(token, userID); err != nil {
		return "", err
	}
	return token, nil
}

// userIDContextKey carries the caller resolved by RequireAuth. Values 0 and 1
// are taken by requestIDContextKey (logging.go) and scriptNonceContextKey
// (security.go).
const userIDContextKey contextKey = 2

// authenticatedUserID returns the caller RequireAuth already resolved, and
// whether there was one. Reusing it matters: GetUserFromToken slides the
// session, so each extra call is another UPDATE for the same request.
func authenticatedUserID(r *http.Request) (int, bool) {
	userID, ok := r.Context().Value(userIDContextKey).(int)
	return userID, ok
}

// RequireAuth is middleware that checks for valid authentication.
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserFromToken(r)
		if err != nil {
			if errors.Is(err, ErrSessionInvalid) {
				http.Error(w, "Session expired", http.StatusUnauthorized)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userIDContextKey, userID)))
	}
}

// MeHandler returns the current user based on the bearer token.
func MeHandler(w http.ResponseWriter, r *http.Request) {
	userID, err := GetUserFromToken(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	user, err := GetUserByIDFromDB(userID)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// UpdateMeHandler changes the caller's own account details. The username is
// the only field it touches.
//
// It works the same for a Google account as for a password one: the name
// derived from a Google profile at first sign-in is a starting point, not
// something the person is stuck with. Nothing else about the identity moves —
// the Google subject and the email stay as the provider gave them.
func UpdateMeHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticatedUserID(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Username == nil {
		http.Error(w, "No changes requested", http.StatusBadRequest)
		return
	}

	username, reason, ok := normalizeUsername(*req.Username)
	if !ok {
		http.Error(w, reason, http.StatusBadRequest)
		return
	}

	user, err := UpdateUsernameInDB(userID, username)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameTaken):
			http.Error(w, "That username is already taken", http.StatusConflict)
		case errors.Is(err, ErrUserNotFound):
			http.Error(w, "User not found", http.StatusNotFound)
		default:
			serverError(w, r, "Failed to update username", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// LogoutHandler invalidates the caller's session.
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token, err := extractBearerToken(r)
	if err == nil && token != "" {
		_ = DeleteSessionInDB(token)
	}

	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"message":"Logged out successfully"}`)
}
