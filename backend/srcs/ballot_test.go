package main

import (
	"errors"
	"testing"
)

// seedVotingEvent creates an event with two categories of two options each,
// plus a member who can vote in it.
func seedVotingEvent(t *testing.T, requireFullBallot bool) (voterID, eventID int, categories, options []int) {
	t.Helper()

	var hostID int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('host', 'host@example.com', 'x') RETURNING id`).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('voter', 'voter@example.com', 'x') RETURNING id`).Scan(&voterID); err != nil {
		t.Fatalf("seed voter: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO events (host_id, name, require_full_ballot)
		VALUES ($1, 'Gala', $2) RETURNING id`, hostID, requireFullBallot).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO event_members (event_id, user_id) VALUES ($1, $2), ($1, $3)`,
		eventID, hostID, voterID); err != nil {
		t.Fatalf("seed members: %v", err)
	}

	for _, name := range []string{"Best Film", "Best Song"} {
		var categoryID int
		if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, $2) RETURNING id`,
			eventID, name).Scan(&categoryID); err != nil {
			t.Fatalf("seed category %q: %v", name, err)
		}
		categories = append(categories, categoryID)

		for _, opt := range []string{"A", "B"} {
			var optionID int
			if err := db.QueryRow(`INSERT INTO options (category_id, name) VALUES ($1, $2) RETURNING id`,
				categoryID, opt).Scan(&optionID); err != nil {
				t.Fatalf("seed option: %v", err)
			}
			options = append(options, optionID)
		}
	}
	return voterID, eventID, categories, options
}

func voteCount(t *testing.T, eventID, userID int) int {
	t.Helper()

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM votes v JOIN categories c ON c.id = v.category_id
		WHERE c.event_id = $1 AND v.user_id = $2`, eventID, userID).Scan(&n); err != nil {
		t.Fatalf("count votes: %v", err)
	}
	return n
}

// The bug: require_full_ballot lived only in the browser, so anything else
// could cast a partial ballot on a strict event.
func TestPartialBallotRejectedOnStrictEvent(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, true)

	// Only the first of two categories.
	_, err := RecordBallotInDB(voterID, eventID, []VoteRequest{
		{CategoryID: categories[0], OptionID: options[0]},
	})
	if !errors.Is(err, ErrBallotIncomplete) {
		t.Fatalf("err = %v, want ErrBallotIncomplete", err)
	}
	if n := voteCount(t, eventID, voterID); n != 0 {
		t.Errorf("recorded %d votes, want 0 — a rejected ballot must leave nothing behind", n)
	}
}

func TestCompleteBallotAcceptedOnStrictEvent(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, true)

	got, err := RecordBallotInDB(voterID, eventID, []VoteRequest{
		{CategoryID: categories[0], OptionID: options[0]},
		{CategoryID: categories[1], OptionID: options[2]},
	})
	if err != nil {
		t.Fatalf("submit full ballot: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("returned %d votes, want 2", len(got))
	}
	if n := voteCount(t, eventID, voterID); n != 2 {
		t.Errorf("recorded %d votes, want 2", n)
	}
}

// A non-strict event still accepts as many or as few as the voter likes.
func TestPartialBallotAllowedWhenNotStrict(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, false)

	if _, err := RecordBallotInDB(voterID, eventID, []VoteRequest{
		{CategoryID: categories[0], OptionID: options[0]},
	}); err != nil {
		t.Fatalf("submit partial ballot: %v", err)
	}
	if n := voteCount(t, eventID, voterID); n != 1 {
		t.Errorf("recorded %d votes, want 1", n)
	}
}

// The per-category endpoint cannot express "every category", so it is closed
// on strict events — this is what actually shuts the curl bypass.
func TestPerCategoryVoteRefusedOnStrictEvent(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, true)

	_, err := RecordVoteInDB(voterID, categories[0], options[0])
	if !errors.Is(err, ErrFullBallotRequired) {
		t.Fatalf("err = %v, want ErrFullBallotRequired", err)
	}
	if n := voteCount(t, eventID, voterID); n != 0 {
		t.Errorf("recorded %d votes, want 0", n)
	}
}

