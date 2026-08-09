package main

import (
	"context"
	"errors"
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

// seedInvitation issues a token for an event. expiresAt nil means no expiry.
func seedInvitation(t *testing.T, eventID, invitedBy int, token string, expiresAt *time.Time) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO invitations (event_id, token, invited_by, expires_at)
		VALUES ($1, $2, $3, $4)`, eventID, token, invitedBy, expiresAt); err != nil {
		t.Fatalf("seed invitation %q: %v", token, err)
	}
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

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"unknown token", "tok-nope", ErrInvitationNotFound},
		{"already redeemed", "tok-used", ErrInvitationRedeemed},
		{"expired", "tok-expired", ErrInvitationExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RedeemInvitationInDB(tt.token, inviteeID); !errors.Is(err, tt.want) {
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

// An invitation is single-use. Without FOR UPDATE on the invitation row, two
// concurrent redemptions both read redeemed_by IS NULL and both join.
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
