package score

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Handler exposes the score endpoint.
type Handler struct {
	svc *Service
}

// NewHandler creates a score handler backed by the given service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Get handles GET /api/score — the focus score for ?date (YYYY-MM-DD, default
// today) for the authenticated user.
func (h *Handler) Get(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	date := time.Now()
	if v := c.Query("date"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
			date = t
		}
	}

	res, err := h.svc.Compute(userID.(string), date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compute score"})
		return
	}

	c.JSON(http.StatusOK, res)
}
