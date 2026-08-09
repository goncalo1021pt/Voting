package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// GetEventsFromDB retrieves all public events and events the user is part of.
// When userID > 0, each row includes an is_member flag for that user.
func GetEventsFromDB(userID int) ([]Event, error) {
	var rows *sql.Rows
	var err error
	if userID > 0 {
		rows, err = db.Query(`
			SELECT e.id, e.host_id, e.name, e.description, e.visibility, e.results_visibility, e.is_active, e.created_at,
			       EXISTS(SELECT 1 FROM event_members em WHERE em.event_id = e.id AND em.user_id = $1) AS is_member
			FROM events e
			WHERE e.visibility = 'public'
			   OR e.host_id = $1
			   OR EXISTS(SELECT 1 FROM event_members em2 WHERE em2.event_id = e.id AND em2.user_id = $1)
			ORDER BY e.created_at DESC
		`, userID)
	} else {
		rows, err = db.Query(`
			SELECT id, host_id, name, description, visibility, results_visibility, is_active, created_at, false AS is_member
			FROM events
			WHERE visibility = 'public'
			ORDER BY created_at DESC
		`)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch events: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.HostID, &e.Name, &e.Description, &e.Visibility, &e.ResultsVisibility, &e.IsActive, &e.CreatedAt, &e.IsMember); err != nil {
			return nil, fmt.Errorf("failed to scan event row: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event rows error: %w", err)
	}
	return events, nil
}

// CreateEventInDB creates a new event with categories and options
func CreateEventInDB(hostID int, name, description, visibility, resultsVisibility string, requireFullBallot bool, categories []CreateCategoryRequest) (*Event, error) {
	if resultsVisibility == "" {
		resultsVisibility = "after_conclusion"
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	var eventID int
	err = tx.QueryRow(
		"INSERT INTO events (host_id, name, description, visibility, results_visibility, require_full_ballot) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id",
		hostID, name, description, visibility, resultsVisibility, requireFullBallot,
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
			"INSERT INTO categories (event_id, name, description) VALUES ($1, $2, $3) RETURNING id",
			eventID, catReq.Name, catReq.Description,
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
			ID:          categoryID,
			EventID:     eventID,
			Name:        catReq.Name,
			Description: catReq.Description,
			Options:     options,
		})
	}

	err = tx.Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &Event{
		ID:                eventID,
		HostID:            hostID,
		Name:              name,
		Description:       description,
		Visibility:        visibility,
		ResultsVisibility: resultsVisibility,
		IsActive:          true,
		RequireFullBallot: requireFullBallot,
		Categories:        eventCategories,
	}, nil
}

