package main

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

// withTestDB points the package-level db handle at TEST_DATABASE_URL, skipping
// the test when it isn't set so `go test ./srcs` still runs without Postgres.
// CI sets it; locally, export it against a throwaway database.
func withTestDB(t *testing.T) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping migration test")
	}

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	prev := db
	db = conn
	t.Cleanup(func() {
		db = prev
		conn.Close()
	})
}

// The migrations must build the schema from nothing, and re-running them must
// be a no-op — a redeploy without schema changes restarts the backend, which
// calls RunMigrations every time.
func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	withTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("first migration run: %v", err)
	}

	var version int64
	if err := db.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version < 2 {
		t.Fatalf("schema version = %d, want >= 2", version)
	}

	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("second migration run should be a no-op: %v", err)
	}
}

// A database created by the old /docker-entrypoint-initdb.d/ bootstrap already
// has the tables but is missing anything added since. The baseline migration is
// written with IF NOT EXISTS so it no-ops there instead of failing, and later
// migrations deliver the columns that database never received. This is the
// upgrade path for the deployed instance (issue #33).
func TestMigrationsAdoptPreExistingDatabase(t *testing.T) {
	withTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	// Stand in for the legacy database: the two tables the assertions touch,
	// created outside goose and without invitations.expires_at.
	legacy := `
		CREATE TABLE users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			email VARCHAR(255) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE events (
			id SERIAL PRIMARY KEY,
			host_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			visibility VARCHAR(50) DEFAULT 'invite-only' CHECK (visibility IN ('public', 'invite-only')),
			results_visibility VARCHAR(50) DEFAULT 'after_conclusion' CHECK (results_visibility IN ('after_conclusion', 'live')),
			is_active BOOLEAN DEFAULT TRUE,
			require_full_ballot BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO users (username, email, password_hash) VALUES ('ana', 'ana@example.com', 'x');
		INSERT INTO events (host_id, name) VALUES (1, 'Legacy Awards');`
	if _, err := db.ExecContext(ctx, legacy); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}

	if err := RunMigrations(ctx); err != nil {
		t.Fatalf("migrating a pre-existing database: %v", err)
	}

	var expiresAt int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'invitations' AND column_name = 'expires_at'`).Scan(&expiresAt); err != nil {
		t.Fatalf("check expires_at: %v", err)
	}
	if expiresAt != 1 {
		t.Fatal("invitations.expires_at missing after migration")
	}

	// The baseline must not have recreated the tables it found.
	var events int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("events row count = %d, want 1 — migration destroyed existing data", events)
	}
}
