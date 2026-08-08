package main

import (
	"context"
	"embed"
	"fmt"
	"log"

	"github.com/pressly/goose/v3"
)

// Migrations are compiled into the binary, so a running server can never
// disagree with the schema it was built against.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsDir is a path inside migrationsFS, not on disk.
const migrationsDir = "migrations"

// migrationLockID is an arbitrary but stable key for the Postgres advisory lock
// held while migrating. Two backends starting at once would otherwise both see
// an unapplied migration and race to run it.
const migrationLockID = 8675309

// RunMigrations brings the database up to the latest embedded migration. It runs
// at startup before the server accepts traffic, so deploying a schema change
// applies it automatically.
func RunMigrations(ctx context.Context) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(log.Default())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("unsupported goose dialect: %w", err)
	}

	// Serialise migration across instances. The lock is session-scoped, so a
	// backend that dies mid-migration releases it when its connection drops.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("failed to take migration lock: %w", err)
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockID); err != nil {
			log.Printf("Failed to release migration lock: %v", err)
		}
	}()

	before, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}

	if err := goose.UpContext(ctx, db, migrationsDir); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	after, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}

	if before == after {
		log.Printf("✓ Database schema up to date (version %d)", after)
	} else {
		log.Printf("✓ Database schema migrated: version %d → %d", before, after)
	}
	return nil
}