func TestPerCategoryVoteStillWorksWhenNotStrict(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, false)

	if _, err := RecordVoteInDB(voterID, categories[0], options[0]); err != nil {
		t.Fatalf("per-category vote: %v", err)
	}
	if n := voteCount(t, eventID, voterID); n != 1 {
		t.Errorf("recorded %d votes, want 1", n)
	}
}

// Votes cast earlier count toward completeness, so a voter part-way through a
// strict event can finish with a ballot covering only what is left.
func TestExistingVotesCountTowardCompleteness(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, false)

	// Vote in one category while the event is lenient, then tighten it.
	if _, err := RecordVoteInDB(voterID, categories[0], options[0]); err != nil {
		t.Fatalf("initial vote: %v", err)
	}
	if _, err := db.Exec("UPDATE events SET require_full_ballot = TRUE WHERE id = $1", eventID); err != nil {
		t.Fatalf("tighten event: %v", err)
	}

	if _, err := RecordBallotInDB(voterID, eventID, []VoteRequest{
		{CategoryID: categories[1], OptionID: options[2]},
	}); err != nil {
		t.Fatalf("ballot covering the remaining category: %v", err)
	}
	if n := voteCount(t, eventID, voterID); n != 2 {
		t.Errorf("recorded %d votes, want 2", n)
	}
}

func TestBallotRejectsMalformedInput(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, false)

	tests := []struct {
		name  string
		votes []VoteRequest
		want  error
	}{
		{"empty", nil, ErrBallotEmpty},
		{
			"same category twice",
			[]VoteRequest{
				{CategoryID: categories[0], OptionID: options[0]},
				{CategoryID: categories[0], OptionID: options[1]},
			},
			ErrDuplicateCategory,
		},
		{
			"category from another event",
			[]VoteRequest{{CategoryID: 9999, OptionID: options[0]}},
			ErrCategoryNotFound,
		},
		{
			// options[2] belongs to categories[1], not categories[0].
			"option smuggled into the wrong category",
			[]VoteRequest{{CategoryID: categories[0], OptionID: options[2]}},
			ErrOptionNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RecordBallotInDB(voterID, eventID, tt.votes); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
			if n := voteCount(t, eventID, voterID); n != 0 {
				t.Errorf("recorded %d votes, want 0", n)
			}
		})
	}
}

func TestBallotRejectsResubmittedCategory(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, false)

	if _, err := RecordBallotInDB(voterID, eventID, []VoteRequest{
		{CategoryID: categories[0], OptionID: options[0]},
	}); err != nil {
		t.Fatalf("first ballot: %v", err)
	}

	_, err := RecordBallotInDB(voterID, eventID, []VoteRequest{
		{CategoryID: categories[0], OptionID: options[1]},
		{CategoryID: categories[1], OptionID: options[2]},
	})
	if !errors.Is(err, ErrAlreadyVoted) {
		t.Fatalf("err = %v, want ErrAlreadyVoted", err)
	}
	// The second category must not have slipped through on a rejected ballot.
	if n := voteCount(t, eventID, voterID); n != 1 {
		t.Errorf("recorded %d votes, want 1", n)
	}
}

func TestBallotRejectedOnClosedEventAndForNonMembers(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, eventID, categories, options := seedVotingEvent(t, false)

	full := []VoteRequest{
		{CategoryID: categories[0], OptionID: options[0]},
		{CategoryID: categories[1], OptionID: options[2]},
	}

	var stranger int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('stranger', 'stranger@example.com', 'x') RETURNING id`).Scan(&stranger); err != nil {
		t.Fatalf("seed stranger: %v", err)
	}
	if _, err := RecordBallotInDB(stranger, eventID, full); !errors.Is(err, ErrNotMember) {
		t.Errorf("non-member: err = %v, want ErrNotMember", err)
	}

	if _, err := db.Exec("UPDATE events SET is_active = FALSE WHERE id = $1", eventID); err != nil {
		t.Fatalf("close event: %v", err)
	}
	if _, err := RecordBallotInDB(voterID, eventID, full); !errors.Is(err, ErrEventClosed) {
		t.Errorf("closed event: err = %v, want ErrEventClosed", err)
	}

	if _, err := RecordBallotInDB(voterID, 9999, full); !errors.Is(err, ErrEventNotFound) {
		t.Errorf("missing event: err = %v, want ErrEventNotFound", err)
	}
}
