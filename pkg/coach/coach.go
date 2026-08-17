// Package coach runs AI coaching off the request path. A mood check-in enqueues
// a job; background workers call the Python AI service (via pkg/ai) and stash the
// resulting nudge for the user to poll at GET /api/coach/nudges. The queue and
// nudge store are in-process for now — a Redis-backed queue (TASK-24) would let
// this span multiple instances and survive restarts.
package coach

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/ai"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/notification"
)

// moodCoach is the slice of the AI client the worker needs; *ai.Client satisfies
// it. Declared here so the worker can be tested with a fake.
type moodCoach interface {
	MoodCheckin(req ai.MoodRequest) (ai.MoodResult, bool)
}

// userLoader is a slice of the UserRepository the worker needs to load user details (FCM token)
type userLoader interface {
	GetFCMToken(userID string) (string, bool)
}

// MoodJob is a queued request to generate a coaching nudge from a check-in.
type MoodJob struct {
	ID     string
	UserID string
	Mood   ai.MoodRequest
}

// Nudge is a generated coaching message waiting to be delivered to a user.
type Nudge struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Service owns the job queue, the worker pool, and the pending-nudge store.
type Service struct {
	jobs       chan MoodJob
	coach      moodCoach
	dispatcher notification.Dispatcher
	userLoader userLoader
	store      *nudgeStore
	now        func() time.Time
	seq        int64
}

// NewService starts `workers` goroutines draining a buffered queue of `buffer`
// jobs. Pass workers=0 to leave job processing to the caller (used in tests).
func NewService(coach moodCoach, workers, buffer int) *Service {
	return NewServiceWithNotification(coach, nil, nil, workers, buffer)
}

// NewServiceWithNotification starts the service with push notification support.
func NewServiceWithNotification(coach moodCoach, dispatcher notification.Dispatcher, userLoader userLoader, workers, buffer int) *Service {
	s := &Service{
		jobs:       make(chan MoodJob, buffer),
		coach:      coach,
		dispatcher: dispatcher,
		userLoader: userLoader,
		store:      newNudgeStore(),
		now:        time.Now,
	}
	for i := 0; i < workers; i++ {
		go s.run()
	}
	return s
}

func (s *Service) run() {
	for job := range s.jobs {
		s.handle(job)
	}
}

// EnqueueMood schedules a coaching job for a mood check-in and returns the job
// id. ok is false if the queue is full, so the caller can carry on (the
// check-in is still saved) without blocking on coaching.
func (s *Service) EnqueueMood(userID string, energy, stress, burnoutRisk int, reflection string) (string, bool) {
	job := MoodJob{
		ID:     s.nextID("job"),
		UserID: userID,
		Mood: ai.MoodRequest{
			EnergyLevel: levelLabel(energy),
			StressLevel: levelLabel(stress),
			BurnoutRisk: burnoutLabel(burnoutRisk),
			Reflection:  reflection,
		},
	}
	select {
	case s.jobs <- job:
		return job.ID, true
	default:
		return "", false
	}
}

// handle runs one job: calls the AI service and stores the nudge if one came
// back. A degraded AI service (ok=false) simply yields no nudge.
func (s *Service) handle(job MoodJob) {
	res, ok := s.coach.MoodCheckin(job.Mood)
	if !ok || res.AIGreeting == "" {
		return
	}
	s.store.add(job.UserID, Nudge{
		ID:        s.nextID("nudge"),
		JobID:     job.ID,
		Message:   res.AIGreeting,
		CreatedAt: s.now(),
	})

	// Dispatch push notification if token exists and dispatcher is wired
	if s.dispatcher != nil && s.userLoader != nil {
		if token, found := s.userLoader.GetFCMToken(job.UserID); found && token != "" {
			_ = s.dispatcher.Send(token, notification.Message{
				Title: "AI Focus Coach",
				Body:  res.AIGreeting,
			})
		}
	}
}

// PendingNudges returns the user's queued nudges. Nudges persist for 2 minutes
// from their creation time to survive page navigation and refreshes.
func (s *Service) PendingNudges(userID string) []Nudge {
	return s.store.drain(userID, s.now())
}

func (s *Service) nextID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, atomic.AddInt64(&s.seq, 1))
}

// --- mood-int -> AI label mapping ------------------------------------------

// levelLabel maps a 1-5 energy/stress score to the low/moderate/high labels the
// Python service expects.
func levelLabel(n int) string {
	switch {
	case n <= 2:
		return "low"
	case n == 3:
		return "moderate"
	default:
		return "high"
	}
}

// burnoutLabel maps a 0-100 burnout risk to low/moderate/high.
func burnoutLabel(n int) string {
	switch {
	case n < 34:
		return "low"
	case n < 67:
		return "moderate"
	default:
		return "high"
	}
}

// --- nudge store -----------------------------------------------------------

type nudgeStore struct {
	mu     sync.Mutex
	byUser map[string][]Nudge
}

func newNudgeStore() *nudgeStore {
	return &nudgeStore{byUser: make(map[string][]Nudge)}
}

func (st *nudgeStore) add(userID string, n Nudge) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.byUser[userID] = append(st.byUser[userID], n)
}

func (st *nudgeStore) drain(userID string, now time.Time) []Nudge {
	st.mu.Lock()
	defer st.mu.Unlock()
	all := st.byUser[userID]

	var active []Nudge
	for _, n := range all {
		if now.Sub(n.CreatedAt) < 2*time.Minute {
			active = append(active, n)
		}
	}

	if len(active) > 0 {
		st.byUser[userID] = active
	} else {
		delete(st.byUser, userID)
	}

	if active == nil {
		active = []Nudge{}
	}
	return active
}
