package ai

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler exposes the phone-facing coaching endpoints, backed by the Python AI
// service via Client.
type Handler struct {
	Client *Client
}

// NewHandler creates a coach handler backed by the given AI client.
func NewHandler(client *Client) *Handler {
	return &Handler{Client: client}
}

type scheduleInput struct {
	Goals          []string `json:"goals"`
	MorningBlock   string   `json:"morning_block"`
	AfternoonBlock string   `json:"afternoon_block"`
}

// GenerateSchedule handles POST /api/schedule/generate.
func (h *Handler) GenerateSchedule(c *gin.Context) {
	if _, ok := c.Get("user_id"); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}
	var in scheduleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result := h.Client.GenerateSchedule(ScheduleRequest{
		Goals:          in.Goals,
		MorningBlock:   in.MorningBlock,
		AfternoonBlock: in.AfternoonBlock,
	})
	c.JSON(http.StatusOK, result)
}

type analyzeInput struct {
	AppName                string  `json:"app_name" binding:"required"`
	SessionDurationMinutes float64 `json:"session_duration_minutes"`
	UnlockCount            int     `json:"unlock_count"`
	CurrentFeeling         string  `json:"current_feeling"`
}

type chatInput struct {
	Message string `json:"message" binding:"required"`
}

// Analyze handles POST /api/coach/analyze — the phone's single entry point for
// session coaching. The Go core fans out to the Python AI service so the phone
// never calls Python directly.
func (h *Handler) Analyze(c *gin.Context) {
	if _, ok := c.Get("user_id"); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	var in analyzeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res := h.Client.AnalyzeSession(AnalyzeRequest{
		AppName:                in.AppName,
		SessionDurationMinutes: in.SessionDurationMinutes,
		UnlockCount:            in.UnlockCount,
		CurrentFeeling:         in.CurrentFeeling,
	})
	c.JSON(http.StatusOK, res)
}

// Chat handles POST /api/coach/chat.
func (h *Handler) Chat(c *gin.Context) {
	if _, ok := c.Get("user_id"); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	var in chatInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res := h.Client.Chat(ChatRequest{
		Message: in.Message,
	})

	c.JSON(http.StatusOK, res)
}