package session

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeRepo is an in-memory session Repository for handler tests (no database).
type fakeRepo struct {
	created []Session
}

func (f *fakeRepo) Create(s Session) (Session, error) {
	s.ID = int64(len(f.created) + 1)
	f.created = append(f.created, s)
	return s, nil
}

func (f *fakeRepo) ListByUser(userID string, limit int) ([]Session, error) {
	return f.created, nil
}

func (f *fakeRepo) Update(userID string, id int64, u SessionUpdate) (Session, error) {
	for i := range f.created {
		if f.created[i].ID == id && f.created[i].UserID == userID {
			if u.Task != nil {
				f.created[i].Task = *u.Task
			}
			if u.EndedAt != nil {
				f.created[i].EndedAt = u.EndedAt
			}
			if u.Status != nil {
				f.created[i].Status = *u.Status
			}
			if u.PomodoroIndex != nil {
				f.created[i].PomodoroIndex = *u.PomodoroIndex
			}
			return f.created[i], nil
		}
	}
	return Session{}, sql.ErrNoRows
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
	r.POST("/sessions", h.Create)
	r.GET("/sessions", h.List)
	r.PATCH("/sessions/:id", h.Update)
	return r
}

func postSession(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/sessions", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func patchSession(r *gin.Engine, id, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPatch, "/sessions/"+id, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestCreateStoresSessionForUser(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	body := `{"task":"Write specs","started_at":"2026-06-21T10:00:00Z","ended_at":"2026-06-21T10:25:00Z","status":"completed","pomodoro_index":2}`
	w := postSession(r, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected 1 stored session, got %d", len(repo.created))
	}
	got := repo.created[0]
	if got.UserID != "u1" {
		t.Errorf("expected session scoped to u1, got %q", got.UserID)
	}
	if got.Task != "Write specs" || got.PomodoroIndex != 2 {
		t.Errorf("stored fields mismatch: %+v", got)
	}
	if got.EndedAt == nil {
		t.Errorf("expected ended_at to be parsed, got nil")
	}
}

func TestCreateAllowsOpenSession(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	// ended_at omitted -> still valid (a session in progress).
	w := postSession(r, `{"task":"Focusing","started_at":"2026-06-21T10:00:00Z"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for open session, got %d (%s)", w.Code, w.Body.String())
	}
	if repo.created[0].EndedAt != nil {
		t.Errorf("expected nil ended_at for an open session")
	}
}

func TestCreateRejectsMissingStartedAt(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	w := postSession(r, `{"task":"No start"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing started_at, got %d", w.Code)
	}
	if len(repo.created) != 0 {
		t.Errorf("expected nothing stored on validation failure")
	}
}

func TestCreateRequiresUserContext(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "") // no auth-context stub
	w := postSession(r, `{"started_at":"2026-06-21T10:00:00Z"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without user context, got %d", w.Code)
	}
}

func TestUpdateEndsRunningSession(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	// Start a running session (no ended_at).
	if w := postSession(r, `{"task":"Deep work","started_at":"2026-06-21T10:00:00Z","status":"running"}`); w.Code != http.StatusCreated {
		t.Fatalf("setup create failed: %d (%s)", w.Code, w.Body.String())
	}

	// End it via PATCH.
	w := patchSession(r, "1", `{"ended_at":"2026-06-21T10:25:00Z","status":"completed"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on update, got %d (%s)", w.Code, w.Body.String())
	}
	got := repo.created[0]
	if got.Status != "completed" {
		t.Errorf("expected status completed, got %q", got.Status)
	}
	if got.EndedAt == nil {
		t.Errorf("expected ended_at to be set after ending the session")
	}
	if got.Task != "Deep work" {
		t.Errorf("untouched field changed: task = %q", got.Task)
	}
}

func TestUpdateNotFound(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "u1")
	w := patchSession(r, "999", `{"status":"ended"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session, got %d", w.Code)
	}
}

func TestUpdateRejectsInvalidID(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "u1")
	w := patchSession(r, "abc", `{"status":"ended"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric id, got %d", w.Code)
	}
}

func TestUpdateRequiresUserContext(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "") // no auth-context stub
	w := patchSession(r, "1", `{"status":"ended"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without user context, got %d", w.Code)
	}
}

func TestUpdateScopedToOwner(t *testing.T) {
	repo := &fakeRepo{}
	// Seed a session owned by u2.
	repo.created = append(repo.created, Session{ID: 1, UserID: "u2", Task: "theirs"})

	r := setupRouter(repo, "u1") // requests run as u1
	w := patchSession(r, "1", `{"status":"ended"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 updating another user's session, got %d", w.Code)
	}
}
