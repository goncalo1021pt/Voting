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
	ID                int         `json:"id"`
	HostID            int         `json:"host_id"`
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	Visibility        string      `json:"visibility"`         // "public" or "invite-only"
	ResultsVisibility string      `json:"results_visibility"` // "after_conclusion" or "live"
	IsActive          bool        `json:"is_active"`
	CreatedAt         time.Time   `json:"created_at"`
	Categories        []Category  `json:"categories,omitempty"`
	IsMember          bool        `json:"is_member,omitempty"`
	MyVotes           map[int]int `json:"my_votes,omitempty"`
	RequireFullBallot bool        `json:"require_full_ballot"`
	MemberCount       int         `json:"member_count"`
	VoterCount        int         `json:"voter_count"`
}

// EventMember represents a user's membership in an event, as returned by the
// host-only member list. IsHost marks the one row that can't be removed.
type EventMember struct {
	UserID   int       `json:"user_id"`
	Username string    `json:"username"`
	JoinedAt time.Time `json:"joined_at"`
	IsHost   bool      `json:"is_host"`
}

// Invitation represents an invite to an event. A nil ExpiresAt means the
// invitation never expires.
type Invitation struct {
	ID                 int        `json:"id"`
	EventID            int        `json:"event_id"`
	Token              string     `json:"token"`
	InvitedBy          int        `json:"invited_by"`
	RedeemedBy         *int       `json:"redeemed_by,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	RedeemedAt         *time.Time `json:"redeemed_at,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	RedeemedByUsername *string    `json:"redeemed_by_username,omitempty"`
}

// Category represents a voting category within an event
type Category struct {
	ID          int       `json:"id"`
	EventID     int       `json:"event_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Options     []Option  `json:"options"`
	CreatedAt   time.Time `json:"created_at"`
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
	CategoryID   int      `json:"category_id"`
	CategoryName string   `json:"category_name"`
	Results      []Result `json:"results"`
	TotalVotes   int      `json:"total_votes"`
	MemberCount  int      `json:"member_count"`
}

// CategoryResults is the per-category slice of an EventResultsResponse.
type CategoryResults struct {
	CategoryID   int      `json:"category_id"`
	CategoryName string   `json:"category_name"`
	Results      []Result `json:"results"`
	TotalVotes   int      `json:"total_votes"`
}

// EventResultsResponse is the all-categories results payload.
type EventResultsResponse struct {
	EventID     int               `json:"event_id"`
	EventName   string            `json:"event_name"`
	IsActive    bool              `json:"is_active"`
	MemberCount int               `json:"member_count"`
	Categories  []CategoryResults `json:"categories"`
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
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	Visibility        string                  `json:"visibility"`
	ResultsVisibility string                  `json:"results_visibility"`
	RequireFullBallot bool                    `json:"require_full_ballot"`
	Categories        []CreateCategoryRequest `json:"categories"`
}

// Category creation request
type CreateCategoryRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Options     []string `json:"options"`
}

// UpdateEventRequest is the whole event as the host wants it to look after the
// edit — not a patch. Anything omitted from Categories (or from a category's
// Options) is removed, which is what lets the same form that created an event
// also edit it.
//
// A category or option carrying an ID is the existing row, renamed in place;
// one without is new. Keeping the ID matters: votes point at option rows, so a
// rename must not become a delete-and-recreate.
type UpdateEventRequest struct {
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	Visibility        string                  `json:"visibility"`
	ResultsVisibility string                  `json:"results_visibility"`
	RequireFullBallot bool                    `json:"require_full_ballot"`
	Categories        []UpdateCategoryRequest `json:"categories"`
}

// UpdateCategoryRequest is one category in an UpdateEventRequest. A nil ID
// means "create this one".
type UpdateCategoryRequest struct {
	ID          *int                  `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Options     []UpdateOptionRequest `json:"options"`
}

// UpdateOptionRequest is one option in an UpdateCategoryRequest. A nil ID
// means "create this one".
type UpdateOptionRequest struct {
	ID   *int   `json:"id"`
	Name string `json:"name"`
}

// UpdateProfileRequest changes the caller's own account details. Username is
// the only editable field, and it is a pointer so "not supplied" is
// distinguishable from "set to empty" — the latter is an error, not a no-op.
type UpdateProfileRequest struct {
	Username *string `json:"username"`
}

// Vote request
type VoteRequest struct {
	CategoryID int `json:"category_id"`
	OptionID   int `json:"option_id"`
}

// BallotRequest is a whole ballot submitted in one go. Recording every vote in
// a single transaction is what makes require_full_ballot enforceable: a
// per-category loop can fail halfway and leave exactly the partial ballot the
// flag exists to prevent.
type BallotRequest struct {
	Votes []VoteRequest `json:"votes"`
}
