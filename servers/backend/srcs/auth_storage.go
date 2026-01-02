package main

import (
	"fmt"
)

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

	err := db.QueryRow(
		"SELECT id, email, password_hash, created_at FROM users WHERE username = $1",
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
