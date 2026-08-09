package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// A nonexistent event is 404 on host-only endpoints. It used to answer 403,
// which both misreports the problem and tells a stranger the id is real.
func TestHostOnlyEndpointsReport404ForMissingEvent(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	withTrustedProxies(t, "172.16.0.0/12")

	var userID int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('someone', 'someone@example.com', 'x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, err := issueSession(userID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	paths := []struct {
		method string
		path   string
	}{
		{"POST", "/events/9999/invitations"},
		{"GET", "/events/9999/invitations"},
		{"DELETE", "/events/9999/invitations/sometoken"},
		{"GET", "/events/9999/members"},
		{"DELETE", "/events/9999/members/1"},
	}

	for _, p := range paths {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			req := httptest.NewRequest(p.method, p.path, nil)
			req.RemoteAddr = "192.168.0.110:5000"
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			RouteHandler(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (body: %q)", rec.Code, rec.Body.String())
			}
		})
	}
}

// A real event the caller doesn't host must still be 403, not 404 — the fix
// must not have turned every authorisation failure into "not found".
func TestHostOnlyEndpointsStillReport403ForNonHost(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	withTrustedProxies(t, "172.16.0.0/12")

	_, strangerID, eventID := seedEvent(t)
	token, err := issueSession(strangerID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	req := httptest.NewRequest("GET", "/events/"+strconv.Itoa(eventID)+"/members", nil)
	req.RemoteAddr = "192.168.0.110:5000"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	RouteHandler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (body: %q)", rec.Code, rec.Body.String())
	}
}

func TestIsEventHostDistinguishesMissingFromNotHost(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, strangerID, eventID := seedEvent(t)

	if _, err := IsEventHostFromDB(9999, hostID); !errors.Is(err, ErrEventNotFound) {
		t.Errorf("missing event: err = %v, want ErrEventNotFound", err)
	}

	isHost, err := IsEventHostFromDB(eventID, strangerID)
	if err != nil {
		t.Fatalf("existing event: %v", err)
	}
	if isHost {
		t.Error("a stranger was reported as host")
	}

	isHost, err = IsEventHostFromDB(eventID, hostID)
	if err != nil || !isHost {
		t.Errorf("host check = (%v, %v), want (true, nil)", isHost, err)
	}
}

// The check-then-insert race: the loser used to surface as a 500. It is the
// same "already voted" outcome and should read as one.
func TestConcurrentDuplicateVoteReportsAlreadyVoted(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	voterID, _, categories, options := seedVotingEvent(t, false)

	// Enough attempts that at least one pair overlaps in the check/insert gap.
	const attempts = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ok   int
		dupe int
		odd  []error
	)
	start := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := RecordVoteInDB(voterID, categories[0], options[n%2])

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrAlreadyVoted):
				dupe++
			default:
				odd = append(odd, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if ok != 1 {
		t.Errorf("%d votes recorded, want exactly 1", ok)
	}
	if dupe != attempts-1 {
		t.Errorf("%d rejections reported ErrAlreadyVoted, want %d", dupe, attempts-1)
	}
	if len(odd) > 0 {
		t.Errorf("losers surfaced as generic errors instead of ErrAlreadyVoted: %v", odd)
	}
}

// The N+1 rewrite must return the same shape it always did.
func TestEventLoadsCategoriesAndOptionsIntact(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	_, eventID, _, _ := seedVotingEvent(t, false)

	event, err := GetEventFromDB(eventID)
	if err != nil {
		t.Fatalf("load event: %v", err)
	}
	if len(event.Categories) != 2 {
		t.Fatalf("got %d categories, want 2", len(event.Categories))
	}
	for _, c := range event.Categories {
		if len(c.Options) != 2 {
			t.Errorf("category %q has %d options, want 2", c.Name, len(c.Options))
		}
		for _, o := range c.Options {
			if o.CategoryID != c.ID {
				t.Errorf("option %d filed under category %d, belongs to %d", o.ID, c.ID, o.CategoryID)
			}
		}
	}
}

// An event with no categories must load cleanly rather than erroring on the
// early return that skips the options query.
func TestEventWithNoCategoriesLoads(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, _, eventID := seedEvent(t)
	_ = hostID

	event, err := GetEventFromDB(eventID)
	if err != nil {
		t.Fatalf("load event with no categories: %v", err)
	}
	if len(event.Categories) != 0 {
		t.Errorf("got %d categories, want 0", len(event.Categories))
	}
}

// Options must land on their own category even when ids interleave.
func TestOptionsGroupUnderTheRightCategory(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, _, eventID := seedEvent(t)

	var catA, catB int
	if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, 'A') RETURNING id`, eventID).Scan(&catA); err != nil {
		t.Fatalf("seed category A: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO categories (event_id, name) VALUES ($1, 'B') RETURNING id`, eventID).Scan(&catB); err != nil {
		t.Fatalf("seed category B: %v", err)
	}
	// Interleaved insertion order, so grouping can't rely on options arriving
	// category by category.
	for _, spec := range []struct {
		cat  int
		name string
	}{{catA, "a1"}, {catB, "b1"}, {catA, "a2"}, {catB, "b2"}} {
		if _, err := db.Exec(`INSERT INTO options (category_id, name) VALUES ($1, $2)`, spec.cat, spec.name); err != nil {
			t.Fatalf("seed option: %v", err)
		}
	}
	_ = hostID

	event, err := GetEventFromDB(eventID)
	if err != nil {
		t.Fatalf("load event: %v", err)
	}
	for _, c := range event.Categories {
		if len(c.Options) != 2 {
			t.Fatalf("category %q has %d options, want 2", c.Name, len(c.Options))
		}
		for _, o := range c.Options {
			if o.CategoryID != c.ID {
				t.Errorf("option %q (category %d) grouped under category %d", o.Name, o.CategoryID, c.ID)
			}
		}
	}
}
