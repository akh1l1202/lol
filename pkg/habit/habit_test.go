package habit

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeRepo is an in-memory habit Repository for handler tests (no database).
type fakeRepo struct {
	habits []Habit
	logs   []Log
	nextID int64
}

func (f *fakeRepo) ListHabits(userID string) ([]Habit, error) {
	out := make([]Habit, 0)
	for _, h := range f.habits {
		if h.UserID == userID {
			out = append(out, h)
		}
	}
	return out, nil
}

func (f *fakeRepo) CreateHabit(h Habit) (Habit, error) {
	f.nextID++
	h.ID = f.nextID
	if h.Kind == "" {
		h.Kind = KindDaily
	}
	if h.Target < 1 {
		h.Target = 1
	}
	h.CreatedAt = time.Now()
	f.habits = append(f.habits, h)
	return h, nil
}

func (f *fakeRepo) find(userID string, id int64) *Habit {
	for i := range f.habits {
		if f.habits[i].ID == id && f.habits[i].UserID == userID {
			return &f.habits[i]
		}
	}
	return nil
}

func (f *fakeRepo) UpdateHabit(userID string, id int64, name *string, target *int, color *string, archived *bool) (Habit, error) {
	h := f.find(userID, id)
	if h == nil {
		return Habit{}, sql.ErrNoRows
	}
	if name != nil {
		h.Name = *name
	}
	if target != nil {
		h.Target = *target
	}
	if color != nil {
		h.Color = *color
	}
	if archived != nil {
		h.Archived = *archived
	}
	return *h, nil
}

func (f *fakeRepo) DeleteHabit(userID string, id int64) error {
	for i := range f.habits {
		if f.habits[i].ID == id && f.habits[i].UserID == userID {
			f.habits = append(f.habits[:i], f.habits[i+1:]...)
			filteredLogs := f.logs[:0]
			for _, l := range f.logs {
				if l.HabitID != id {
					filteredLogs = append(filteredLogs, l)
				}
			}
			f.logs = filteredLogs
			return nil
		}
	}
	return sql.ErrNoRows
}

func (f *fakeRepo) ListLogs(userID string, from, to time.Time) ([]Log, error) {
	out := make([]Log, 0)
	for _, l := range f.logs {
		if !l.LoggedOn.Before(from) && l.LoggedOn.Before(to) {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeRepo) ApplyLog(userID string, habitID int64, day time.Time, delta int) (int, error) {
	if f.find(userID, habitID) == nil {
		return 0, sql.ErrNoRows
	}
	for i := range f.logs {
		if f.logs[i].HabitID == habitID && f.logs[i].LoggedOn.Equal(day) {
			f.logs[i].Count += delta
			if f.logs[i].Count < 0 {
				f.logs[i].Count = 0
			}
			return f.logs[i].Count, nil
		}
	}
	count := delta
	if count < 0 {
		count = 0
	}
	f.logs = append(f.logs, Log{HabitID: habitID, LoggedOn: day, Count: count})
	return count, nil
}

// fixedNow pins "today" so week/month aggregation is deterministic.
var fixedNow = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC) // a Wednesday

func setupRouter(repo Repository, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if userID != "" {
		r.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Next() })
	}
	h := NewHandler(repo)
	h.Now = func() time.Time { return fixedNow }
	r.GET("/habits", h.List)
	r.POST("/habits", h.Create)
	r.PATCH("/habits/:id", h.Update)
	r.DELETE("/habits/:id", h.Delete)
	r.POST("/habits/:id/log", h.Log)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func seedHabit(f *fakeRepo, userID, name, kind string, target int) Habit {
	h, _ := f.CreateHabit(Habit{UserID: userID, Name: name, Kind: kind, Target: target})
	return h
}

