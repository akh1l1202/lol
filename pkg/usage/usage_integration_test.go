//go:build integration

package usage_test

import (
	"testing"
	"time"

	"github.com/SwDC-kjsse/app-dev-1/backend/internal/testdb"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/auth"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/usage"
)

var baseTime = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func TestInsertAndListByUserWindow(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()

	if err := auth.NewPostgresUserRepository(db).Create(
		auth.User{ID: "u1", Username: "u1", PasswordHash: "x"},
	); err != nil {
		t.Fatalf("create user: %v", err)
	}
	repo := usage.NewPostgresRepository(db)

	if err := repo.Insert("u1", []usage.Event{
		{AppPackage: "a", StartedAt: baseTime.AddDate(0, 0, -1), DurationS: 60},
		{AppPackage: "b", StartedAt: baseTime.AddDate(0, 0, -10), DurationS: 120},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Window covering only the last 3 days -> only the -1d event.
	got, err := repo.ListByUser("u1", baseTime.AddDate(0, 0, -3), baseTime.AddDate(0, 0, 1), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].AppPackage != "a" {
		t.Fatalf("expected only the recent event, got %+v", got)
	}
	if got[0].UserID != "u1" {
		t.Errorf("expected event scoped to u1, got %q", got[0].UserID)
	}
}

func TestListByUserScopesToUser(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()

	ur := auth.NewPostgresUserRepository(db)
	if err := ur.Create(auth.User{ID: "u1", Username: "u1", PasswordHash: "x"}); err != nil {
		t.Fatalf("user u1: %v", err)
	}
	if err := ur.Create(auth.User{ID: "u2", Username: "u2", PasswordHash: "x"}); err != nil {
		t.Fatalf("user u2: %v", err)
	}
	repo := usage.NewPostgresRepository(db)

	if err := repo.Insert("u1", []usage.Event{{AppPackage: "a", StartedAt: baseTime, DurationS: 60}}); err != nil {
		t.Fatalf("insert u1: %v", err)
	}
	if err := repo.Insert("u2", []usage.Event{{AppPackage: "b", StartedAt: baseTime, DurationS: 60}}); err != nil {
		t.Fatalf("insert u2: %v", err)
	}

	got, err := repo.ListByUser("u1", baseTime.AddDate(0, 0, -1), baseTime.AddDate(0, 0, 1), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].AppPackage != "a" {
		t.Errorf("expected only u1's event, got %+v", got)
	}
}
