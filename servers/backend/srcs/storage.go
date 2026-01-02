package main

import (
	"database/sql"
	"time"
)

// GetCategoriesFromDB returns all votings (which are treated as categories in the API)
func GetCategoriesFromDB() ([]Category, error) {
	rows, err := db.Query(`
		SELECT id, name, created_at
		FROM votings
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	categories := make([]Category, 0)
	for rows.Next() {
		var cat Category
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.CreatedAt); err != nil {
			return nil, err
		}

		// Get options for this voting
		options, err := getOptionsByVotingID(cat.ID)
		if err != nil {
			return nil, err
		}
		cat.Options = options
		categories = append(categories, cat)
	}

	return categories, rows.Err()
}

// GetCategoryFromDB retrieves a single voting (category in API) by ID
func GetCategoryFromDB(id int) (*Category, error) {
	var cat Category
	err := db.QueryRow(`
		SELECT id, name, created_at
		FROM votings
		WHERE id = $1
	`, id).Scan(&cat.ID, &cat.Name, &cat.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCategoryNotFound
		}
		return nil, err
	}

	// Get all options from all categories under this voting
	options, err := getOptionsByVotingID(cat.ID)
	if err != nil {
		return nil, err
	}
	cat.Options = options

	return &cat, nil
}

// CreateCategoryInDB creates a new voting with categories and options
func CreateCategoryInDB(name string, optionNames []string) (*Category, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Create the voting
	var votingID int
	err = tx.QueryRow(`
		INSERT INTO votings (name, created_by, is_active)
		VALUES ($1, 1, true)
		RETURNING id
	`, name).Scan(&votingID)
	if err != nil {
		return nil, err
	}

	// Create a default category for the voting
	var categoryID int
	err = tx.QueryRow(`
		INSERT INTO categories (voting_id, name)
		VALUES ($1, $2)
		RETURNING id
	`, votingID, "Options").Scan(&categoryID)
	if err != nil {
		return nil, err
	}

	// Create the options
	options := make([]Option, 0)
	for i, optName := range optionNames {
		var optID int
		err := tx.QueryRow(`
			INSERT INTO options (category_id, name)
			VALUES ($1, $2)
			RETURNING id
		`, categoryID, optName).Scan(&optID)
		if err != nil {
			return nil, err
		}

		options = append(options, Option{
			ID:         optID,
			CategoryID: i + 1, // Simple mapping for API response
			Name:       optName,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Category{
		ID:        votingID,
		Name:      name,
		Options:   options,
		CreatedAt: time.Now(),
	}, nil
}

// AddVoteToDatabase records a new vote
func AddVoteToDatabase(votingID, categoryID, optionID int, voterID string) (*Vote, error) {
	// First, check if this voting + category + option combination exists
	var optExists int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM options WHERE id = $1
	`, optionID).Scan(&optExists)
	if err != nil {
		return nil, err
	}

	if optExists == 0 {
		return nil, ErrOptionNotFound
	}

	// Check if voter has already voted in this voting+category
	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM votes
		WHERE voting_id = $1 AND category_id = $2 AND user_id = 1
	`, votingID, categoryID).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	if count > 0 {
		return nil, ErrAlreadyVoted
	}

	// Insert the vote
	var voteID int
	err = db.QueryRow(`
		INSERT INTO votes (voting_id, category_id, option_id, user_id)
		VALUES ($1, $2, $3, 1)
		RETURNING id
	`, votingID, categoryID, optionID).Scan(&voteID)
	if err != nil {
		return nil, err
	}

	return &Vote{
		ID:         voteID,
		CategoryID: categoryID,
		OptionID:   optionID,
		VoterID:    voterID,
		CreatedAt:  time.Now(),
	}, nil
}

// GetResultsFromDB retrieves voting results
func GetResultsFromDB(votingID, categoryID int) (*ResultsResponse, error) {
	// Get voting name
	var votingName string
	err := db.QueryRow(`
		SELECT name FROM votings WHERE id = $1
	`, votingID).Scan(&votingName)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCategoryNotFound
		}
		return nil, err
	}

	// Get all options and their vote counts
	rows, err := db.Query(`
		SELECT o.id, o.name, COUNT(v.id) as vote_count
		FROM options o
		LEFT JOIN votes v ON o.id = v.option_id
		WHERE o.category_id IN (
			SELECT id FROM categories WHERE voting_id = $1
		)
		GROUP BY o.id, o.name
		ORDER BY o.id
	`, votingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]Result, 0)
	totalVotes := 0
	for rows.Next() {
		var optID int
		var optName string
		var voteCount int
		if err := rows.Scan(&optID, &optName, &voteCount); err != nil {
			return nil, err
		}
		results = append(results, Result{
			OptionID:   optID,
			OptionName: optName,
			Votes:      voteCount,
		})
		totalVotes += voteCount
	}

	return &ResultsResponse{
		CategoryID:   votingID,
		CategoryName: votingName,
		Results:      results,
		TotalVotes:   totalVotes,
	}, rows.Err()
}

// Helper function to get all options for a voting
func getOptionsByVotingID(votingID int) ([]Option, error) {
	rows, err := db.Query(`
		SELECT o.id, c.id, o.name
		FROM options o
		JOIN categories c ON o.category_id = c.id
		WHERE c.voting_id = $1
		ORDER BY o.id
	`, votingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	options := make([]Option, 0)
	for rows.Next() {
		var opt Option
		if err := rows.Scan(&opt.ID, &opt.CategoryID, &opt.Name); err != nil {
			return nil, err
		}
		options = append(options, opt)
	}

	return options, rows.Err()
}
