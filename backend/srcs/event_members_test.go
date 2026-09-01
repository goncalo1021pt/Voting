package main

import "testing"

// seedRosterEvent creates an event with two categories of one option each, a
// host and two other members, and returns everything a turnout assertion needs.
func seedRosterEvent(t *testing.T) (hostID int, memberIDs []int, eventID int, categories, options []int) {
	t.Helper()

	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('host', 'host@example.com', 'x') RETURNING id`).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO events (host_id, name) VALUES ($1, 'Gala') RETURNING id`,
		hostID).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO event_members (event_id, user_id) VALUES ($1, $2)`,
		eventID, hostID); err != nil {
		t.Fatalf("seed host membership: %v", err)
	}

	// Joined after the host, and in this order — the roster ordering is part of
	// what these tests cover, so the join times are set explicitly rather than
	// left to whatever resolution CURRENT_TIMESTAMP gives two inserts in a row.
	for _, name := range []string{"early", "late"} {
		var userID int
		if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
			VALUES ($1, $2, 'x') RETURNING id`, name, name+"@example.com").Scan(&userID); err != nil {
			t.Fatalf("seed member %q: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO event_members (event_id, user_id, joined_at)
			VALUES ($1, $2, now() + make_interval(secs => $3))`,
			eventID, userID, len(memberIDs)+1); err != nil {
			t.Fatalf("seed membership %q: %v", name, err)
		}
		memberIDs = append(memberIDs, userID)
	}

	for _, name := range []string{"Best Film", "Best Song"} {
		var categoryID int
		if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, $2) RETURNING id`,
			eventID, name).Scan(&categoryID); err != nil {
			t.Fatalf("seed category %q: %v", name, err)
		}
		categories = append(categories, categoryID)

		var optionID int
		if err := db.QueryRow(`INSERT INTO options (category_id, name) VALUES ($1, 'A') RETURNING id`,
			categoryID).Scan(&optionID); err != nil {
			t.Fatalf("seed option: %v", err)
		}
		options = append(options, optionID)
	}
	return hostID, memberIDs, eventID, categories, options
}

func castVote(t *testing.T, userID, categoryID, optionID int) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO votes (category_id, option_id, user_id) VALUES ($1, $2, $3)`,
		categoryID, optionID, userID); err != nil {
		t.Fatalf("cast vote: %v", err)
	}
}

func memberByName(t *testing.T, members []EventMember, username string) EventMember {
	t.Helper()

	for _, m := range members {
		if m.Username == username {
			return m
		}
	}
	t.Fatalf("member %q missing from roster of %d", username, len(members))
	return EventMember{}
}

// The whole point of votes_cast: a host looking at "2 of 3 voted" can see which
// name the missing vote belongs to. A full voter, a partial one and a silent
// one have to be told apart, and the silent one has to still get a row — a
// LEFT JOIN that dropped them would hide exactly the person being looked for.
func TestListMembersReportsVoteProgress(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	_, memberIDs, eventID, categories, options := seedRosterEvent(t)

	castVote(t, memberIDs[0], categories[0], options[0])
	castVote(t, memberIDs[0], categories[1], options[1])
	castVote(t, memberIDs[1], categories[0], options[0])

	members, err := ListMembersForEventFromDB(eventID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}

	if got := memberByName(t, members, "early").VotesCast; got != 2 {
		t.Errorf("full voter: expected votes_cast 2, got %d", got)
	}
	if got := memberByName(t, members, "late").VotesCast; got != 1 {
		t.Errorf("partial voter: expected votes_cast 1, got %d", got)
	}
	if got := memberByName(t, members, "host").VotesCast; got != 0 {
		t.Errorf("non-voter: expected votes_cast 0, got %d", got)
	}
}

// The vote count is scoped through this event's categories. A member who is
// busy voting in another event has not voted in this one, and reporting
// otherwise would tell the host to stop chasing someone they should chase.
func TestListMembersIgnoresVotesInOtherEvents(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, memberIDs, eventID, _, _ := seedRosterEvent(t)

	var otherEventID, otherCategoryID, otherOptionID int
	if err := db.QueryRow(`INSERT INTO events (host_id, name) VALUES ($1, 'Other') RETURNING id`,
		hostID).Scan(&otherEventID); err != nil {
		t.Fatalf("seed other event: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, 'Best Other') RETURNING id`,
		otherEventID).Scan(&otherCategoryID); err != nil {
		t.Fatalf("seed other category: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO options (category_id, name) VALUES ($1, 'A') RETURNING id`,
		otherCategoryID).Scan(&otherOptionID); err != nil {
		t.Fatalf("seed other option: %v", err)
	}
	castVote(t, memberIDs[0], otherCategoryID, otherOptionID)

	members, err := ListMembersForEventFromDB(eventID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if got := memberByName(t, members, "early").VotesCast; got != 0 {
		t.Errorf("expected votes_cast 0 for a vote cast elsewhere, got %d", got)
	}
}

// An event with no categories yet still has members, and every one of them has
// cast nothing. The LEFT JOIN has to survive having no categories to join to.
func TestListMembersWithoutCategories(t *testing.T) {
	withTestDB(t)
	freshSchema(t)

	var hostID, eventID int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('host', 'host@example.com', 'x') RETURNING id`).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO events (host_id, name) VALUES ($1, 'Empty') RETURNING id`,
		hostID).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO event_members (event_id, user_id) VALUES ($1, $2)`,
		eventID, hostID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	members, err := ListMembersForEventFromDB(eventID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if members[0].VotesCast != 0 {
		t.Errorf("expected votes_cast 0 with no categories, got %d", members[0].VotesCast)
	}
}

// Grouping the vote count per member must not disturb the order the roster has
// always had: the host first, then everyone else by when they joined.
func TestListMembersKeepsHostFirstThenJoinOrder(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	_, memberIDs, eventID, categories, options := seedRosterEvent(t)

	// Only the last member to join has voted, so an ordering that leaked the
	// GROUP BY's grouping order — or sorted on the count — would show up here.
	castVote(t, memberIDs[1], categories[0], options[0])

	members, err := ListMembersForEventFromDB(eventID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}

	want := []string{"host", "early", "late"}
	for i, username := range want {
		if members[i].Username != username {
			t.Fatalf("position %d: expected %q, got %q", i, username, members[i].Username)
		}
	}
	if !members[0].IsHost {
		t.Error("expected the first row to be flagged as the host")
	}
	if members[1].IsHost || members[2].IsHost {
		t.Error("expected only the host row to be flagged as host")
	}
}
