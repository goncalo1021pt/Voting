package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SessionTTL is the sliding lifetime of a session.
const SessionTTL = 30 * 24 * time.Hour

// CreateUserInDB creates a new user in the database
func CreateUserInDB(username, email, passwordHash string) (*User, error) {
	var userID int
	var createdAt string

	err := db.QueryRow(
		"INSERT INTO users (username, email, password_hash) VALUES ($1, $2, $3) RETURNING id, created_at",
		username, email, passwordHash,
	).Scan(&userID, &createdAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return &User{
		ID:       userID,
		Username: username,
		Email:    email,
	}, nil
}

// GetUserByUsernameFromDB retrieves user credentials by username
func GetUserByUsernameFromDB(username string) (*User, string, error) {
	var userID int
	var email string
	var passwordHash string
	var createdAt string

	// COALESCE because a Google-only user has no password_hash, and scanning a
	// NULL into a string fails outright. Empty is the right answer here: bcrypt
	// rejects it, so such a user simply cannot log in with a password — which
	// is exactly the intent.
	err := db.QueryRow(
		"SELECT id, email, COALESCE(password_hash, ''), created_at FROM users WHERE username = $1",
		username,
	).Scan(&userID, &email, &passwordHash, &createdAt)

	if err != nil {
		return nil, "", fmt.Errorf("user not found: %w", err)
	}

	return &User{
		ID:       userID,
		Username: username,
		Email:    email,
	}, passwordHash, nil
}

// CreateSessionInDB inserts a new session row valid for SessionTTL.
func CreateSessionInDB(token string, userID int) error {
	_, err := db.Exec(
		"INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)",
		token, userID, time.Now().Add(SessionTTL),
	)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// VerifyAndSlideSessionInDB validates a token and extends its expiry.
// Returns the user ID on success, or ErrSessionInvalid if the token is unknown
// or expired.
func VerifyAndSlideSessionInDB(token string) (int, error) {
	var userID int
	err := db.QueryRow(
		`UPDATE sessions
		 SET expires_at = $2
		 WHERE token = $1 AND expires_at > now()
		 RETURNING user_id`,
		token, time.Now().Add(SessionTTL),
	).Scan(&userID)
	if err == sql.ErrNoRows {
		return 0, ErrSessionInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("failed to verify session: %w", err)
	}
	return userID, nil
}

// DeleteSessionInDB removes a session row. No-op if it doesn't exist.
func DeleteSessionInDB(token string) error {
	_, err := db.Exec("DELETE FROM sessions WHERE token = $1", token)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessionsFromDB removes sessions past their expiry and returns
// how many were deleted. Expired rows are already unusable —
// VerifyAndSlideSessionInDB checks expires_at — this just stops the table
// growing forever.
func DeleteExpiredSessionsFromDB() (int64, error) {
	res, err := db.Exec("DELETE FROM sessions WHERE expires_at < now()")
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count deleted sessions: %w", err)
	}
	return n, nil
}

// GetUserByIDFromDB retrieves a user by ID
func GetUserByIDFromDB(userID int) (*User, error) {
	var username string
	var email string
	var createdAt string

	err := db.QueryRow(
		"SELECT username, email, created_at FROM users WHERE id = $1",
		userID,
	).Scan(&username, &email, &createdAt)

	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	return &User{
		ID:       userID,
		Username: username,
		Email:    email,
	}, nil
}

// UpdateUsernameInDB renames a user and returns the account as it now stands.
//
// Only the username moves: an account that signs in with Google keeps its
// subject and its provider email, so the rename cannot be used to point an
// identity somewhere else. Renaming to the name already held is a no-op that
// succeeds — the row is its own, so the UNIQUE constraint has nothing to say
// about it.
func UpdateUsernameInDB(userID int, username string) (*User, error) {
	var user User
	err := db.QueryRow(
		"UPDATE users SET username = $2 WHERE id = $1 RETURNING id, username, email",
		userID, username,
	).Scan(&user.ID, &user.Username, &user.Email)
	if isUniqueViolation(err) {
		return nil, ErrUsernameTaken
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update username: %w", err)
	}
	return &user, nil
}
