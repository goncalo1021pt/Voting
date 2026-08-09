package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// uniqueViolation is Postgres' SQLSTATE for a unique constraint breach.
const uniqueViolation = "23505"

// isUniqueViolation reports whether err is a unique constraint breach, checked
// by SQLSTATE rather than by matching on message text.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == uniqueViolation
}

// RecordBallotInDB records a whole ballot in one transaction.
//
// This exists because require_full_ballot is not expressible one vote at a
// time: the browser's submit loop posted each category separately, so it could
// fail halfway and leave exactly the partial ballot the flag forbids — and a
// caller who simply didn't run that loop was never constrained at all.
//
// Votes already cast by this user count toward completeness but must not be
// re-submitted; the ballot carries the categories they have yet to vote in.
func RecordBallotInDB(userID, eventID int, votes []VoteRequest) ([]Vote, error) {
	if len(votes) == 0 {
		return nil, ErrBallotEmpty
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// FOR SHARE so a host closing the event mid-submission waits rather than
	// racing the is_active check.
	var isActive, requireFullBallot bool
	err = tx.QueryRow(
		"SELECT is_active, require_full_ballot FROM events WHERE id = $1 FOR SHARE",
		eventID,
	).Scan(&isActive, &requireFullBallot)
	if err == sql.ErrNoRows {
		return nil, ErrEventNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load event: %w", err)
	}
	if !isActive {
		return nil, ErrEventClosed
	}

	var isMember bool
	err = tx.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2)",
		eventID, userID,
	).Scan(&isMember)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if !isMember {
		return nil, ErrNotMember
	}

	// Every category in the event, so completeness can be judged and so a
	// category from someone else's event is rejected.
	eventCategories, err := categoryIDsForEvent(tx, eventID)
	if err != nil {
		return nil, err
	}

	alreadyVoted, err := votedCategoriesForUser(tx, eventID, userID)
	if err != nil {
		return nil, err
	}

	submitted := make(map[int]bool, len(votes))
	for _, v := range votes {
		if !eventCategories[v.CategoryID] {
			return nil, ErrCategoryNotFound
		}
		if submitted[v.CategoryID] {
			return nil, ErrDuplicateCategory
		}
		if alreadyVoted[v.CategoryID] {
			return nil, ErrAlreadyVoted
		}
		submitted[v.CategoryID] = true
	}

	if requireFullBallot {
		for categoryID := range eventCategories {
			if !submitted[categoryID] && !alreadyVoted[categoryID] {
				return nil, ErrBallotIncomplete
			}
		}
	}

	recorded := make([]Vote, 0, len(votes))
	for _, v := range votes {
		// Confirms the option exists *and* belongs to the category it was sent
		// under, so a valid option id can't be smuggled into another category.
		var belongs bool
		err = tx.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM options WHERE id = $1 AND category_id = $2)",
			v.OptionID, v.CategoryID,
		).Scan(&belongs)
		if err != nil {
			return nil, fmt.Errorf("failed to validate option: %w", err)
		}
		if !belongs {
			return nil, ErrOptionNotFound
		}

		var voteID int
		var createdAt time.Time
		err = tx.QueryRow(
			"INSERT INTO votes (category_id, option_id, user_id) VALUES ($1, $2, $3) RETURNING id, created_at",
			v.CategoryID, v.OptionID, userID,
		).Scan(&voteID, &createdAt)
		if err != nil {
			// UNIQUE(category_id, user_id) is the last line of defence if a
			// concurrent submission slipped in between the check and here.
			if isUniqueViolation(err) {
				return nil, ErrAlreadyVoted
			}
			return nil, fmt.Errorf("failed to record vote: %w", err)
		}

		recorded = append(recorded, Vote{
			ID:         voteID,
			CategoryID: v.CategoryID,
			OptionID:   v.OptionID,
			UserID:     userID,
			CreatedAt:  createdAt,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit ballot: %w", err)
	}
	return recorded, nil
}

// categoryIDsForEvent returns the event's category ids as a set.
func categoryIDsForEvent(tx *sql.Tx, eventID int) (map[int]bool, error) {
	rows, err := tx.Query("SELECT id FROM categories WHERE event_id = $1", eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch categories: %w", err)
	}
	defer rows.Close()

	ids := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan category id: %w", err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("category rows error: %w", err)
	}
	return ids, nil
}

// votedCategoriesForUser returns the categories in this event the user has
// already voted in.
func votedCategoriesForUser(tx *sql.Tx, eventID, userID int) (map[int]bool, error) {
	rows, err := tx.Query(`
		SELECT v.category_id
		FROM votes v
		JOIN categories c ON c.id = v.category_id
		WHERE c.event_id = $1 AND v.user_id = $2`,
		eventID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch existing votes: %w", err)
	}
	defer rows.Close()

	ids := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan vote row: %w", err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vote rows error: %w", err)
	}
	return ids, nil
}
