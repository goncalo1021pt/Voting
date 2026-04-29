package main

import "time"

// User represents a registered user
type User struct {
	ID        int       `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// Event represents a voting event (created by a host user)
type Event struct {
	ID          int       `json:"id"`
	HostID      int       `json:"host_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"` // "public" or "invite-only"
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	Categories  []Category `json:"categories,omitempty"`
}

// EventMember represents a user's membership in an event
type EventMember struct {
	ID       int       `json:"id"`
	EventID  int       `json:"event_id"`
	UserID   int       `json:"user_id"`
	JoinedAt time.Time `json:"joined_at"`
}

// Invitation represents an invite to an event
type Invitation struct {
	ID        int       `json:"id"`
	EventID   int       `json:"event_id"`
	Token     string    `json:"token"`
	InvitedBy int       `json:"invited_by"`
	RedeemedBy *int     `json:"redeemed_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	RedeemedAt *time.Time `json:"redeemed_at,omitempty"`
}

// Category represents a voting category within an event
type Category struct {
	ID        int       `json:"id"`
	EventID   int       `json:"event_id"`
	Name      string    `json:"name"`
	Options   []Option  `json:"options"`
	CreatedAt time.Time `json:"created_at"`
}

// Option represents a voting option within a category
type Option struct {
	ID         int    `json:"id"`
	CategoryID int    `json:"category_id"`
	Name       string `json:"name"`
}

// Vote represents a single vote cast
type Vote struct {
	ID         int       `json:"id"`
	CategoryID int       `json:"category_id"`
	OptionID   int       `json:"option_id"`
	UserID     int       `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// Result represents voting results for a single option
type Result struct {
	OptionID   int    `json:"option_id"`
	OptionName string `json:"option_name"`
	Votes      int    `json:"votes"`
}

// ResultsResponse represents voting results for a category
type ResultsResponse struct {
	CategoryID   int        `json:"category_id"`
	CategoryName string     `json:"category_name"`
	Results      []Result   `json:"results"`
	TotalVotes   int        `json:"total_votes"`
}

// Auth request/response types
type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	User  User   `json:"user"`
	Token string `json:"token"`
}

// Event creation request
type CreateEventRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Visibility  string   `json:"visibility"`
	Categories  []CreateCategoryRequest `json:"categories"`
}

// Category creation request
type CreateCategoryRequest struct {
	Name    string   `json:"name"`
	Options []string `json:"options"`
}

// Vote request
type VoteRequest struct {
	CategoryID int `json:"category_id"`
	OptionID   int `json:"option_id"`
}