// GetEventFromDB retrieves a specific event with its categories and options
func GetEventFromDB(eventID int) (*Event, error) {
	var event Event
	var createdAt time.Time

	err := db.QueryRow(
		"SELECT id, host_id, name, description, visibility, results_visibility, is_active, require_full_ballot, created_at FROM events WHERE id = $1",
		eventID,
	).Scan(&event.ID, &event.HostID, &event.Name, &event.Description, &event.Visibility, &event.ResultsVisibility, &event.IsActive, &event.RequireFullBallot, &createdAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("event not found: %w", err)
	}

	event.CreatedAt = createdAt

	db.QueryRow(
		"SELECT COUNT(*) FROM event_members WHERE event_id = $1", eventID,
	).Scan(&event.MemberCount)

	// Turnout: current members who have cast at least one vote anywhere in the
	// event. Distinct because a voter filling in five categories is still one
	// voter. Restricted to current members so this can't exceed member_count
	// after a removal — a removed member's votes stay in the tallies, but they
	// are no longer part of the "x of y voted" denominator.
	db.QueryRow(`
		SELECT COUNT(DISTINCT v.user_id)
		FROM votes v
		JOIN categories c ON c.id = v.category_id
		JOIN event_members m ON m.user_id = v.user_id AND m.event_id = c.event_id
		WHERE c.event_id = $1`, eventID,
	).Scan(&event.VoterCount)

	// Get categories
	rows, err := db.Query(
		"SELECT id, event_id, name, COALESCE(description, '') FROM categories WHERE event_id = $1",
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch categories: %w", err)
	}
	defer rows.Close()

	// Skipping a row silently would serve a partial event as if it were whole.
	// Dropping it is still wrong — issue #48 tracks propagating these — but at
	// least the operator can now see that it happened.
	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.ID, &category.EventID, &category.Name, &category.Description); err != nil {
			log.Printf("WARN event %d: skipping category row: %v", eventID, err)
			continue
		}

		// Get options for this category
		optRows, err := db.Query(
			"SELECT id, category_id, name FROM options WHERE category_id = $1",
			category.ID,
		)
		if err != nil {
			log.Printf("WARN event %d: skipping options for category %d: %v", eventID, category.ID, err)
			continue
		}

		for optRows.Next() {
			var option Option
			if err := optRows.Scan(&option.ID, &option.CategoryID, &option.Name); err != nil {
				log.Printf("WARN event %d: skipping option row in category %d: %v", eventID, category.ID, err)
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
// ListMembersForEventFromDB returns everyone who has joined an event, host
// first and then in join order.
func ListMembersForEventFromDB(eventID int) ([]EventMember, error) {
	rows, err := db.Query(`
		SELECT m.user_id, u.username, m.joined_at, (m.user_id = e.host_id) AS is_host
		FROM event_members m
		JOIN users u ON u.id = m.user_id
		JOIN events e ON e.id = m.event_id
		WHERE m.event_id = $1
		ORDER BY is_host DESC, m.joined_at ASC
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to list members: %w", err)
	}
	defer rows.Close()

	members := []EventMember{}
	for rows.Next() {
		var m EventMember
		if err := rows.Scan(&m.UserID, &m.Username, &m.JoinedAt, &m.IsHost); err != nil {
			return nil, fmt.Errorf("failed to scan member row: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("member rows error: %w", err)
	}
	return members, nil
}

// RemoveMemberFromDB drops a user's membership in an event. Votes they already
// cast are deliberately left in place — a tally shouldn't change retroactively
// because someone was removed. The host can't be removed from their own event.
func RemoveMemberFromDB(eventID, userID int) error {
	var hostID int
	if err := db.QueryRow("SELECT host_id FROM events WHERE id = $1", eventID).Scan(&hostID); err != nil {
		if err == sql.ErrNoRows {
			return ErrEventNotFound
		}
		return fmt.Errorf("failed to fetch event: %w", err)
	}
	if hostID == userID {
		return ErrCannotRemoveHost
	}

	res, err := db.Exec("DELETE FROM event_members WHERE event_id = $1 AND user_id = $2", eventID, userID)
	if err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to confirm removal: %w", err)
	}
	if affected == 0 {
		return ErrMemberNotFound
	}
	return nil
}

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

// CreateInvitationInDB creates a new invitation. A nil expiresAt stores an
// invitation that never expires.
func CreateInvitationInDB(eventID, invitedBy int, token string, expiresAt *time.Time) (*Invitation, error) {
	var invitationID int
	var createdAt time.Time

	err := db.QueryRow(
		"INSERT INTO invitations (event_id, invited_by, token, expires_at) VALUES ($1, $2, $3, $4) RETURNING id, created_at",
		eventID, invitedBy, token, expiresAt,
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
		ExpiresAt: expiresAt,
	}, nil
}

// ListInvitationsForEventFromDB returns every invitation for an event, with
// the redeemer's username joined in when redeemed.
func ListInvitationsForEventFromDB(eventID int) ([]Invitation, error) {
	rows, err := db.Query(`
		SELECT i.id, i.event_id, i.token, i.invited_by, i.redeemed_by, i.created_at, i.redeemed_at, i.expires_at, u.username
		FROM invitations i
		LEFT JOIN users u ON u.id = i.redeemed_by
		WHERE i.event_id = $1
		ORDER BY i.created_at DESC
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to list invitations: %w", err)
	}
	defer rows.Close()

	invitations := []Invitation{}
	for rows.Next() {
		var inv Invitation
		var redeemedBy sql.NullInt64
		var redeemedAt sql.NullTime
		var expiresAt sql.NullTime
		var username sql.NullString
		if err := rows.Scan(&inv.ID, &inv.EventID, &inv.Token, &inv.InvitedBy, &redeemedBy, &inv.CreatedAt, &redeemedAt, &expiresAt, &username); err != nil {
			return nil, fmt.Errorf("failed to scan invitation row: %w", err)
		}
		if redeemedBy.Valid {
			id := int(redeemedBy.Int64)
			inv.RedeemedBy = &id
		}
		if redeemedAt.Valid {
			t := redeemedAt.Time
			inv.RedeemedAt = &t
		}
		if expiresAt.Valid {
			t := expiresAt.Time
			inv.ExpiresAt = &t
		}
		if username.Valid {
			s := username.String
			inv.RedeemedByUsername = &s
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invitation rows error: %w", err)
	}
	return invitations, nil
}

// DeleteInvitationFromDB revokes an unredeemed invitation scoped to one event.
// Returns ErrInvitationNotFound if no matching token exists for the event, or
// ErrInvitationRedeemed if the invitation has already been used.
func DeleteInvitationFromDB(eventID int, token string) error {
	var redeemedBy sql.NullInt64
	err := db.QueryRow(
		"SELECT redeemed_by FROM invitations WHERE event_id = $1 AND token = $2",
		eventID, token,
	).Scan(&redeemedBy)
	if err == sql.ErrNoRows {
		return ErrInvitationNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to look up invitation: %w", err)
	}
	if redeemedBy.Valid {
		return ErrInvitationRedeemed
	}
	_, err = db.Exec("DELETE FROM invitations WHERE event_id = $1 AND token = $2", eventID, token)
	if err != nil {
		return fmt.Errorf("failed to delete invitation: %w", err)
	}
	return nil
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
	var expired bool
	var isActive bool

	// The expiry comparison runs in SQL so it uses the same clock that stamped
	// expires_at at creation time.
	//
	// The row locks make the checks below mean something at commit time rather
	// than merely at read time:
	//   FOR UPDATE OF i — two people redeeming the same token concurrently
	//     would otherwise both see redeemed_by IS NULL and both join.
	//   FOR SHARE OF e  — a host closing the event mid-redemption blocks until
	//     this transaction finishes, instead of racing the is_active check.
	err = tx.QueryRow(`
		SELECT i.event_id,
		       i.redeemed_by,
		       (i.expires_at IS NOT NULL AND i.expires_at < NOW()),
		       e.is_active
		FROM invitations i
		JOIN events e ON e.id = i.event_id
		WHERE i.token = $1
		FOR UPDATE OF i
		FOR SHARE OF e`,
		token,
	).Scan(&eventID, &redeemedBy, &expired, &isActive)

	if err == sql.ErrNoRows {
		return 0, ErrInvitationNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("failed to look up invitation: %w", err)
	}

	if redeemedBy != nil {
		return 0, ErrInvitationRedeemed
	}
	if expired {
		return 0, ErrInvitationExpired
	}
	// A link issued while the event was open must not still let people in
	// after the host closes it. Visibility deliberately isn't checked: an
	// invitation is a grant to join this event, and if it has since become
	// public then joining was allowed anyway.
	if !isActive {
		return 0, ErrEventClosed
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

	// A dropped row here understates a tally, which is worse than it sounds for
	// a voting app — log it loudly. Propagating the error is issue #48.
	for rows.Next() {
		var result Result
		var voteCount int
		if err := rows.Scan(&result.OptionID, &result.OptionName, &voteCount); err != nil {
			log.Printf("WARN category %d: skipping result row, tally may be understated: %v", categoryID, err)
			continue
		}
		result.Votes = voteCount
		totalVotes += voteCount
		results = append(results, result)
	}

	var memberCount int
	db.QueryRow(
		"SELECT COUNT(*) FROM event_members WHERE event_id = $1", eventID,
	).Scan(&memberCount)

	return &ResultsResponse{
		CategoryID:   categoryID,
		CategoryName: categoryName,
		Results:      results,
		TotalVotes:   totalVotes,
		MemberCount:  memberCount,
	}, nil
}

// GetAllEventResultsFromDB returns the full results payload for an event:
// every category and its tallies, plus event metadata.
func GetAllEventResultsFromDB(eventID int) (*EventResultsResponse, error) {
	var resp EventResultsResponse
	resp.EventID = eventID

	err := db.QueryRow(
		"SELECT name, is_active FROM events WHERE id = $1", eventID,
	).Scan(&resp.EventName, &resp.IsActive)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("failed to fetch event: %w", err)
	}

	db.QueryRow(
		"SELECT COUNT(*) FROM event_members WHERE event_id = $1", eventID,
	).Scan(&resp.MemberCount)

	rows, err := db.Query(`
		SELECT c.id, c.name, COALESCE(o.id, 0), COALESCE(o.name, ''), COUNT(v.id)
		FROM categories c
		LEFT JOIN options o ON o.category_id = c.id
		LEFT JOIN votes v ON v.option_id = o.id
		WHERE c.event_id = $1
		GROUP BY c.id, c.name, o.id, o.name
		ORDER BY c.id, COUNT(v.id) DESC, o.id
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch results: %w", err)
	}
	defer rows.Close()

	byCategory := map[int]*CategoryResults{}
	order := []int{}
	for rows.Next() {
		var catID, optID, votes int
		var catName, optName string
		if err := rows.Scan(&catID, &catName, &optID, &optName, &votes); err != nil {
			return nil, fmt.Errorf("failed to scan results row: %w", err)
		}
		cat, ok := byCategory[catID]
		if !ok {
			cat = &CategoryResults{CategoryID: catID, CategoryName: catName}
			byCategory[catID] = cat
			order = append(order, catID)
		}
		if optID != 0 {
			cat.Results = append(cat.Results, Result{OptionID: optID, OptionName: optName, Votes: votes})
			cat.TotalVotes += votes
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("results rows error: %w", err)
	}

	resp.Categories = make([]CategoryResults, 0, len(order))
	for _, id := range order {
		resp.Categories = append(resp.Categories, *byCategory[id])
	}
	return &resp, nil
}

// DeleteEventInDB removes an event and all its data. Only the host may delete.
func DeleteEventInDB(eventID, userID int) error {
	res, err := db.Exec(
		"DELETE FROM events WHERE id = $1 AND host_id = $2",
		eventID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete event: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if rows == 0 {
		var hostID int
		if qerr := db.QueryRow("SELECT host_id FROM events WHERE id = $1", eventID).Scan(&hostID); qerr == sql.ErrNoRows {
			return ErrEventNotFound
		}
		return ErrNotHost
	}
	return nil
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
		if err == sql.ErrNoRows {
			return nil, ErrOptionNotFound
		}
		return nil, fmt.Errorf("option not found: %w", err)
	}

	if categoryIDCheck != categoryID {
		return nil, fmt.Errorf("option does not belong to this category")
	}

	// Resolve event for this category and check that it is active and the user is a member
	var eventID int
	var isActive bool
	err = db.QueryRow(
		"SELECT e.id, e.is_active FROM categories c JOIN events e ON e.id = c.event_id WHERE c.id = $1",
		categoryID,
	).Scan(&eventID, &isActive)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCategoryNotFound
		}
		return nil, fmt.Errorf("failed to resolve event for category: %w", err)
	}
	if !isActive {
		return nil, ErrEventClosed
	}

	isMember, err := IsEventMemberFromDB(eventID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if !isMember {
		return nil, ErrNotMember
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

// GetUserVotesForEventFromDB returns a map of categoryID → optionID for all
// votes cast by the user in the given event.
func GetUserVotesForEventFromDB(eventID, userID int) (map[int]int, error) {
	rows, err := db.Query(`
		SELECT v.category_id, v.option_id
		FROM votes v
		JOIN categories c ON c.id = v.category_id
		WHERE c.event_id = $1 AND v.user_id = $2
	`, eventID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user votes: %w", err)
	}
	defer rows.Close()

	votes := map[int]int{}
	for rows.Next() {
		var catID, optID int
		if err := rows.Scan(&catID, &optID); err != nil {
			return nil, err
		}
		votes[catID] = optID
	}
	return votes, rows.Err()
}

// IsEventMemberFromDB reports whether the user has joined the event.
func IsEventMemberFromDB(eventID, userID int) (bool, error) {
	var one int
	err := db.QueryRow(
		"SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2",
		eventID, userID,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check membership: %w", err)
	}
	return true, nil
}

// GetEventVisibilityStateFromDB returns the host, active flag, visibility, and
// results_visibility for an event in a single round-trip.
func GetEventVisibilityStateFromDB(eventID int) (hostID int, isActive bool, visibility, resultsVisibility string, err error) {
	err = db.QueryRow(
		"SELECT host_id, is_active, visibility, results_visibility FROM events WHERE id = $1",
		eventID,
	).Scan(&hostID, &isActive, &visibility, &resultsVisibility)
	if err == sql.ErrNoRows {
		return 0, false, "", "", ErrEventNotFound
	}
	if err != nil {
		return 0, false, "", "", fmt.Errorf("failed to fetch event state: %w", err)
	}
	return hostID, isActive, visibility, resultsVisibility, nil
}

// CloseEventInDB marks an event as inactive. Only the host may close.
func CloseEventInDB(eventID, userID int) error {
	res, err := db.Exec(
		"UPDATE events SET is_active = FALSE WHERE id = $1 AND host_id = $2",
		eventID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to close event: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if rows == 0 {
		// Either the event doesn't exist or the user isn't the host. Distinguish.
		var hostID int
		qerr := db.QueryRow("SELECT host_id FROM events WHERE id = $1", eventID).Scan(&hostID)
		if qerr == sql.ErrNoRows {
			return ErrEventNotFound
		}
		if qerr != nil {
			return fmt.Errorf("failed to verify event: %w", qerr)
		}
		return ErrNotHost
	}
	return nil
}

// JoinPublicEventInDB joins a public, active event. Idempotent on re-join.
func JoinPublicEventInDB(eventID, userID int) error {
	_, isActive, visibility, _, err := GetEventVisibilityStateFromDB(eventID)
	if err != nil {
		return err
	}
	if visibility != "public" {
		return ErrEventNotPublic
	}
	if !isActive {
		return ErrEventClosed
	}

	_, err = db.Exec(
		"INSERT INTO event_members (event_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		eventID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to add event member: %w", err)
	}
	return nil
}
