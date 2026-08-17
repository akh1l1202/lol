//go:build integration

// Package testdb provides shared setup for Postgres integration tests. These
// tests are gated behind the `integration` build tag and a TEST_DATABASE_URL
// env var, so the default `go test ./...` stays hermetic.
package testdb

import (
	"database/sql"
	"os"
	"testing"

	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/auth"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/mood"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/session"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/usage"
)

// Open connects to TEST_DATABASE_URL, applies every migration, and truncates
// all tables so each test starts from a clean slate. It skips the test when the
// env var is unset.
func Open(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run integration tests")
	}
	db, err := auth.OpenDB(dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	for _, migrate := range []func(*sql.DB) error{
		auth.Migrate, usage.Migrate, mood.Migrate, session.Migrate,
	} {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate test db: %v", err)
		}
	}
	Truncate(t, db)
	return db
}

// Truncate clears all tables and resets identity sequences.
func Truncate(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(
		`TRUNCATE usage_events, mood_checkins, focus_sessions, users RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate test db: %v", err)
	}
}
