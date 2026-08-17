// Package ai is the Go core's server-to-server client for the Python AI
// service (backend/ai-service). Keeping all AI calls here means the phone only
// ever talks to the Go API; the Go API fans out to Python. A short response
// cache and a circuit breaker keep the phone-facing endpoints responsive and
// let them degrade gracefully when the AI service is slow or down.
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AnalyzeRequest mirrors the Python service's SessionData.
type AnalyzeRequest struct {
	AppName                string  `json:"app_name"`
	SessionDurationMinutes float64 `json:"session_duration_minutes"`
	UnlockCount            int     `json:"unlock_count"`
	CurrentFeeling         string  `json:"current_feeling,omitempty"`
}

// AnalyzeResult mirrors the Python service's /analyze_session response.
type AnalyzeResult struct {
	AppName         string `json:"app_name"`
	DistractionFlag int    `json:"distraction_flag"`
	CoachNudge      string `json:"coach_nudge"`
}

// MoodRequest mirrors the Python service's MoodData.
type MoodRequest struct {
	EnergyLevel string `json:"energy_level"`
	StressLevel string `json:"stress_level"`
	BurnoutRisk string `json:"burnout_risk"`
	Reflection  string `json:"reflection,omitempty"`
}

// MoodResult mirrors the Python service's /mood_checkin response.
type MoodResult struct {
	Status     string `json:"status"`
	AIGreeting string `json:"ai_greeting"`
}

// pythonNudge matches the rich NudgeUI object returned by the Python AI service
type pythonNudge struct {
	NudgeType    string `json:"nudge_type"`
	Headline     string `json:"headline"`
	BodyText     string `json:"body_text"`
	ActionButton string `json:"action_button"`
}

// pythonAnalyzeResponse matches the rich response schema of /analyze_session
type pythonAnalyzeResponse struct {
	AppName         string      `json:"app_name"`
	DistractionFlag int         `json:"distraction_flag"`
	CoachNudge      pythonNudge `json:"coach_nudge"`
}

// pythonMoodResponse matches the rich response schema of /mood_checkin
type pythonMoodResponse struct {
	Status     string      `json:"status"`
	AIGreeting pythonNudge `json:"ai_greeting"`
}

// ScheduleRequest is sent to /generate_schedule.
type ScheduleRequest struct {
	Goals          []string `json:"goals"`
	MorningBlock   string   `json:"morning_block"`
	AfternoonBlock string   `json:"afternoon_block"`
}

// ScheduleSlot is one slot in the generated daily schedule.
type ScheduleSlot struct {
	Time        string `json:"time"`
	Title       string `json:"title"`
	Duration    string `json:"duration"`
	Description string `json:"description"`
	Type        string `json:"type"` // focus | break | admin | meal
}

// ScheduleResult wraps the array returned by /generate_schedule.
type ScheduleResult struct {
	Slots []ScheduleSlot `json:"slots"`
}

// ChatRequest is sent to the Python /chat endpoint.
type ChatRequest struct {
    Message string `json:"message"`
}

// ChatResult mirrors the Python /chat response.
type ChatResult struct {
    Reply string `json:"reply"`
}

// Client calls the Python AI service. It is safe for concurrent use.
type Client struct {
	baseURL string
	http    *http.Client
	cache   *ttlCache
	breaker *breaker
}

// NewClient returns a client for the AI service at baseURL (falling back to
// http://127.0.0.1:8000 when empty), with sensible cache/breaker defaults.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8000"
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 12 * time.Second},
		cache:   newTTLCache(2*time.Minute, time.Now),
		breaker: newBreaker(3, 30*time.Second, time.Now),
	}
}

// AnalyzeSession classifies one usage session and returns a coach nudge. It
// serves identical requests from a short cache, and when the AI service is
// failing (circuit open) it returns an encouraging fallback rather than an
// error, so the phone always gets a usable response.
func (c *Client) AnalyzeSession(req AnalyzeRequest) AnalyzeResult {
	key := analyzeCacheKey(req)
	if v, ok := c.cache.get(key); ok {
		return v
	}
	if !c.breaker.allow() {
		return analyzeFallback(req)
	}

	var out pythonAnalyzeResponse
	if err := c.post("/analyze_session", req, &out); err != nil {
		c.breaker.fail()
		return analyzeFallback(req)
	}
	c.breaker.success()
	
	res := AnalyzeResult{
		AppName:         out.AppName,
		DistractionFlag: out.DistractionFlag,
		CoachNudge:      out.CoachNudge.BodyText, // Extract the string nudge for Go/Flutter compatibility
	}
	
	c.cache.set(key, res)
	return res
}

// MoodCheckin generates an empathetic greeting from mood metrics. Returns
// (result, false) when the AI service is unavailable so the caller can decide
// how to handle the degraded case (e.g. retry later in a job worker).
func (c *Client) MoodCheckin(req MoodRequest) (MoodResult, bool) {
	if !c.breaker.allow() {
		return MoodResult{}, false
	}
	var out pythonMoodResponse
	if err := c.post("/mood_checkin", req, &out); err != nil {
		c.breaker.fail()
		return MoodResult{}, false
	}
	c.breaker.success()
	
	res := MoodResult{
		Status:     out.Status,
		AIGreeting: out.AIGreeting.BodyText, // Extract the string greeting
	}
	return res, true
}

