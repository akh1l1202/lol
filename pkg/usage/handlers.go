package usage

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/ai"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/notification"
	"github.com/gin-gonic/gin"
)

type userLoader interface {
	GetFCMToken(userID string) (string, bool)
}

// Handler exposes usage ingest/read endpoints.
type Handler struct {
	Repo       Repository
	AIClient   *ai.Client
	Dispatcher notification.Dispatcher
	UserRepo   userLoader
}

// NewHandler creates a usage handler backed by the given repository.
func NewHandler(repo Repository) *Handler {
	return NewHandlerWithAnalysis(repo, nil, nil, nil)
}

// NewHandlerWithAnalysis creates a usage handler with AI analysis and notification support.
func NewHandlerWithAnalysis(repo Repository, aiClient *ai.Client, dispatcher notification.Dispatcher, userRepo userLoader) *Handler {
	return &Handler{
		Repo:       repo,
		AIClient:   aiClient,
		Dispatcher: dispatcher,
		UserRepo:   userRepo,
	}
}

type ingestEvent struct {
	AppPackage string    `json:"app_package" binding:"required"`
	StartedAt  time.Time `json:"started_at" binding:"required"`
	DurationS  int       `json:"duration_s"`
}

type ingestRequest struct {
	Events []ingestEvent `json:"events" binding:"required,min=1,dive"`
}

// Ingest handles POST /api/usage — stores a batch of usage events for the
// authenticated user. The user is taken from the JWT, never from the body.
func (h *Handler) Ingest(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	var req ingestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	events := make([]Event, len(req.Events))
	for i, e := range req.Events {
		events[i] = Event{
			AppPackage: e.AppPackage,
			StartedAt:  e.StartedAt,
			DurationS:  e.DurationS,
		}
	}

	if err := h.Repo.Insert(userID.(string), events); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store usage events"})
		return
	}

	// Trigger asynchronous background distraction analysis if services are wired
	if h.AIClient != nil && h.Dispatcher != nil && h.UserRepo != nil {
		go h.analyzeAndNudge(userID.(string), events)
	}

	c.JSON(http.StatusCreated, gin.H{"stored": len(events)})
}

// List handles GET /api/usage — returns the authenticated user's events.
// Optional query params: from, to (RFC3339); limit (default 200, max 1000).
// Defaults to the last 30 days.
func (h *Handler) List(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	to := time.Now()
	from := to.AddDate(0, 0, -30)
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}

	limit := 200
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	events, err := h.Repo.ListByUser(userID.(string), from, to, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load usage events"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"events": events, "count": len(events)})
}

func (h *Handler) analyzeAndNudge(userID string, events []Event) {
	var longestEvent *Event
	for i := range events {
		if longestEvent == nil || events[i].DurationS > longestEvent.DurationS {
			longestEvent = &events[i]
		}
	}

	if longestEvent == nil {
		return
	}

	appName := appDisplayName(longestEvent.AppPackage)
	durationMin := float64(longestEvent.DurationS) / 60.0

	// Check if we have a token
	token, found := h.UserRepo.GetFCMToken(userID)
	if !found || token == "" {
		return
	}

	// Request classification
	res := h.AIClient.AnalyzeSession(ai.AnalyzeRequest{
		AppName:                appName,
		SessionDurationMinutes: durationMin,
		UnlockCount:            5,
		CurrentFeeling:         "distracted",
	})

	if res.DistractionFlag == 1 && res.CoachNudge != "" {
		err := h.Dispatcher.Send(token, notification.Message{
			Title: "AI Focus Coach",
			Body:  res.CoachNudge,
		})
		if err != nil {
			log.Printf("Failed to send background distraction notification: %v", err)
		}
	}
}

func appDisplayName(pkg string) string {
	switch pkg {
	case "com.google.android.youtube":
		return "YouTube"
	case "com.instagram.android":
		return "Instagram"
	case "com.zhiliaoapp.musically":
		return "TikTok"
	case "com.facebook.katana":
		return "Facebook"
	case "com.twitter.android":
		return "Twitter"
	case "com.snapchat.android":
		return "Snapchat"
	case "com.reddit.frontpage":
		return "Reddit"
	case "com.netflix.mediaclient":
		return "Netflix"
	default:
		parts := strings.Split(pkg, ".")
		if len(parts) > 0 {
			name := parts[len(parts)-1]
			if len(name) > 0 {
				return strings.Title(name)
			}
		}
		return pkg
	}
}
