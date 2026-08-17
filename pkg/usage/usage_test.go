package usage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeRepo is an in-memory Repository for handler tests (no database needed).
type fakeRepo struct {
	inserted []Event
}

func (f *fakeRepo) Insert(userID string, events []Event) error {
	for i := range events {
		events[i].UserID = userID
	}
	f.inserted = append(f.inserted, events...)
	return nil
}

func (f *fakeRepo) ListByUser(userID string, from, to time.Time, limit int) ([]Event, error) {
	return f.inserted, nil
}

func setupRouter(repo Repository, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Next() })
	h := NewHandler(repo)
	r.POST("/usage", h.Ingest)
	r.GET("/usage", h.List)
	return r
}

func TestIngestStoresEventsForUser(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	body, _ := json.Marshal(map[string]any{
		"events": []map[string]any{
			{"app_package": "com.example", "started_at": "2026-06-21T09:00:00Z", "duration_s": 600},
		},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/usage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	if len(repo.inserted) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(repo.inserted))
	}
	if repo.inserted[0].UserID != "u1" {
		t.Errorf("expected event scoped to user u1, got %q", repo.inserted[0].UserID)
	}
}

func TestIngestRejectsEmptyBatch(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "u1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/usage", bytes.NewReader([]byte(`{"events":[]}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an empty batch, got %d", w.Code)
	}
}
