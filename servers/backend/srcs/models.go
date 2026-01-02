package main

import "time"

// Category represents a voting category
type Category struct {
	ID        int       `json:"id"`
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
	VoterID    string    `json:"voter_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// VoteRequest represents the request body for recording a vote
type VoteRequest struct {
	CategoryID int    `json:"category_id"`
	OptionID   int    `json:"option_id"`
	VoterID    string `json:"voter_id"`
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
