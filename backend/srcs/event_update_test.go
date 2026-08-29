package main

import (
	"errors"
	"testing"
)

// intPtr is the id-or-nil the edit payload uses to tell an update from an
// insert.
func intPtr(v int) *int { return &v }

// editableEvent seeds an event with one category of two options, and returns
// the host, the event, the category and its options.
func editableEvent(t *testing.T) (hostID, eventID, categoryID int, optionIDs []int) {
	t.Helper()

	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('host', 'host@example.com', 'x') RETURNING id`).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO events (host_id, name, description, visibility)
		VALUES ($1, 'Gala', 'first draft', 'invite-only') RETURNING id`, hostID).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO event_members (event_id, user_id) VALUES ($1, $2)`, eventID, hostID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, 'Best Film') RETURNING id`,
		eventID).Scan(&categoryID); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	for _, name := range []string{"A", "B"} {
		var optionID int
		if err := db.QueryRow(`INSERT INTO options (category_id, name) VALUES ($1, $2) RETURNING id`,
			categoryID, name).Scan(&optionID); err != nil {
			t.Fatalf("seed option %q: %v", name, err)
		}
		optionIDs = append(optionIDs, optionID)
	}
	return hostID, eventID, categoryID, optionIDs
}

// The whole point of the feature: a host can fix the details and the ballot of
// an event that already exists, renaming rows in place rather than replacing
// them — the ids have to survive, because votes point at them.
func TestUpdateEventRewritesDetailsAndBallot(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, eventID, categoryID, options := editableEvent(t)

	updated, err := UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:              "Gala 2026",
		Description:       "second draft",
		Visibility:        "public",
		ResultsVisibility: "live",
		RequireFullBallot: true,
		Categories: []UpdateCategoryRequest{
			{
				ID:          intPtr(categoryID),
				Name:        "Best Picture",
				Description: "renamed",
				Options: []UpdateOptionRequest{
					{ID: intPtr(options[0]), Name: "A renamed"},
					{ID: intPtr(options[1]), Name: "B"},
					{Name: "C"},
				},
			},
			{Name: "Best Song", Options: []UpdateOptionRequest{{Name: "S1"}, {Name: "S2"}}},
		},
	})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}

	if updated.Name != "Gala 2026" || updated.Description != "second draft" {
		t.Errorf("details = %q/%q; want the edited ones", updated.Name, updated.Description)
	}
	if updated.Visibility != "public" || updated.ResultsVisibility != "live" || !updated.RequireFullBallot {
		t.Errorf("settings = %s/%s/%v; want public/live/true",
			updated.Visibility, updated.ResultsVisibility, updated.RequireFullBallot)
	}
	if len(updated.Categories) != 2 {
		t.Fatalf("categories = %d; want 2", len(updated.Categories))
	}

	first := updated.Categories[0]
	if first.ID != categoryID {
		t.Errorf("category id = %d; want %d kept", first.ID, categoryID)
	}
	if first.Name != "Best Picture" || first.Description != "renamed" {
		t.Errorf("category = %q/%q; want the renamed one", first.Name, first.Description)
	}
	if len(first.Options) != 3 {
		t.Fatalf("options = %d; want 3", len(first.Options))
	}
	if first.Options[0].ID != options[0] || first.Options[0].Name != "A renamed" {
		t.Errorf("option = %d/%q; want %d renamed in place", first.Options[0].ID, first.Options[0].Name, options[0])
	}
	if updated.Categories[1].Name != "Best Song" || len(updated.Categories[1].Options) != 2 {
		t.Errorf("added category = %+v; want Best Song with 2 options", updated.Categories[1])
	}
}

