package habit

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler exposes habit CRUD and per-day logging endpoints.
type Handler struct {
	Repo Repository
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// NewHandler creates a habit handler backed by the given repository.
func NewHandler(repo Repository) *Handler {
	return &Handler{Repo: repo, Now: time.Now}
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// habitView is the aggregated per-habit payload the app renders directly.
type habitView struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Target         int    `json:"target"`
	Color          string `json:"color"`
	CompletedToday bool   `json:"completed_today"`
	TodayCount     int    `json:"today_count"`
	WeekCount      int    `json:"week_count"`
	MonthDays      []int  `json:"month_days"`
}

// List handles GET /api/habits — habits plus today's completion, the current
// week's count, and the days of the current month each habit was logged.
func (h *Handler) List(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}
	uid := userID.(string)

	habits, err := h.Repo.ListHabits(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load habits"})
		return
	}

	// Work in UTC throughout: logged_on is a tz-naive DATE that comes back as
	// UTC midnight, so "today"/week/month bounds must be UTC to line up.
	now := h.now().UTC()
	today := dayOf(now)
	weekStart := startOfWeek(today)
	weekEnd := weekStart.AddDate(0, 0, 7)
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Fetch a window covering both the current week and month in one query.
	from := minTime(weekStart, monthStart)
	to := maxTime(weekEnd, monthEnd)
	logs, err := h.Repo.ListLogs(uid, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load habit logs"})
		return
	}

	type agg struct {
		today     int
		week      int
		monthDays []int
	}
	byHabit := make(map[int64]*agg, len(habits))
	for _, l := range logs {
		if l.Count <= 0 {
			continue
		}
		day := dayOf(l.LoggedOn)
		a := byHabit[l.HabitID]
		if a == nil {
			a = &agg{}
			byHabit[l.HabitID] = a
		}
		if day.Equal(today) {
			a.today += l.Count
		}
		if !day.Before(weekStart) && day.Before(weekEnd) {
			a.week += l.Count
		}
		if !day.Before(monthStart) && day.Before(monthEnd) {
			a.monthDays = append(a.monthDays, day.Day())
		}
	}

	views := make([]habitView, 0, len(habits))
	for _, hb := range habits {
		a := byHabit[hb.ID]
		v := habitView{
			ID:        hb.ID,
			Name:      hb.Name,
			Kind:      hb.Kind,
			Target:    hb.Target,
			Color:     hb.Color,
			MonthDays: []int{},
		}
		if a != nil {
			v.TodayCount = a.today
			v.CompletedToday = a.today > 0
			v.WeekCount = a.week
			if len(a.monthDays) > 0 {
				v.MonthDays = a.monthDays
			}
		}
		views = append(views, v)
	}

	c.JSON(http.StatusOK, gin.H{"habits": views, "count": len(views)})
}

type createInput struct {
	Name      string `json:"name" binding:"required"`
	Kind      string `json:"kind"`
	Target    int    `json:"target"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
}

// Create handles POST /api/habits.
func (h *Handler) Create(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	var in createInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if in.Kind != "" && in.Kind != KindDaily && in.Kind != KindWeekly {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be 'daily' or 'weekly'"})
		return
	}

	saved, err := h.Repo.CreateHabit(Habit{
		UserID:    userID.(string),
		Name:      in.Name,
		Kind:      in.Kind,
		Target:    in.Target,
		Color:     in.Color,
		SortOrder: in.SortOrder,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create habit"})
		return
	}
	c.JSON(http.StatusCreated, saved)
}

type updateInput struct {
	Name     *string `json:"name"`
	Target   *int    `json:"target"`
	Color    *string `json:"color"`
	Archived *bool   `json:"archived"`
}

// Update handles PATCH /api/habits/:id.
func (h *Handler) Update(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid habit id"})
		return
	}

	var in updateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.Repo.UpdateHabit(userID.(string), id, in.Name, in.Target, in.Color, in.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "habit not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update habit"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete handles DELETE /api/habits/:id — permanently removes the habit.
func (h *Handler) Delete(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid habit id"})
		return
	}

	err = h.Repo.DeleteHabit(userID.(string), id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "habit not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete habit"})
		return
	}
	c.Status(http.StatusNoContent)
}

type logInput struct {
	Delta int `json:"delta"`
}

// Log handles POST /api/habits/:id/log — adjusts today's count by delta.
func (h *Handler) Log(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid habit id"})
		return
	}

	var in logInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if in.Delta == 0 {
		in.Delta = 1 // a bare log marks today done
	}

	count, err := h.Repo.ApplyLog(userID.(string), id, dayOf(h.now().UTC()), in.Delta)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "habit not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to log habit"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"habit_id": id, "today_count": count, "completed_today": count > 0})
}

// dayOf truncates a time to its calendar date in the same location.
func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// startOfWeek returns the Monday on or before the given day.
func startOfWeek(day time.Time) time.Time {
	offset := (int(day.Weekday()) + 6) % 7 // Mon=0 ... Sun=6
	return day.AddDate(0, 0, -offset)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
