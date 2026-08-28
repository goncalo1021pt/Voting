package main

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrEmailBelongsToPasswordAccount is returned when a Google identity presents
// an address that already belongs to a password account we cannot safely claim.
var ErrEmailBelongsToPasswordAccount = errors.New("email already registered to a password account")

// usernameUnsafe matches everything not allowed in a generated username.
var usernameUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// UpsertGoogleUserInDB resolves a Google identity to a local user, creating one
// on first sign-in.
//
// Matching is by `sub` first, which is the only identifier Google promises is
// stable — a user can change the email on their Google account, and reusing
// email as the key would silently split or merge accounts when they do.
//
// Falling back to email is what lets someone who registered with a password
// later use the Google button and land on the same account. That link is only
// made when Google says the address is verified: without that check, anyone
// able to create a Google account bearing an existing user's address could
// take over that account.
func UpsertGoogleUserInDB(sub, email, name string, emailVerified bool) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	// 1. Known Google identity.
	var u User
	err := db.QueryRow(
		"SELECT id, username, email FROM users WHERE google_sub = $1",
		sub,
	).Scan(&u.ID, &u.Username, &u.Email)
	if err == nil {
		return &u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to look up google identity: %w", err)
	}

	// 2. An existing account with the same, Google-verified address.
	if email != "" && emailVerified {
		err := db.QueryRow(
			`UPDATE users SET google_sub = $1
			 WHERE email = $2 AND google_sub IS NULL
			 RETURNING id, username, email`,
			sub, email,
		).Scan(&u.ID, &u.Username, &u.Email)
		if err == nil {
			return &u, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to link google identity: %w", err)
		}
	}

	// 3. New user. A row may still exist on this address — an unverified
	// Google address, or one already linked to a different subject — and the
	// UNIQUE constraint would reject the insert with a message no user could
	// act on. Say something they can.
	if email != "" {
		var exists bool
		if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)", email).Scan(&exists); err != nil {
			return nil, fmt.Errorf("failed to check existing email: %w", err)
		}
		if exists {
			return nil, ErrEmailBelongsToPasswordAccount
		}
	}

	username, err := generateUsername(email, name)
	if err != nil {
		return nil, err
	}

	err = db.QueryRow(
		`INSERT INTO users (username, email, password_hash, google_sub)
		 VALUES ($1, $2, NULL, $3)
		 RETURNING id, username, email`,
		username, email, sub,
	).Scan(&u.ID, &u.Username, &u.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to create google user: %w", err)
	}
	return &u, nil
}

// generateUsername derives a free username from the Google profile. Usernames
// are unique and user-visible, so a collision has to be resolved rather than
// surfaced: two people called jane@ on different domains are both entitled to
// an account.
func generateUsername(email, name string) (string, error) {
	base := ""
	if i := strings.IndexByte(email, '@'); i > 0 {
		base = email[:i]
	}
	if base == "" {
		base = name
	}
	base = usernameUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(base)), "")
	base = strings.Trim(base, "._-")
	if len(base) > 24 {
		base = base[:24]
	}
	if base == "" {
		base = "voter"
	}

	for attempt := 0; attempt < 50; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s%d", base, attempt+1)
		}
		var taken bool
		if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM users WHERE username = $1)", candidate).Scan(&taken); err != nil {
			return "", fmt.Errorf("failed to check username: %w", err)
		}
		if !taken {
			return candidate, nil
		}
	}

	// Fifty collisions on one base is not a name clash any more; fall back to
	// something that cannot collide rather than looping forever.
	suffix, err := randomToken(4)
	if err != nil {
		return "", err
	}
	return base + "-" + suffix, nil
}

// CreateOAuthExchangeInDB stores a one-time code for the browser to redeem.
func CreateOAuthExchangeInDB(code string, userID int) error {
	if _, err := db.Exec(
		"INSERT INTO oauth_exchanges (code, user_id, expires_at) VALUES ($1, $2, $3)",
		code, userID, time.Now().Add(oauthExchangeTTL),
	); err != nil {
		return fmt.Errorf("failed to create oauth exchange: %w", err)
	}
	return nil
}

// ConsumeOAuthExchangeInDB redeems a one-time code and returns the user it was
// issued for.
//
// The DELETE ... RETURNING is what makes it single-use: redemption and removal
// are one statement, so two concurrent requests cannot both come away with a
// session. Expired rows simply do not match.
func ConsumeOAuthExchangeInDB(code string) (int, error) {
	var userID int
	err := db.QueryRow(
		"DELETE FROM oauth_exchanges WHERE code = $1 AND expires_at > now() RETURNING user_id",
		code,
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("unknown or expired exchange code")
	}
	if err != nil {
		return 0, fmt.Errorf("failed to consume oauth exchange: %w", err)
	}
	return userID, nil
}

// DeleteExpiredOAuthExchangesFromDB clears codes nobody redeemed.
func DeleteExpiredOAuthExchangesFromDB() (int64, error) {
	res, err := db.Exec("DELETE FROM oauth_exchanges WHERE expires_at < now()")
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired oauth exchanges: %w", err)
	}
	return res.RowsAffected()
}
