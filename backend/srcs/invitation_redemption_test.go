package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// freshSchema resets the test database to a migrated, empty state.
func freshSchema(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// seedEvent creates a host, an invitee and an event, returning their ids.
func seedEvent(t *testing.T) (hostID, inviteeID, eventID int) {
	t.Helper()

	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('host', 'host@example.com', 'x') RETURNING id`).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('invitee', 'invitee@example.com', 'x') RETURNING id`).Scan(&inviteeID); err != nil {
		t.Fatalf("seed invitee: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO events (host_id, name, visibility)
		VALUES ($1, 'Awards', 'invite-only') RETURNING id`, hostID).Scan(&eventID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return hostID, inviteeID, eventID
}

// seedInvitation issues a single-use token for an event. expiresAt nil means
// no expiry.
func seedInvitation(t *testing.T, eventID, invitedBy int, token string, expiresAt *time.Time) {
	t.Helper()
	seedInvitationWithUses(t, eventID, invitedBy, token, expiresAt, intPtr(1))
}

// seedInvitationWithUses issues a token with an explicit cap. A nil maxUses
// stores the unlimited link a host would post in a group chat.
func seedInvitationWithUses(t *testing.T, eventID, invitedBy int, token string, expiresAt *time.Time, maxUses *int) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO invitations (event_id, token, invited_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5)`, eventID, token, invitedBy, expiresAt, maxUses); err != nil {
		t.Fatalf("seed invitation %q: %v", token, err)
	}
}

// seedUser adds a user who is not yet in any event.
func seedUser(t *testing.T, name string) int {
	t.Helper()

	var id int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ($1, $2, 'x') RETURNING id`, name, name+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user %q: %v", name, err)
	}
	return id
}

func memberCount(t *testing.T, eventID int) int {
	t.Helper()

	var n int
	if err := db.QueryRow("SELECT count(*) FROM event_members WHERE event_id = $1", eventID).Scan(&n); err != nil {
		t.Fatalf("count members: %v", err)
	}
	return n
}

// The bug: a link issued while the event was open still joined people after
// the host closed it.
func TestRedeemRejectedAfterEventCloses(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	seedInvitation(t, eventID, hostID, "tok-closed", nil)

	if _, err := db.Exec("UPDATE events SET is_active = FALSE WHERE id = $1", eventID); err != nil {
		t.Fatalf("close event: %v", err)
	}

	_, err := RedeemInvitationInDB("tok-closed", inviteeID)
	if !errors.Is(err, ErrEventClosed) {
		t.Fatalf("err = %v, want ErrEventClosed", err)
	}
	if n := memberCount(t, eventID); n != 0 {
		t.Errorf("member count = %d, want 0 — redemption should not have joined anyone", n)
	}
}

func TestRedeemSucceedsWhileEventIsOpen(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	seedInvitation(t, eventID, hostID, "tok-open", nil)

	got, err := RedeemInvitationInDB("tok-open", inviteeID)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got != eventID {
		t.Errorf("event id = %d, want %d", got, eventID)
	}
	if n := memberCount(t, eventID); n != 1 {
		t.Errorf("member count = %d, want 1", n)
	}
}

// An invitation grants access to this event; if it has since become public,
// joining was permitted anyway, so redemption should still work.
func TestRedeemStillWorksIfEventTurnedPublic(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	seedInvitation(t, eventID, hostID, "tok-public", nil)

	if _, err := db.Exec("UPDATE events SET visibility = 'public' WHERE id = $1", eventID); err != nil {
		t.Fatalf("flip visibility: %v", err)
	}

	if _, err := RedeemInvitationInDB("tok-public", inviteeID); err != nil {
		t.Fatalf("redeem on a now-public event: %v", err)
	}
}

// The pre-existing rejections must still fire, and must still take precedence
// over the new closed-event check where they overlap.
func TestRedeemRejectsUnknownRedeemedAndExpired(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)

	past := time.Now().Add(-time.Hour)
	seedInvitation(t, eventID, hostID, "tok-expired", &past)
	seedInvitation(t, eventID, hostID, "tok-used", nil)

	if _, err := RedeemInvitationInDB("tok-used", inviteeID); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	// A second person, since the invitee is now a member and would be turned
	// away for that before the spent token is ever considered.
	latecomerID := seedUser(t, "latecomer")

	tests := []struct {
		name   string
		token  string
		userID int
		want   error
	}{
		{"unknown token", "tok-nope", latecomerID, ErrInvitationNotFound},
		{"already redeemed", "tok-used", latecomerID, ErrInvitationRedeemed},
		{"expired", "tok-expired", latecomerID, ErrInvitationExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RedeemInvitationInDB(tt.token, tt.userID); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// An expired invitation on a closed event should report the expiry, matching
// the order the checks are written in.
func TestExpiryReportedBeforeClosedEvent(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)

	past := time.Now().Add(-time.Hour)
	seedInvitation(t, eventID, hostID, "tok-both", &past)
	if _, err := db.Exec("UPDATE events SET is_active = FALSE WHERE id = $1", eventID); err != nil {
		t.Fatalf("close event: %v", err)
	}

	if _, err := RedeemInvitationInDB("tok-both", inviteeID); !errors.Is(err, ErrInvitationExpired) {
		t.Errorf("err = %v, want ErrInvitationExpired", err)
	}
}

// A single-use invitation admits one person. Without FOR UPDATE on the
// invitation row, two concurrent redemptions both read a use count of zero and
// both join.
func TestConcurrentRedemptionOnlySucceedsOnce(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, _, eventID := seedEvent(t)
	seedInvitation(t, eventID, hostID, "tok-race", nil)

	var second int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('other', 'other@example.com', 'x') RETURNING id`).Scan(&second); err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		okCount int
		errs    []error
	)
	start := make(chan struct{})

	for _, userID := range []int{second - 1, second} {
		wg.Add(1)
		go func(uid int) {
			defer wg.Done()
			<-start
			_, err := RedeemInvitationInDB("tok-race", uid)

			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				okCount++
			} else {
				errs = append(errs, err)
			}
		}(userID)
	}
	close(start)
	wg.Wait()

	if okCount != 1 {
		t.Errorf("%d redemptions succeeded, want exactly 1 (errors: %v)", okCount, errs)
	}
	if n := memberCount(t, eventID); n != 1 {
		t.Errorf("member count = %d, want 1 — a single-use invite let in %d people", n, n)
	}
}

