package main

import (
	"fmt"
	"time"
)

// GetEventsFromDB retrieves all public events and user's events
func GetEventsFromDB(userID int) ([]Event, error) {
	var events []Event

	query := "SELECT id, host_id, name, description, visibility, is_active, created_at FROM events WHERE visibility = 'public'"
	if userID > 0 {
		query += " OR host_id = $1 OR id IN (SELECT event_id FROM event_members WHERE user_id = $1)"
	}
	query += " ORDER BY created_at DESC"

	var rows interface{}
	var err error

	if userID > 0 {
		rows, err = db.Query(query, userID)
	} else {
		rows, err = db.Query(query)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch events: %w", err)
	}
	defer rows.(interface{ Close() error }).Close()

	return events, nil
}

// CreateEventInDB creates a new event with categories and options
func CreateEventInDB(hostID int, name, description, visibility string, categories []CreateCategoryRequest) (*Event, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	var eventID int
	err = tx.QueryRow(
		"INSERT INTO events (host_id, name, description, visibility) VALUES ($1, $2, $3, $4) RETURNING id",
		hostID, name, description, visibility,
	).Scan(&eventID)

	if err != nil {
		return nil, fmt.Errorf("failed to create event: %w", err)
	}

	// Add host as member
	_, err = tx.Exec(
		"INSERT INTO event_members (event_id, user_id) VALUES ($1, $2)",
		eventID, hostID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add host as member: %w", err)
	}

	// Create categories and options
	var eventCategories []Category
	for _, catReq := range categories {
		var categoryID int
		err = tx.QueryRow(
			"INSERT INTO categories (event_id, name) VALUES ($1, $2) RETURNING id",
			eventID, catReq.Name,
		).Scan(&categoryID)

		if err != nil {
			return nil, fmt.Errorf("failed to create category: %w", err)
		}

		var options []Option
		for _, optName := range catReq.Options {
			var optionID int
			err = tx.QueryRow(
				"INSERT INTO options (category_id, name) VALUES ($1, $2) RETURNING id",
				categoryID, optName,
			).Scan(&optionID)

			if err != nil {
				return nil, fmt.Errorf("failed to create option: %w", err)
			}

			options = append(options, Option{
				ID:         optionID,
				CategoryID: categoryID,
				Name:       optName,
			})
		}

		eventCategories = append(eventCategories, Category{
			ID:      categoryID,
			EventID: eventID,
			Name:    catReq.Name,
			Options: options,
		})
	}

	err = tx.Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &Event{
		ID:          eventID,
		HostID:      hostID,
		Name:        name,
		Description: description,
		Visibility:  visibility,
		IsActive:    true,
		Categories:  eventCategories,
	}, nil
}

// GetEventFromDB retrieves a specific event with its categories and options
func GetEventFromDB(eventID int) (*Event, error) {
	var event Event
	var createdAt time.Time

	err := db.QueryRow(
		"SELECT id, host_id, name, description, visibility, is_active, created_at FROM events WHERE id = $1",
		eventID,
	).Scan(&event.ID, &event.HostID, &event.Name, &event.Description, &event.Visibility, &event.IsActive, &createdAt)

	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	event.CreatedAt = createdAt

	// Get categories
	rows, err := db.Query(
		"SELECT id, event_id, name FROM categories WHERE event_id = $1",
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch categories: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.ID, &category.EventID, &category.Name); err != nil {
			continue
		}

		// Get options for this category
		optRows, err := db.Query(
			"SELECT id, category_id, name FROM options WHERE category_id = $1",
			category.ID,
		)
		if err != nil {
			continue
		}

		for optRows.Next() {
			var option Option
			if err := optRows.Scan(&option.ID, &option.CategoryID, &option.Name); err != nil {
				continue
			}
			category.Options = append(category.Options, option)
		}
		optRows.Close()

		event.Categories = append(event.Categories, category)
	}

	return &event, nil
}

// IsEventHostFromDB checks if a user is the host of an event
func IsEventHostFromDB(eventID, userID int) (bool, error) {
	var hostID int
	err := db.QueryRow(
		"SELECT host_id FROM events WHERE id = $1",
		eventID,
	).Scan(&hostID)

	if err != nil {
		return false, fmt.Errorf("event not found: %w", err)
	}

	return hostID == userID, nil
}

