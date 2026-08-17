package coach

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/ai"
	"github.com/gin-gonic/gin"
)

// fakeCoach is an in-test moodCoach. ok controls whether it reports success.
type fakeCoach struct {
	ok       bool
	greeting string
	calls    int
	lastReq  ai.MoodRequest
}

func (f *fakeCoach) MoodCheckin(req ai.MoodRequest) (ai.MoodResult, bool) {
	f.calls++
	f.lastReq = req
	if !f.ok {
		return ai.MoodResult{}, false
	}
	return ai.MoodResult{Status: "success", AIGreeting: f.greeting}, true
}

func TestEnqueueAndHandleStoresNudge(t *testing.T) {
	fc := &fakeCoach{ok: true, greeting: "Breathe — you've got this."}
	svc := NewService(fc, 0, 8) // 0 workers: drive the job manually for determinism

	// Mock the clock
	baseTime := time.Now()
	svc.now = func() time.Time { return baseTime }

	jobID, ok := svc.EnqueueMood("u1", 4, 5, 80, "swamped")
	if !ok || jobID == "" {
		t.Fatalf("expected enqueue to succeed with a job id, got ok=%v id=%q", ok, jobID)
	}

	svc.handle(<-svc.jobs)

	// 1-5 high energy/stress and 80 burnout should map to high labels.
	if fc.lastReq.EnergyLevel != "high" || fc.lastReq.StressLevel != "high" || fc.lastReq.BurnoutRisk != "high" {
		t.Errorf("unexpected label mapping: %+v", fc.lastReq)
	}

	nudges := svc.PendingNudges("u1")
	if len(nudges) != 1 {
		t.Fatalf("expected 1 pending nudge, got %d", len(nudges))
	}
	if nudges[0].Message != "Breathe — you've got this." || nudges[0].JobID != jobID {
		t.Errorf("unexpected nudge: %+v", nudges[0])
	}

	// 2-minute persistence: a second drain within 2 minutes STILL returns the nudge.
	if again := svc.PendingNudges("u1"); len(again) != 1 {
		t.Errorf("expected nudge to persist within 2 minutes, got %d", len(again))
	}

	// Advance clock by 3 minutes to expire the nudge
	svc.now = func() time.Time { return baseTime.Add(3 * time.Minute) }
	if again := svc.PendingNudges("u1"); len(again) != 0 {
		t.Errorf("expected nudges to be expired after 2 minutes, got %d", len(again))
	}
}

func TestHandleDropsNudgeWhenAIUnavailable(t *testing.T) {
	fc := &fakeCoach{ok: false}
	svc := NewService(fc, 0, 8)

	svc.EnqueueMood("u1", 1, 1, 0, "")
	svc.handle(<-svc.jobs)

	if n := svc.PendingNudges("u1"); len(n) != 0 {
		t.Fatalf("expected no nudge when AI is unavailable, got %d", len(n))
	}
}

func TestEnqueueReportsFullQueue(t *testing.T) {
	fc := &fakeCoach{ok: true, greeting: "hi"}
	svc := NewService(fc, 0, 1) // buffer 1, no workers draining

	if _, ok := svc.EnqueueMood("u1", 3, 3, 50, ""); !ok {
		t.Fatal("first enqueue should fit in the buffer")
	}
	if _, ok := svc.EnqueueMood("u1", 3, 3, 50, ""); ok {
		t.Fatal("second enqueue should report a full queue")
	}
}

func TestNudgesEndpointDrainsForUser(t *testing.T) {
	fc := &fakeCoach{ok: true, greeting: "keep going"}
	svc := NewService(fc, 0, 8)
	svc.EnqueueMood("u1", 3, 3, 10, "")
	svc.handle(<-svc.jobs)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", "u1"); c.Next() })
	r.GET("/coach/nudges", NewHandler(svc).Nudges)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/coach/nudges", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "keep going") {
		t.Errorf("expected nudge in body, got %s", body)
	}
}