// The feature: one link a host can post in a group chat, redeemed by everyone
// who reads it.
func TestUnlimitedInvitationAdmitsEveryone(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	seedInvitationWithUses(t, eventID, hostID, "tok-group", nil, nil)

	joiners := []int{inviteeID, seedUser(t, "second"), seedUser(t, "third")}
	for _, userID := range joiners {
		if _, err := RedeemInvitationInDB("tok-group", userID); err != nil {
			t.Fatalf("redeem for user %d: %v", userID, err)
		}
	}

	if n := memberCount(t, eventID); n != len(joiners) {
		t.Errorf("member count = %d, want %d", n, len(joiners))
	}

	invitations, err := ListInvitationsForEventFromDB(eventID)
	if err != nil {
		t.Fatalf("list invitations: %v", err)
	}
	if len(invitations) != 1 {
		t.Fatalf("invitations = %d, want 1", len(invitations))
	}
	inv := invitations[0]
	if inv.MaxUses != nil {
		t.Errorf("max_uses = %d, want unlimited", *inv.MaxUses)
	}
	if inv.Uses != len(joiners) || len(inv.Redemptions) != len(joiners) {
		t.Errorf("uses = %d with %d redemptions, want %d of each", inv.Uses, len(inv.Redemptions), len(joiners))
	}
	if inv.Redemptions[0].Username == "" {
		t.Errorf("redemption is missing the redeemer's name: %+v", inv.Redemptions[0])
	}
}

// A capped link admits exactly that many people and no more.
func TestCappedInvitationStopsAtItsLimit(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	seedInvitationWithUses(t, eventID, hostID, "tok-two", nil, intPtr(2))

	for _, userID := range []int{inviteeID, seedUser(t, "second")} {
		if _, err := RedeemInvitationInDB("tok-two", userID); err != nil {
			t.Fatalf("redeem for user %d: %v", userID, err)
		}
	}

	if _, err := RedeemInvitationInDB("tok-two", seedUser(t, "third")); !errors.Is(err, ErrInvitationRedeemed) {
		t.Errorf("third redemption: err = %v, want ErrInvitationRedeemed", err)
	}
	if n := memberCount(t, eventID); n != 2 {
		t.Errorf("member count = %d, want 2", n)
	}
}