// CreateInvitationInDB creates a new invitation
func CreateInvitationInDB(eventID, invitedBy int, token string) (*Invitation, error) {
	var invitationID int
	var createdAt time.Time

	err := db.QueryRow(
		"INSERT INTO invitations (event_id, invited_by, token) VALUES ($1, $2, $3) RETURNING id, created_at",
		eventID, invitedBy, token,
	).Scan(&invitationID, &createdAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create invitation: %w", err)
	}

	return &Invitation{
		ID:        invitationID,
		EventID:   eventID,
		Token:     token,
		InvitedBy: invitedBy,
		CreatedAt: createdAt,
	}, nil
}

// RedeemInvitationInDB redeems an invitation and adds user to event
func RedeemInvitationInDB(token string, userID int) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	var eventID int
	var redeemedBy *int

	err = tx.QueryRow(
		"SELECT event_id, redeemed_by FROM invitations WHERE token = $1",
		token,
	).Scan(&eventID, &redeemedBy)

	if err != nil {
		return 0, fmt.Errorf("invitation not found: %w", err)
	}

	if redeemedBy != nil {
		return 0, fmt.Errorf("invitation already redeemed")
	}

	// Update invitation
	now := time.Now()
	_, err = tx.Exec(
		"UPDATE invitations SET redeemed_by = $1, redeemed_at = $2 WHERE token = $3",
		userID, now, token,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to update invitation: %w", err)
	}

	// Add user to event members
	_, err = tx.Exec(
		"INSERT INTO event_members (event_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		eventID, userID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to add event member: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return eventID, nil
}

// GetEventResultsFromDB gets voting results for a category in an event
func GetEventResultsFromDB(eventID, categoryID int) (*ResultsResponse, error) {
	var categoryName string
	var createdAt time.Time

	err := db.QueryRow(
		"SELECT name, created_at FROM categories WHERE id = $1 AND event_id = $2",
		categoryID, eventID,
	).Scan(&categoryName, &createdAt)

	if err != nil {
		return nil, fmt.Errorf("category not found: %w", err)
	}

	// Get vote counts per option
	rows, err := db.Query(`
		SELECT o.id, o.name, COUNT(v.id) as vote_count
		FROM options o
		LEFT JOIN votes v ON o.id = v.option_id
		WHERE o.category_id = $1
		GROUP BY o.id, o.name
		ORDER BY vote_count DESC
	`, categoryID)

	if err != nil {
		return nil, fmt.Errorf("failed to fetch results: %w", err)
	}
	defer rows.Close()

	var results []Result
	var totalVotes int

	for rows.Next() {
		var result Result
		var voteCount int
		if err := rows.Scan(&result.OptionID, &result.OptionName, &voteCount); err != nil {
			continue
		}
		result.Votes = voteCount
		totalVotes += voteCount
		results = append(results, result)
	}

	return &ResultsResponse{
		CategoryID:   categoryID,
		CategoryName: categoryName,
		Results:      results,
		TotalVotes:   totalVotes,
	}, nil
}

// RecordVoteInDB records a vote from a user
func RecordVoteInDB(userID, categoryID, optionID int) (*Vote, error) {
	// Check if option exists and get category
	var categoryIDCheck int
	err := db.QueryRow(
		"SELECT category_id FROM options WHERE id = $1",
		optionID,
	).Scan(&categoryIDCheck)

	if err != nil {
		return nil, fmt.Errorf("option not found: %w", err)
	}

	if categoryIDCheck != categoryID {
		return nil, fmt.Errorf("option does not belong to this category")
	}

	// Check if user already voted in this category
	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM votes WHERE category_id = $1 AND user_id = $2",
		categoryID, userID,
	).Scan(&count)

	if err != nil {
		return nil, fmt.Errorf("failed to check vote: %w", err)
	}

	if count > 0 {
		return nil, ErrAlreadyVoted
	}

	// Record vote
	var voteID int
	var createdAt time.Time

	err = db.QueryRow(
		"INSERT INTO votes (category_id, option_id, user_id) VALUES ($1, $2, $3) RETURNING id, created_at",
		categoryID, optionID, userID,
	).Scan(&voteID, &createdAt)

	if err != nil {
		return nil, fmt.Errorf("failed to record vote: %w", err)
	}

	return &Vote{
		ID:         voteID,
		CategoryID: categoryID,
		OptionID:   optionID,
		UserID:     userID,
		CreatedAt:  createdAt,
	}, nil
}
