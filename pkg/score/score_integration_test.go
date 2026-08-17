//go:build integration

package score_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/SwDC-kjsse/app-dev-1/backend/internal/testdb"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/auth"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/score"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/usage"
)

const (
	distractingApp = "com.instagram.android" // in score.distractingApps
	focusApp       = "com.android.chrome"    // not distracting
)

// Noon UTC keeps each event on the same calendar day regardless of the DB's
// session timezone.
var baseDay = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func dayOffset(days int) time.Time { return baseDay.AddDate(0, 0, days) }

func mustUser(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if err := auth.NewPostgresUserRepository(db).Create(
		auth.User{ID: id, Username: id, PasswordHash: "x"},
	); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

func seed(t *testing.T, db *sql.DB, userID string, events ...usage.Event) {
	t.Helper()
	if err := usage.NewPostgresRepository(db).Insert(userID, events); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

func ev(app string, at time.Time, durationS int) usage.Event {
	return usage.Event{AppPackage: app, StartedAt: at, DurationS: durationS}
}

func TestComputeFocusRatioAndTopApp(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()
	mustUser(t, db, "u1")
	seed(t, db, "u1",
		ev(distractingApp, baseDay, 3600), // 1h distracting
		ev(focusApp, baseDay, 5400),       // 1.5h focus
	)

	res, err := score.NewService(db).Compute("u1", baseDay)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if res.Score != 60 { // (9000-3600)/9000 = 60%
		t.Errorf("score = %d, want 60", res.Score)
	}
	if res.TotalUsageS != 9000 || res.DistractingS != 3600 {
		t.Errorf("totals = %d/%d, want 9000/3600", res.TotalUsageS, res.DistractingS)
	}
	if res.TopApp != focusApp {
		t.Errorf("top_app = %q, want %q", res.TopApp, focusApp)
	}
	if res.StreakDays != 1 {
		t.Errorf("streak = %d, want 1", res.StreakDays)
	}
}

func TestComputeStreakWalksConsecutiveGoalDays(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()
	mustUser(t, db, "u1")

	// Three consecutive all-focus days (score 100), then a distracting day that
	// falls below the goal and must break the streak.
	seed(t, db, "u1",
		ev(focusApp, dayOffset(0), 3600),
		ev(focusApp, dayOffset(-1), 3600),
		ev(focusApp, dayOffset(-2), 3600),
		ev(distractingApp, dayOffset(-3), 3600), // score 0 -> breaks
	)

	res, err := score.NewService(db).Compute("u1", baseDay)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if res.Score != 100 {
		t.Errorf("score = %d, want 100", res.Score)
	}
	if res.StreakDays != 3 {
		t.Errorf("streak = %d, want 3 (D, D-1, D-2)", res.StreakDays)
	}
}

func TestComputeEmptyDayIsZero(t *testing.T) {
	db := testdb.Open(t)
	defer db.Close()
	mustUser(t, db, "u1")

	res, err := score.NewService(db).Compute("u1", baseDay)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if res.Score != 0 || res.StreakDays != 0 || res.TotalUsageS != 0 {
		t.Errorf("empty day = %+v, want zeros", res)
	}
}
