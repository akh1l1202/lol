package auth

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// rateLimiter is a small in-memory fixed-window limiter keyed by client. It is
// process-local — good enough to blunt brute-force against the auth endpoints
// on a single instance; a Redis-backed limiter would be needed once the API
// runs multi-instance (see TASK-24).
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
	now    func() time.Time // injectable for tests
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		hits:   make(map[string][]time.Time),
		max:    max,
		window: window,
		now:    time.Now,
	}
}

// allow records a hit for key and reports whether it is within the limit. It
// prunes timestamps older than the window on each call, so the map self-cleans
// for active keys.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := rl.now().Add(-rl.window)
	recent := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= rl.max {
		rl.hits[key] = recent
		return false
	}

	rl.hits[key] = append(recent, rl.now())
	return true
}

// RateLimit returns middleware that allows at most max requests per window from
// a single client IP, replying 429 once the limit is exceeded.
func RateLimit(max int, window time.Duration) gin.HandlerFunc {
	rl := newRateLimiter(max, window)
	return func(c *gin.Context) {
		if !rl.allow(c.ClientIP()) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests, please slow down",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
