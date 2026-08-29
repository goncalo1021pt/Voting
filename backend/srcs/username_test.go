package main

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeUsername(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means the name should be rejected
	}{
		{"plain", "goncalo", "goncalo"},
		{"spaced name", "Gonçalo Pereira", "Gonçalo Pereira"},
		{"surrounding whitespace trimmed", "  bob  ", "bob"},
		{"inner whitespace collapsed", "bob\t  smith", "bob smith"},
		{"punctuation inside", "bob.smith_the-2nd", "bob.smith_the-2nd"},
		{"digits", "voter2026", "voter2026"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"too short", "ab", ""},
		{"too long", strings.Repeat("a", maxUsernameLen+1), ""},
		{"leading punctuation", ".bob", ""},
		{"trailing punctuation", "bob-", ""},
		{"at sign", "bob@example.com", ""},
		{"angle brackets", "<script>x", ""},
		{"newline", "bob\nsmith", "bob smith"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason, ok := normalizeUsername(tc.in)
			if tc.want == "" {
				if ok {
					t.Errorf("normalizeUsername(%q) = %q, accepted; want rejected", tc.in, got)
				}
				if reason == "" {
					t.Errorf("normalizeUsername(%q) rejected without a reason", tc.in)
				}
				return
			}
			if !ok {
				t.Errorf("normalizeUsername(%q) rejected (%s); want %q", tc.in, reason, tc.want)
				return
			}
			if got != tc.want {
				t.Errorf("normalizeUsername(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A Google account gets a username derived from its email address on first
// sign-in. Renaming has to work there just as it does for a password account —
// that is the whole point — and it must not disturb the Google identity the
// account signs in with.
func TestUpdateUsernameWorksForGoogleAccounts(t *testing.T) {
	withTestDB(t)
	freshSchema(t)

	user, err := UpsertGoogleUserInDB("sub-123", "goncalo1021pt@example.com", "Gonçalo", true)
	if err != nil {
		t.Fatalf("create google user: %v", err)
	}
	if user.Username != "goncalo1021pt" {
		t.Fatalf("generated username = %q; want it derived from the address", user.Username)
	}

	renamed, err := UpdateUsernameInDB(user.ID, "Gonçalo P")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Username != "Gonçalo P" {
		t.Errorf("username = %q; want the chosen one", renamed.Username)
	}
	if renamed.Email != user.Email {
		t.Errorf("email = %q; want it unchanged", renamed.Email)
	}

	// Signing in with Google again resolves by subject, so it finds the same
	// account under its new name rather than making a second one.
	again, err := UpsertGoogleUserInDB("sub-123", "goncalo1021pt@example.com", "Gonçalo", true)
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if again.ID != user.ID {
		t.Errorf("user id = %d; want the same account %d", again.ID, user.ID)
	}
	if again.Username != "Gonçalo P" {
		t.Errorf("username after re-signin = %q; want the chosen one to stick", again.Username)
	}
}

func TestUpdateUsernameConflicts(t *testing.T) {
	withTestDB(t)
	freshSchema(t)

	var mine, theirs int
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('mine', 'mine@example.com', 'x') RETURNING id`).Scan(&mine); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO users (username, email, password_hash)
		VALUES ('taken', 'taken@example.com', 'x') RETURNING id`).Scan(&theirs); err != nil {
		t.Fatalf("seed other user: %v", err)
	}

	if _, err := UpdateUsernameInDB(mine, "taken"); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("rename onto a taken name: err = %v; want ErrUsernameTaken", err)
	}

	// Renaming to the name you already hold is a no-op, not a conflict.
	if _, err := UpdateUsernameInDB(mine, "mine"); err != nil {
		t.Errorf("rename to the current name: %v; want success", err)
	}

	if _, err := UpdateUsernameInDB(theirs+9999, "nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("rename of a missing user: err = %v; want ErrUserNotFound", err)
	}
}
