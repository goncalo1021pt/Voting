package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

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

	if len(req.Password) < 6 {
		http.Error(w, "Password must be at least 6 characters", http.StatusBadRequest)
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	// Create user in database
	user, err := CreateUserInDB(req.Username, req.Email, string(hashedPassword))
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			http.Error(w, "Username or email already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	// Generate token
	token := generateToken(user.ID)

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

	// Generate token
	token := generateToken(user.ID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(AuthResponse{
		User:  *user,
		Token: token,
	})
}

// GetUserFromToken extracts user ID from Authorization header
// Format: "Bearer <token>"
func GetUserFromToken(r *http.Request) (int, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return 0, fmt.Errorf("missing authorization header")
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return 0, fmt.Errorf("invalid authorization header format")
	}

	token := parts[1]
	userID, err := verifyToken(token)
	if err != nil {
		return 0, err
	}

	return userID, nil
}

// generateToken creates a simple token with user ID and expiration
// In production, use JWT or a proper session management library
func generateToken(userID int) string {
	// Create a simple token: userID:timestamp:random
	timestamp := time.Now().Unix()
	randomBytes := make([]byte, 16)
	rand.Read(randomBytes)
	randomStr := hex.EncodeToString(randomBytes)

	token := fmt.Sprintf("%d:%d:%s", userID, timestamp, randomStr)
	// In production, sign this token with a secret key

	return token
}

// verifyToken parses and validates a token
// In production, use JWT verification
func verifyToken(token string) (int, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid token format")
	}

	var userID int
	if _, err := fmt.Sscanf(parts[0], "%d", &userID); err != nil {
		return 0, fmt.Errorf("invalid token")
	}

	// In production, verify signature and check expiration
	// For now, just validate the format

	return userID, nil
}

// RequireAuth is middleware that checks for valid authentication
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := GetUserFromToken(r)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// LogoutHandler clears the token (client-side deletion)
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// In a real implementation with sessions, delete the session here
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"message":"Logged out successfully"}`)
}
