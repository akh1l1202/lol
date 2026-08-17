package coach

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler exposes the nudge-polling endpoint.
type Handler struct {
	Svc *Service
}

// NewHandler creates a coach handler backed by the given service.
func NewHandler(svc *Service) *Handler {
	return &Handler{Svc: svc}
}

// Nudges handles GET /api/coach/nudges — returns and clears the authenticated
// user's pending coaching nudges produced asynchronously after a mood check-in.
func (h *Handler) Nudges(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	items := h.Svc.PendingNudges(userID.(string))
	c.JSON(http.StatusOK, gin.H{"nudges": items, "count": len(items)})
}