// Anything the host leaves out of the payload is gone: the edit is the end
// state, not a diff.
func TestUpdateEventDropsOmittedRows(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, eventID, categoryID, options := editableEvent(t)

	updated, err := UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{{
			ID:      intPtr(categoryID),
			Name:    "Best Film",
			Options: []UpdateOptionRequest{{ID: intPtr(options[0]), Name: "A"}},
		}},
	})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}
	if len(updated.Categories) != 1 || len(updated.Categories[0].Options) != 1 {
		t.Fatalf("ballot = %+v; want one category with one option", updated.Categories)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM options WHERE id = $1`, options[1]).Scan(&remaining); err != nil {
		t.Fatalf("count option: %v", err)
	}
	if remaining != 0 {
		t.Errorf("omitted option still present")
	}
}

// Options and categories cascade to votes, so dropping one that has been voted
// on would rewrite a tally. The edit is refused instead, whole.
func TestUpdateEventRefusesToDropVotedRows(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, eventID, categoryID, options := editableEvent(t)

	if _, err := db.Exec(`INSERT INTO votes (category_id, option_id, user_id) VALUES ($1, $2, $3)`,
		categoryID, options[1], hostID); err != nil {
		t.Fatalf("seed vote: %v", err)
	}

	// Dropping just the voted option.
	_, err := UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{{
			ID:      intPtr(categoryID),
			Name:    "Best Film",
			Options: []UpdateOptionRequest{{ID: intPtr(options[0]), Name: "A"}},
		}},
	})
	if !errors.Is(err, ErrOptionHasVotes) {
		t.Fatalf("dropping a voted option: err = %v; want ErrOptionHasVotes", err)
	}

	// Dropping the whole category it lives in.
	_, err = UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{{Name: "Fresh start", Options: []UpdateOptionRequest{{Name: "X"}}}},
	})
	if !errors.Is(err, ErrCategoryHasVotes) {
		t.Fatalf("dropping a voted category: err = %v; want ErrCategoryHasVotes", err)
	}

	// Both were refused before anything was written: the vote, its option and
	// the event's name are all untouched.
	var votes, name = 0, ""
	if err := db.QueryRow(`SELECT COUNT(*) FROM votes WHERE category_id = $1`, categoryID).Scan(&votes); err != nil {
		t.Fatalf("count votes: %v", err)
	}
	if votes != 1 {
		t.Errorf("votes = %d; want the vote kept", votes)
	}
	if err := db.QueryRow(`SELECT name FROM events WHERE id = $1`, eventID).Scan(&name); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if name != "Gala" {
		t.Errorf("event name = %q; want the failed edit rolled back", name)
	}

	// Renaming the voted option is still fine — the row, and so the vote, stays.
	updated, err := UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{{
			ID:   intPtr(categoryID),
			Name: "Best Film",
			Options: []UpdateOptionRequest{
				{ID: intPtr(options[0]), Name: "A"},
				{ID: intPtr(options[1]), Name: "B, corrected"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("renaming a voted option: %v", err)
	}
	if got := updated.Categories[0].Options[1].Name; got != "B, corrected" {
		t.Errorf("option name = %q; want the rename applied", got)
	}
}

// Only the host edits, and only rows belonging to this event can be addressed
// by id — otherwise an edit would be a way to reach into someone else's ballot.
func TestUpdateEventRejectsForeignCallersAndRows(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, eventID, categoryID, options := editableEvent(t)

	var strangerID int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('stranger', 'stranger@example.com', 'x') RETURNING id`).Scan(&strangerID); err != nil {
		t.Fatalf("seed stranger: %v", err)
	}

	minimal := UpdateEventRequest{Name: "Hijacked", Visibility: "invite-only"}
	if _, err := UpdateEventInDB(eventID, strangerID, minimal); !errors.Is(err, ErrNotHost) {
		t.Errorf("stranger edit: err = %v; want ErrNotHost", err)
	}
	if _, err := UpdateEventInDB(eventID+9999, hostID, minimal); !errors.Is(err, ErrEventNotFound) {
		t.Errorf("missing event: err = %v; want ErrEventNotFound", err)
	}

	// A category id from another event.
	var otherEventID, otherCategoryID int
	if err := db.QueryRow(`INSERT INTO events (host_id, name) VALUES ($1, 'Other') RETURNING id`,
		hostID).Scan(&otherEventID); err != nil {
		t.Fatalf("seed other event: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, 'Elsewhere') RETURNING id`,
		otherEventID).Scan(&otherCategoryID); err != nil {
		t.Fatalf("seed other category: %v", err)
	}
	_, err := UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{{ID: intPtr(otherCategoryID), Name: "Stolen"}},
	})
	if !errors.Is(err, ErrCategoryNotFound) {
		t.Errorf("foreign category: err = %v; want ErrCategoryNotFound", err)
	}

	// An option id that belongs to a different category of this same event.
	var secondCategoryID int
	if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, 'Best Song') RETURNING id`,
		eventID).Scan(&secondCategoryID); err != nil {
		t.Fatalf("seed second category: %v", err)
	}
	_, err = UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{
			{ID: intPtr(categoryID), Name: "Best Film", Options: []UpdateOptionRequest{
				{ID: intPtr(options[0]), Name: "A"}, {ID: intPtr(options[1]), Name: "B"},
			}},
			{ID: intPtr(secondCategoryID), Name: "Best Song", Options: []UpdateOptionRequest{
				{ID: intPtr(options[0]), Name: "Moved"},
			}},
		},
	})
	if !errors.Is(err, ErrOptionNotFound) {
		t.Errorf("option moved between categories: err = %v; want ErrOptionNotFound", err)
	}
}

// The same existing row listed twice asks for two different end states at
// once; there is no right answer, so it is refused.
func TestUpdateEventRejectsDuplicateIDs(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, eventID, categoryID, options := editableEvent(t)

	_, err := UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{
			{ID: intPtr(categoryID), Name: "One"},
			{ID: intPtr(categoryID), Name: "Two"},
		},
	})
	if !errors.Is(err, ErrDuplicateEditRef) {
		t.Errorf("duplicate category: err = %v; want ErrDuplicateEditRef", err)
	}

	_, err = UpdateEventInDB(eventID, hostID, UpdateEventRequest{
		Name:       "Gala",
		Visibility: "invite-only",
		Categories: []UpdateCategoryRequest{{ID: intPtr(categoryID), Name: "Best Film", Options: []UpdateOptionRequest{
			{ID: intPtr(options[0]), Name: "One"},
			{ID: intPtr(options[0]), Name: "Two"},
		}}},
	})
	if !errors.Is(err, ErrDuplicateEditRef) {
		t.Errorf("duplicate option: err = %v; want ErrDuplicateEditRef", err)
	}
}