// The bug: following an invitation to an event you are already in reported a
// successful join and spent a use of the link. The host, who is a member of
// their own event from the moment they create it, could burn their own
// single-use invitation by clicking it.
func TestRedeemByExistingMemberSpendsNothing(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	if _, err := db.Exec(`INSERT INTO event_members (event_id, user_id) VALUES ($1, $2)`, eventID, hostID); err != nil {
		t.Fatalf("seed host membership: %v", err)
	}
	seedInvitation(t, eventID, hostID, "tok-own", nil)

	gotEventID, err := RedeemInvitationInDB("tok-own", hostID)
	if !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("host redeeming their own link: err = %v, want ErrAlreadyMember", err)
	}
	if gotEventID != eventID {
		t.Errorf("event id = %d, want %d — the caller still needs somewhere to send them", gotEventID, eventID)
	}
	if n := memberCount(t, eventID); n != 1 {
		t.Errorf("member count = %d, want 1", n)
	}

	// The link is untouched, so the person it was meant for can still use it.
	if _, err := RedeemInvitationInDB("tok-own", inviteeID); err != nil {
		t.Fatalf("invitee redeeming the link afterwards: %v", err)
	}
	if n := memberCount(t, eventID); n != 2 {
		t.Errorf("member count = %d, want 2", n)
	}
}

// Clicking the same group-chat link twice is ordinary, and must not count
// twice against a capped link.
func TestSecondClickByTheSamePersonCostsNoUse(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	seedInvitationWithUses(t, eventID, hostID, "tok-twice", nil, intPtr(2))

	if _, err := RedeemInvitationInDB("tok-twice", inviteeID); err != nil {
		t.Fatalf("first click: %v", err)
	}
	if _, err := RedeemInvitationInDB("tok-twice", inviteeID); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("second click: err = %v, want ErrAlreadyMember", err)
	}

	// The use they didn't spend is still there for somebody else.
	if _, err := RedeemInvitationInDB("tok-twice", seedUser(t, "second")); err != nil {
		t.Errorf("someone else redeeming the remaining use: %v", err)
	}
}

// A link posted publicly is a door left open; the host has to be able to shut
// it after people have come through, and doing so must not evict them.
func TestRevokingAUsedInvitationKeepsItsMembers(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, inviteeID, eventID := seedEvent(t)
	seedInvitationWithUses(t, eventID, hostID, "tok-open-door", nil, nil)

	if _, err := RedeemInvitationInDB("tok-open-door", inviteeID); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if err := DeleteInvitationFromDB(eventID, "tok-open-door"); err != nil {
		t.Fatalf("revoke a used invitation: %v", err)
	}

	if n := memberCount(t, eventID); n != 1 {
		t.Errorf("member count = %d, want 1 — revoking must not evict anyone", n)
	}
	if _, err := RedeemInvitationInDB("tok-open-door", seedUser(t, "latecomer")); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("redeeming a revoked link: err = %v, want ErrInvitationNotFound", err)
	}
	if err := DeleteInvitationFromDB(eventID, "tok-open-door"); !errors.Is(err, ErrInvitationNotFound) {
		t.Errorf("revoking twice: err = %v, want ErrInvitationNotFound", err)
	}
}

// A capped link handed to a crowd must not admit more people than its cap,
// however many clicks land at once.
func TestConcurrentRedemptionRespectsTheCap(t *testing.T) {
	withTestDB(t)
	freshSchema(t)
	hostID, _, eventID := seedEvent(t)
	seedInvitationWithUses(t, eventID, hostID, "tok-crowd", nil, intPtr(3))

	userIDs := make([]int, 0, 6)
	for i := 0; i < 6; i++ {
		userIDs = append(userIDs, seedUser(t, fmt.Sprintf("guest%d", i)))
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		okCount int
	)
	start := make(chan struct{})
	for _, userID := range userIDs {
		wg.Add(1)
		go func(uid int) {
			defer wg.Done()
			<-start
			if _, err := RedeemInvitationInDB("tok-crowd", uid); err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}(userID)
	}
	close(start)
	wg.Wait()

	if okCount != 3 {
		t.Errorf("%d redemptions succeeded, want exactly 3", okCount)
	}
	if n := memberCount(t, eventID); n != 3 {
		t.Errorf("member count = %d, want 3 — a link capped at 3 let in %d", n, n)
	}
}
