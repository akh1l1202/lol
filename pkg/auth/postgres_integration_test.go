//go:build integration

package auth_test

import (
	"testing"

	"github.com/SwDC-kjsse/app-dev-1/backend/internal/testdb"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/auth"
)

func TestPostgresUserRepositoryCreateAndGet(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()
	repo := auth.NewPostgresUserRepository(db)

	if err := repo.Create(auth.User{ID: "u1", Username: "alice", Name: "Alice", PasswordHash: "h"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, ok := repo.GetByUsername("alice")
	if !ok {
		t.Fatal("expected to find alice")
	}
	if got.ID != "u1" || got.PasswordHash != "h" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestPostgresUserRepositoryRejectsDuplicate(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()
	repo := auth.NewPostgresUserRepository(db)

	if err := repo.Create(auth.User{ID: "u1", Username: "bob", PasswordHash: "h"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := repo.Create(auth.User{ID: "u2", Username: "bob", PasswordHash: "h"})
	if err == nil || err.Error() != "user already exists" {
		t.Errorf("expected 'user already exists', got %v", err)
	}
}

func TestPostgresUserRepositoryMissingUser(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()
	if _, ok := auth.NewPostgresUserRepository(db).GetByUsername("nobody"); ok {
		t.Error("expected GetByUsername to report a missing user")
	}
}
