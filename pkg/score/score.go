package score

import (
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// distractingApps are treated as non-focus time when computing the score.
// v1 is a hardcoded set; this can later be user-configurable or driven by the
// AI classifier.
var distractingApps = []string{
	// Social Media Category
	"com.instagram.android",
	"com.zhiliaoapp.musically", // TikTok
	"com.facebook.katana",
	"com.twitter.android",
	"com.snapchat.android",
	"com.reddit.frontpage",

	// Video Streaming Category
	"com.google.android.youtube",
	"com.netflix.mediaclient",
	"com.disney.disneyplus",
	"com.hbo.hbonow",
	"com.amazon.avod.thirdpartyclient",
}

// goalScore is the daily threshold a day must meet to extend a streak.
const goalScore = 60

// Result is the computed score payload for a single day.
type Result struct {
	Date         string `json:"date"`
	Score        int    `json:"score"`
	TotalUsageS  int    `json:"total_usage_s"`
	DistractingS int    `json:"distracting_s"`
	TopApp       string `json:"top_app"`
	StreakDays   int    `json:"streak_days"`
}

// Service computes scores from stored usage events.
type Service struct {
	db *sql.DB
}

// NewService returns a score service backed by the given DB.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

type dayAgg struct {
	total       int
	distracting int
}

// dayScore is the share of screen time NOT spent on distracting apps, 0–100.
func dayScore(a dayAgg) int {
	if a.total <= 0 {
		return 0
	}
	focus := float64(a.total-a.distracting) / float64(a.total)
	s := int(focus*100 + 0.5)
	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	return s
}

// Compute returns the focus score for the given day plus the current streak
// (consecutive days up to and including that day meeting the goal).
func (s *Service) Compute(userID string, date time.Time) (Result, error) {
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	windowStart := day.AddDate(0, 0, -60)
	windowEnd := day.AddDate(0, 0, 1)

	rows, err := s.db.Query(
		`SELECT started_at::date AS d,
		        SUM(duration_s) AS total,
		        SUM(CASE WHEN app_package = ANY($2) THEN duration_s ELSE 0 END) AS distracting
		   FROM usage_events
		  WHERE user_id = $1 AND started_at >= $3 AND started_at < $4
		  GROUP BY d`,
		userID, pq.Array(distractingApps), windowStart, windowEnd,
	)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()

	byDay := make(map[string]dayAgg)
	for rows.Next() {
		var (
			d time.Time
			a dayAgg
		)
		if err := rows.Scan(&d, &a.total, &a.distracting); err != nil {
			return Result{}, err
		}
		byDay[d.Format("2006-01-02")] = a
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}

	key := day.Format("2006-01-02")
	today := byDay[key]

	// Top app for the target day (ignored error -> empty string if none).
	var topApp string
	_ = s.db.QueryRow(
		`SELECT app_package FROM usage_events
		  WHERE user_id = $1 AND started_at >= $2 AND started_at < $3
		  GROUP BY app_package ORDER BY SUM(duration_s) DESC LIMIT 1`,
		userID, day, day.AddDate(0, 0, 1),
	).Scan(&topApp)

	// Streak: walk back day-by-day while each day meets the goal.
	streak := 0
	for d := day; ; d = d.AddDate(0, 0, -1) {
		agg, ok := byDay[d.Format("2006-01-02")]
		if !ok || dayScore(agg) < goalScore {
			break
		}
		streak++
	}

	return Result{
		Date:         key,
		Score:        dayScore(today),
		TotalUsageS:  today.total,
		DistractingS: today.distracting,
		TopApp:       topApp,
		StreakDays:   streak,
	}, nil
}
