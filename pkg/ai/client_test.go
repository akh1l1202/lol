package ai

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient builds a Client pointed at srv with fast, test-friendly cache
// and breaker settings driven by an injectable clock.
func newTestClient(srv *httptest.Server, now func() time.Time, threshold int, cooldown time.Duration) *Client {
	return &Client{
		baseURL: srv.URL,
		http:    srv.Client(),
		cache:   newTTLCache(2*time.Minute, now),
		breaker: newBreaker(threshold, cooldown, now),
	}
}

func TestAnalyzeSessionReturnsNudge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analyze_session" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"app_name":"Instagram","distraction_flag":1,"coach_nudge":{"nudge_type":"encouragement","headline":"Reset","body_text":"Take a breath.","action_button":"Focus"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv, time.Now, 3, time.Minute)
	res := c.AnalyzeSession(AnalyzeRequest{AppName: "Instagram", SessionDurationMinutes: 30, UnlockCount: 12})

	if res.DistractionFlag != 1 || res.CoachNudge != "Take a breath." {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestAnalyzeSessionCachesIdenticalRequests(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`{"app_name":"X","distraction_flag":0,"coach_nudge":{"nudge_type":"encouragement","headline":"Ok","body_text":"ok","action_button":"Ok"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv, time.Now, 3, time.Minute)
	req := AnalyzeRequest{AppName: "X", SessionDurationMinutes: 10, UnlockCount: 1}
	c.AnalyzeSession(req)
	c.AnalyzeSession(req)

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected the second identical call to hit the cache (1 upstream call), got %d", got)
	}
}

func TestAnalyzeSessionFallsBackWhenServiceErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv, time.Now, 3, time.Minute)
	res := c.AnalyzeSession(AnalyzeRequest{AppName: "Y", SessionDurationMinutes: 5, UnlockCount: 1})

	if res.DistractionFlag != 0 || res.CoachNudge == "" {
		t.Fatalf("expected an encouraging fallback, got %+v", res)
	}
	if res.AppName != "Y" {
		t.Errorf("fallback should echo the app name, got %q", res.AppName)
	}
}

func TestBreakerOpensAfterThresholdAndSkipsService(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	base := time.Now()
	clock := base
	c := newTestClient(srv, func() time.Time { return clock }, 2, 30*time.Second)

	// Two failures trip the breaker (threshold 2). Use distinct requests so the
	// cache never serves them.
	c.AnalyzeSession(AnalyzeRequest{AppName: "a", UnlockCount: 1})
	c.AnalyzeSession(AnalyzeRequest{AppName: "b", UnlockCount: 2})
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 upstream attempts before the breaker opened, got %d", got)
	}

	// Breaker now open: this call must not reach the service.
	c.AnalyzeSession(AnalyzeRequest{AppName: "c", UnlockCount: 3})
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected no upstream call while the breaker is open, got %d", got)
	}

	// After the cooldown the breaker allows traffic through again.
	clock = base.Add(31 * time.Second)
	c.AnalyzeSession(AnalyzeRequest{AppName: "d", UnlockCount: 4})
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected the breaker to retry after cooldown, got %d", got)
	}
}

func TestMoodCheckinReportsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(srv, time.Now, 3, time.Minute)
	if _, ok := c.MoodCheckin(MoodRequest{EnergyLevel: "low"}); ok {
		t.Fatal("expected MoodCheckin to report unavailable on service error")
	}
}
