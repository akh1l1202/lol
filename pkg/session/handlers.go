package session

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler exposes focus-session endpoints.
type Handler struct {
	Repo Repository
}

// NewHandler creates a session handler backed by the given repository.
func NewHandler(repo Repository) *Handler {
	return &Handler{Repo: repo}
}

type sessionInput struct {
	Task          string     `json:"task"`
	StartedAt     time.Time  `json:"started_at" binding:"required"`
	EndedAt       *time.Time `json:"ended_at"`
	Status        string     `json:"status"`
	PomodoroIndex int        `json:"pomodoro_index"`
}

// Create handles POST /api/sessions for the authenticated user.
func (h *Handler) Create(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	var in sessionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	saved, err := h.Repo.Create(Session{
		UserID:        userID.(string),
		Task:          in.Task,
		StartedAt:     in.StartedAt,
		EndedAt:       in.EndedAt,
		Status:        in.Status,
		PomodoroIndex: in.PomodoroIndex,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

	c.JSON(http.StatusCreated, saved)
}

// updateInput is the PATCH body. Every field is optional; only the ones present
// are applied, so the common case (end a running session) sends just ended_at
// and status.
type updateInput struct {
	Task          *string    `json:"task"`
	EndedAt       *time.Time `json:"ended_at"`
	Status        *string    `json:"status"`
	PomodoroIndex *int       `json:"pomodoro_index"`
}

// Update handles PATCH /api/sessions/:id — advance a session's lifecycle
// (e.g. running -> ended) for the authenticated user.
func (h *Handler) Update(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	var in updateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := h.Repo.Update(userID.(string), id, SessionUpdate{
		Task:          in.Task,
		EndedAt:       in.EndedAt,
		Status:        in.Status,
		PomodoroIndex: in.PomodoroIndex,
	})
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update session"})
		return
	}

	c.JSON(http.StatusOK, updated)
}

// List handles GET /api/sessions — the user's recent sessions (?limit, default 50).
func (h *Handler) List(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	items, err := h.Repo.ListByUser(userID.(string), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load sessions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessions": items, "count": len(items)})
}