// GenerateSchedule asks the AI service to produce a personalised daily
// schedule. It always returns a usable result — failures produce a minimal
// fallback so the Flutter screen never blocks on a dead AI service.
func (c *Client) GenerateSchedule(req ScheduleRequest) ScheduleResult {
	if len(req.Goals) == 0 {
		req.Goals = []string{"Deep Work"}
	}
	if req.MorningBlock == "" {
		req.MorningBlock = "9:00 AM - 12:00 PM"
	}
	if req.AfternoonBlock == "" {
		req.AfternoonBlock = "2:00 PM - 5:00 PM"
	}
	if !c.breaker.allow() {
		return scheduleFallback(req)
	}
	var out ScheduleResult
	if err := c.post("/generate_schedule", req, &out); err != nil {
		c.breaker.fail()
		return scheduleFallback(req)
	}
	c.breaker.success()
	return out
}

// Chat sends a user message to the Python AI service and returns its reply.
// If the AI service is unavailable, a friendly fallback reply is returned.
func (c *Client) Chat(req ChatRequest) ChatResult {
	if !c.breaker.allow() {
		return ChatResult{
			Reply: "I'm having a little trouble right now. Please try again in a moment.",
		}
	}

	var out ChatResult
	if err := c.post("/chat", req, &out); err != nil {
		c.breaker.fail()
		return ChatResult{
			Reply: "I'm having a little trouble right now. Please try again in a moment.",
		}
	}

	c.breaker.success()
	return out
}

func scheduleFallback(req ScheduleRequest) ScheduleResult {
	first := "Deep Work"
	if len(req.Goals) > 0 {
		first = req.Goals[0]
	}
	second := "Review & Planning"
	if len(req.Goals) > 1 {
		second = req.Goals[1]
	}
	morningStart := req.MorningBlock
	if i := len(req.MorningBlock); i > 0 {
		if parts := splitFirst(req.MorningBlock, " - "); parts != "" {
			morningStart = parts
		}
	}
	afternoonStart := req.AfternoonBlock
	if parts := splitFirst(req.AfternoonBlock, " - "); parts != "" {
		afternoonStart = parts
	}
	return ScheduleResult{Slots: []ScheduleSlot{
		{morningStart, first + " Session", "90m", "Start the day with a protected block.", "focus"},
		{"10:30 AM", "Mindful Break", "15m", "Step away from screens. Stretch or walk.", "break"},
		{afternoonStart, second + " Block", "75m", "Use the next focus window for your second priority.", "focus"},
		{"4:00 PM", "Wrap-up & Plan Tomorrow", "20m", "Close open loops and set tomorrow's top tasks.", "admin"},
	}}
}

func splitFirst(s, sep string) string {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i]
		}
	}
	return ""
}

func (c *Client) post(path string, payload, out interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := c.http.Post(c.baseURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ai service %s returned %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func analyzeCacheKey(r AnalyzeRequest) string {
	return fmt.Sprintf("%s|%.1f|%d|%s", r.AppName, r.SessionDurationMinutes, r.UnlockCount, r.CurrentFeeling)
}

func analyzeFallback(r AnalyzeRequest) AnalyzeResult {
	return AnalyzeResult{
		AppName:         r.AppName,
		DistractionFlag: 0,
		CoachNudge:      "Your coach is taking a quick break — keep going, you've got this.",
	}
}

// --- circuit breaker -------------------------------------------------------

// breaker opens after `threshold` consecutive failures and stays open for
// `cooldown`, during which allow() returns false so callers skip the AI service.
type breaker struct {
	mu        sync.Mutex
	failures  int
	threshold int
	cooldown  time.Duration
	openUntil time.Time
	now       func() time.Time
}

func newBreaker(threshold int, cooldown time.Duration, now func() time.Time) *breaker {
	return &breaker{threshold: threshold, cooldown: cooldown, now: now}
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.now().Before(b.openUntil)
}

func (b *breaker) fail() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = b.now().Add(b.cooldown)
		b.failures = 0
	}
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
}

// --- ttl cache -------------------------------------------------------------

type cacheEntry struct {
	val AnalyzeResult
	exp time.Time
}

type ttlCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
	now     func() time.Time
}

func newTTLCache(ttl time.Duration, now func() time.Time) *ttlCache {
	return &ttlCache{entries: make(map[string]cacheEntry), ttl: ttl, now: now}
}

func (c *ttlCache) get(key string) (AnalyzeResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || c.now().After(e.exp) {
		return AnalyzeResult{}, false
	}
	return e.val, true
}

func (c *ttlCache) set(key string, v AnalyzeResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{val: v, exp: c.now().Add(c.ttl)}
}
