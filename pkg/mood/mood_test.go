package mood

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeRepo is an in-memory mood Repository for handler tests (no database).
type fakeRepo struct {
	created []Checkin
}

func (f *fakeRepo) Create(c Checkin) (Checkin, error) {
	c.ID = int64(len(f.created) + 1)
	f.created = append(f.created, c)
	return c, nil
}

func (f *fakeRepo) ListByUser(userID string, limit int) ([]Checkin, error) {
	return f.created, nil
}

// setupRouter wires the handler; an empty userID omits the auth-context stub so
// the missing-user path can be exercised.
func setupRouter(repo Repository, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if userID != "" {
		r.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Next() })
	}
	h := NewHandler(repo)
	r.POST("/mood", h.Create)
	r.GET("/mood", h.List)
	return r
}

func postMood(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/mood", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestCreateStoresCheckinForUser(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	w := postMood(r, `{"energy":4,"stress":3,"burnout_risk":30,"reflection":"ok"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 stored check-in, got %d", len(repo.created))
	}
	got := repo.created[0]
	if got.UserID != "u1" {
		t.Errorf("expected check-in scoped to u1, got %q", got.UserID)
	}
	if got.Energy != 4 || got.Stress != 3 || got.BurnoutRisk != 30 {
		t.Errorf("stored fields mismatch: %+v", got)
	}
}

func TestCreateRejectsOutOfRangeEnergy(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	w := postMood(r, `{"energy":6,"stress":3}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for energy out of range, got %d", w.Code)
	}
	if len(repo.created) != 0 {
		t.Errorf("expected nothing stored on validation failure, got %d", len(repo.created))
	}
}

func TestCreateRejectsMissingStress(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "u1")
	w := postMood(r, `{"energy":3}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing stress, got %d", w.Code)
	}
}

func TestCreateRequiresUserContext(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "") // no auth-context stub
	w := postMood(r, `{"energy":3,"stress":3}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without user context, got %d", w.Code)
	}
}