func TestCreateStoresHabitForUser(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")

	w := do(r, http.MethodPost, "/habits", `{"name":"Read","kind":"weekly","target":3,"color":"#fff"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	if len(repo.habits) != 1 {
		t.Fatalf("expected 1 habit stored, got %d", len(repo.habits))
	}
	got := repo.habits[0]
	if got.UserID != "u1" || got.Name != "Read" || got.Kind != "weekly" || got.Target != 3 {
		t.Errorf("stored fields mismatch: %+v", got)
	}
}

func TestCreateRejectsMissingName(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")
	w := do(r, http.MethodPost, "/habits", `{"kind":"daily"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", w.Code)
	}
}

func TestCreateRejectsBadKind(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "u1")
	w := do(r, http.MethodPost, "/habits", `{"name":"X","kind":"monthly"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad kind, got %d", w.Code)
	}
}

func TestCreateRequiresUserContext(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "")
	w := do(r, http.MethodPost, "/habits", `{"name":"X"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without user context, got %d", w.Code)
	}
}

func TestLogTogglesTodayAndAggregates(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")
	h := seedHabit(repo, "u1", "Meditate", "daily", 1)

	// Log today -> completed.
	w := do(r, http.MethodPost, "/habits/"+itoa(h.ID)+"/log", `{"delta":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 logging, got %d (%s)", w.Code, w.Body.String())
	}

	// List should now report completed_today and today in month_days.
	lw := do(r, http.MethodGet, "/habits", "")
	var resp struct {
		Habits []habitView `json:"habits"`
	}
	if err := json.Unmarshal(lw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(resp.Habits) != 1 {
		t.Fatalf("expected 1 habit, got %d", len(resp.Habits))
	}
	hv := resp.Habits[0]
	if !hv.CompletedToday || hv.TodayCount != 1 {
		t.Errorf("expected completed today, got %+v", hv)
	}
	if hv.WeekCount != 1 {
		t.Errorf("expected week_count 1, got %d", hv.WeekCount)
	}
	if len(hv.MonthDays) != 1 || hv.MonthDays[0] != fixedNow.Day() {
		t.Errorf("expected month_days=[%d], got %v", fixedNow.Day(), hv.MonthDays)
	}

	// Un-log -> back to zero, clamped.
	do(r, http.MethodPost, "/habits/"+itoa(h.ID)+"/log", `{"delta":-1}`)
	do(r, http.MethodPost, "/habits/"+itoa(h.ID)+"/log", `{"delta":-1}`)
	lw2 := do(r, http.MethodGet, "/habits", "")
	_ = json.Unmarshal(lw2.Body.Bytes(), &resp)
	if resp.Habits[0].CompletedToday || resp.Habits[0].WeekCount != 0 {
		t.Errorf("expected reset to zero, got %+v", resp.Habits[0])
	}
}

func TestLogRejectsUnownedHabit(t *testing.T) {
	repo := &fakeRepo{}
	seedHabit(repo, "owner", "Theirs", "daily", 1) // id 1 belongs to someone else
	r := setupRouter(repo, "u1")

	w := do(r, http.MethodPost, "/habits/1/log", `{"delta":1}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 logging an unowned habit, got %d", w.Code)
	}
}

func TestDeleteRemovesHabit(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")
	h := seedHabit(repo, "u1", "Gone", "daily", 1)

	w := do(r, http.MethodDelete, "/habits/"+itoa(h.ID), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	lw := do(r, http.MethodGet, "/habits", "")
	var resp struct {
		Habits []habitView `json:"habits"`
	}
	_ = json.Unmarshal(lw.Body.Bytes(), &resp)
	if len(resp.Habits) != 0 {
		t.Errorf("expected deleted habit removed from list, got %d", len(resp.Habits))
	}
}

func TestUpdateRenamesHabit(t *testing.T) {
	repo := &fakeRepo{}
	r := setupRouter(repo, "u1")
	h := seedHabit(repo, "u1", "Old", "daily", 1)

	w := do(r, http.MethodPatch, "/habits/"+itoa(h.ID), `{"name":"New","target":5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if repo.habits[0].Name != "New" || repo.habits[0].Target != 5 {
		t.Errorf("expected rename+retarget, got %+v", repo.habits[0])
	}
}

func TestUpdateMissingHabitIs404(t *testing.T) {
	r := setupRouter(&fakeRepo{}, "u1")
	w := do(r, http.MethodPatch, "/habits/999", `{"name":"X"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
