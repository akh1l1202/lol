package mood

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// CoachEnqueuer schedules an asynchronous coaching job for a mood check-in.
// It is optional: when nil, check-ins are saved without triggering coaching.
// Implemented by *coach.Service.
type CoachEnqueuer interface {
	EnqueueMood(userID string, energy, stress, burnoutRisk int, reflection string) (jobID string, ok bool)
}

// Handler exposes mood check-in endpoints.
type Handler struct {
	Repo  Repository
	Coach CoachEnqueuer
}

// NewHandler creates a mood handler backed by the given repository.
func NewHandler(repo Repository) *Handler {
	return &Handler{Repo: repo}
}

// NewHandlerWithCoach creates a mood handler that also enqueues an async
// coaching job on each check-in.
func NewHandlerWithCoach(repo Repository, coach CoachEnqueuer) *Handler {
	return &Handler{Repo: repo, Coach: coach}
}

type checkinInput struct {
	Energy      int    `json:"energy" binding:"required,min=1,max=5"`
	Stress      int    `json:"stress" binding:"required,min=1,max=5"`
	BurnoutRisk int    `json:"burnout_risk" binding:"min=0,max=100"`
	Reflection  string `json:"reflection"`
}

// Create handles POST /api/mood for the authenticated user.
func (h *Handler) Create(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	var in checkinInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	saved, err := h.Repo.Create(Checkin{
		UserID:      userID.(string),
		Energy:      in.Energy,
		Stress:      in.Stress,
		BurnoutRisk: in.BurnoutRisk,
		Reflection:  in.Reflection,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save check-in"})
		return
	}

	// Kick off coaching off the request path. The check-in is already saved, so
	// a full queue just means no async nudge — fall back to a plain 201.
	if h.Coach != nil {
		if jobID, ok := h.Coach.EnqueueMood(saved.UserID, saved.Energy, saved.Stress, saved.BurnoutRisk, saved.Reflection); ok {
			c.JSON(http.StatusAccepted, gin.H{"checkin": saved, "job_id": jobID})
			return
		}
	}

	c.JSON(http.StatusCreated, saved)
}

// List handles GET /api/mood — the user's recent check-ins (?limit, default 50).
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load check-ins"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"checkins": items, "count": len(items)})
}
