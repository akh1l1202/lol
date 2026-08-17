// Command seed populates the database with demo data for three accounts so the
// app can be demoed across distinct states:
//
//   - demo_admin     "improving journey": focus climbs over ~6 weeks to a strong
//                    present (streak 9), habit rings filling in, upbeat moods.
//   - struggle_admin "needs coaching": heavy distraction, low scores, no streak,
//                    high-stress moods — sets up the AI coach / nudges.
//   - real_admin     clean new-user: starter habits, no history.
//
// It is idempotent: each account's rows are wiped and regenerated on every run.
//
// The data is stored persistently in the database (e.g. Supabase). Because every
// row carries a fixed calendar date, the curated history drifts into the past as
// real days pass. Rather than regenerate, `--freshen` shifts each demo account's
// stored timestamps forward so the newest day is "today" again — same data, just
// re-anchored. dev-up runs `--freshen` on every boot; full `--seed` rebuilds.
//
//	cd backend && go run ./cmd/seed            # full rebuild
//	cd backend && go run ./cmd/seed --freshen  # re-anchor existing data to today
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/auth"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/habit"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/mood"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/session"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/usage"
	"github.com/joho/godotenv"
)

// Demo credentials — these match the auth autofill profiles in the app.
const (
	demoUser  = "demo_admin"
	demoPass  = "Demo@2026"
	strugUser = "struggle_admin"
	strugPass = "Struggle@2026"
	realUser  = "real_admin"
	realPass  = "Real@2026"

	seedDays  = 45 // how far back to generate history
	goalScore = 60 // mirrors score.goalScore; a day at/above this extends a streak
)

// Apps the score service does NOT treat as distracting -> focus time.
var focusApps = []string{
	"com.android.chrome",
	"com.google.android.gm",
	"com.google.android.apps.docs.editors.docs",
	"com.Slack",
	"com.microsoft.office.word",
	"com.google.android.keep",
}

// Apps the score service treats as distracting (subset of score.distractingApps).
var distractApps = []string{
	"com.instagram.android",
	"com.google.android.youtube",
	"com.zhiliaoapp.musically",
	"com.reddit.frontpage",
}

var positiveReflections = []string{
	"Shipped the focus screen, feeling good.",
	"Deep work morning, light afternoon.",
	"Great flow today.",
	"Stayed off socials and it paid off.",
	"Best focus day in a while.",
}

var struggleReflections = []string{
	"Couldn't stop checking my phone today.",
	"Doomscrolled most of the evening.",
	"Meant to work, lost the afternoon to Reels.",
	"Feeling burnt out and behind.",
	"Hard to concentrate, kept getting pulled away.",
}

var taskNames = []string{
	"Writing design specs",
	"Backend integration",
	"Study session",
	"Reading docs",
	"Bug triage",
	"Planning sprint",
}

// tables touched by the seed, in wipe order. Reused by freshen.
var demoTables = []string{"usage_events", "mood_checkins", "focus_sessions", "habit_logs"}

func main() {
	freshenOnly := flag.Bool("freshen", false, "re-anchor existing demo data to today instead of regenerating")
	flag.Parse()

	_ = godotenv.Load()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set — point backend/.env at your database first")
	}

	db, err := auth.OpenDB(dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	if *freshenOnly {
		runFreshen(db)
		return
	}

	for _, migrate := range []func(*sql.DB) error{auth.Migrate, usage.Migrate, mood.Migrate, session.Migrate, habit.Migrate} {
		if err := migrate(db); err != nil {
			log.Fatalf("migrate: %v", err)
		}
	}

	// Ensure all accounts exist with known passwords.
	demoID, err := ensureUser(db, demoUser, demoPass)
	if err != nil {
		log.Fatalf("ensure demo user: %v", err)
	}
	strugID, err := ensureUser(db, strugUser, strugPass)
	if err != nil {
		log.Fatalf("ensure struggle user: %v", err)
	}
	realID, err := ensureUser(db, realUser, realPass)
	if err != nil {
		log.Fatalf("ensure real user: %v", err)
	}

	// Real Admin behaves like a brand-new user: no logs, but the starter habits
	// exist so the Habit Tracker is usable out of the box.
	wipe(db, realID)
	if _, err := ensureHabits(db, realID); err != nil {
		log.Fatalf("ensure real habits: %v", err)
	}

	// Dummy Admin: an improving journey that ends strong today.
	wipe(db, demoID)
	rng := rand.New(rand.NewSource(42))
	demoUsage, demoTotalS, demoDistractS := seedUsageArc(db, demoID, rng, improvingFocus, map[int]bool{9: true, 25: true, 38: true}, 150, 230)
	demoMoods := seedMoods(db, demoID, rng, improvingMood)
	demoSessions := seedSessions(db, demoID, rng, 40)
	demoHabits, err := ensureHabits(db, demoID)
	if err != nil {
		log.Fatalf("ensure demo habits: %v", err)
	}
	demoLogs := seedHabitLogs(db, demoID, demoHabits, rng, improvingHabit, true)

	// Struggle Admin: heavy distraction, low scores, no streak.
	wipe(db, strugID)
	srng := rand.New(rand.NewSource(7))
	strugUsage, strugTotalS, strugDistractS := seedUsageArc(db, strugID, srng, struggleFocus, nil, 220, 330)
	strugMoods := seedMoods(db, strugID, srng, struggleMoodFn)
	strugSessions := seedSessions(db, strugID, srng, 18)
	strugHabits, err := ensureHabits(db, strugID)
	if err != nil {
		log.Fatalf("ensure struggle habits: %v", err)
	}
	strugLogs := seedHabitLogs(db, strugID, strugHabits, srng, struggleHabit, false)

	fmt.Println("Seed complete.")
	printAccount("Dummy Admin   (improving journey)", demoUser, demoPass, demoID, demoUsage, demoMoods, demoSessions, demoLogs, demoTotalS, demoDistractS)
	printAccount("Struggle Admin(needs coaching)   ", strugUser, strugPass, strugID, strugUsage, strugMoods, strugSessions, strugLogs, strugTotalS, strugDistractS)
	fmt.Printf("  Real Admin    (clean new-user)    %s / %s  (id %s)  — starter habits, no history\n", realUser, realPass, realID)
}

func printAccount(label, user, pass, id string, usage, moods, sessions, logs, totalS, distractS int) {
	score := 0
	if totalS > 0 {
		score = int(float64(totalS-distractS)/float64(totalS)*100 + 0.5)
	}
	fmt.Printf("  %s %s / %s  (id %s)\n", label, user, pass, id)
	fmt.Printf("    usage %d  moods %d  sessions %d  habit logs %d  ~avg score %d  (%dh over %d days)\n",
		usage, moods, sessions, logs, score, totalS/3600, seedDays)
}

// --- persona shape functions (day index i: 0 = today, seedDays-1 = oldest) ---

// recency maps day index to 0 (oldest) .. 1 (today).
func recency(i int) float64 { return float64(seedDays-1-i) / float64(seedDays-1) }

// improvingFocus climbs from ~0.52 (six weeks ago) to ~0.90 (today).
func improvingFocus(i int) float64 { return 0.52 + 0.38*recency(i) }

// struggleFocus stays low and dips toward the present, so the account looks like
// it needs help right now — never meeting the streak goal.
func struggleFocus(i int) float64 { return 0.40 - 0.06*recency(i) }

// --- generic generators ---

// seedUsageArc generates per-day usage where each day hits a target focus
// fraction (focusFn). offDays force a dip below the streak goal regardless of the
// trend. Focus minutes land on focus apps in the morning; distraction in the
// evening. Returns event count and total / distracting seconds.
func seedUsageArc(db *sql.DB, userID string, rng *rand.Rand, focusFn func(int) float64, offDays map[int]bool, minMin, maxMin int) (count, totalS, distractS int) {
	repo := usage.NewPostgresRepository(db)

	var events []usage.Event
	add := func(day time.Time, hour, minutes int, app string, distracting bool) {
		if minutes <= 0 {
			return
		}
		dur := minutes * 60
		events = append(events, usage.Event{
			AppPackage: app,
			StartedAt:  day.Add(time.Duration(hour) * time.Hour),
			DurationS:  dur,
		})
		totalS += dur
		if distracting {
			distractS += dur
		}
	}

	for i := 0; i < seedDays; i++ {
		day := dayStart(i)
		total := randInt(rng, minMin, maxMin)
		ff := focusFn(i)
		if offDays[i] {
			ff = math.Min(ff, 0.35) // guaranteed below-goal day
		}
		ff += (rng.Float64() - 0.5) * 0.08 // small day-to-day noise
		ff = math.Max(0.1, math.Min(0.97, ff))

		distractMin := int(float64(total)*(1-ff) + 0.5)
		focusMin := total - distractMin

		// Spread focus across two morning blocks.
		add(day, 9, focusMin*6/10, focusApps[rng.Intn(3)], false)
		add(day, 13, focusMin-focusMin*6/10, focusApps[rng.Intn(len(focusApps))], false)
		// Spread distraction across two evening blocks.
		add(day, 17, distractMin*6/10, distractApps[rng.Intn(len(distractApps))], true)
		add(day, 20, distractMin-distractMin*6/10, distractApps[rng.Intn(len(distractApps))], true)
	}

	if err := repo.Insert(userID, events); err != nil {
		log.Fatalf("insert usage: %v", err)
	}
	return len(events), totalS, distractS
}

// moodPoint is the energy/stress/burnout for a given day index.
type moodPoint struct {
	energy, stress, burnout int
	reflection              string
}

func improvingMood(i int, rng *rand.Rand) moodPoint {
	r := recency(i)
	return moodPoint{
		energy:     clamp(int(2.5+2*r+rng.Float64()), 1, 5),
		stress:     clamp(int(4-2*r+rng.Float64()), 1, 5),
		burnout:    clamp(int(55-35*r)+randInt(rng, -8, 8), 5, 90),
		reflection: positiveReflections[rng.Intn(len(positiveReflections))],
	}
}

func struggleMoodFn(i int, rng *rand.Rand) moodPoint {
	return moodPoint{
		energy:     randInt(rng, 1, 3),
		stress:     randInt(rng, 3, 5),
		burnout:    randInt(rng, 55, 88),
		reflection: struggleReflections[rng.Intn(len(struggleReflections))],
	}
}

// seedMoods writes a check-in every few days using the persona's mood function.
func seedMoods(db *sql.DB, userID string, rng *rand.Rand, fn func(int, *rand.Rand) moodPoint) int {
	count := 0
	for i := 0; i < seedDays; i += 3 {
		mp := fn(i, rng)
		day := dayStart(i).Add(20 * time.Hour)
		_, err := db.Exec(
			`INSERT INTO mood_checkins (user_id, energy, stress, burnout_risk, reflection, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, mp.energy, mp.stress, mp.burnout, mp.reflection, day,
		)
		if err != nil {
			log.Fatalf("insert mood: %v", err)
		}
		count++
	}
	return count
}

// seedSessions records focus sessions on roughly pct% of days. Struggle personas
// abandon more of them ("ended" instead of "completed").
func seedSessions(db *sql.DB, userID string, rng *rand.Rand, pct int) int {
	count := 0
	for i := 0; i < seedDays; i++ {
		if rng.Intn(100) >= pct {
			continue
		}
		start := dayStart(i).Add(time.Duration(randInt(rng, 9, 18)) * time.Hour)
		end := start.Add(time.Duration(randInt(rng, 25, 50)) * time.Minute)
		status := "completed"
		if rng.Intn(100) < 20 {
			status = "ended"
		}
		_, err := db.Exec(
			`INSERT INTO focus_sessions (user_id, task, started_at, ended_at, status, pomodoro_index)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, taskNames[rng.Intn(len(taskNames))], start, end, status, randInt(rng, 0, 3),
		)
		if err != nil {
			log.Fatalf("insert session: %v", err)
		}
		count++
	}
	return count
}

// --- habit completion probability per persona (day index -> p of logging) ---

func improvingHabit(i int) float64 { return 0.45 + 0.45*recency(i) } // ramps up recently
func struggleHabit(i int) float64  { return 0.18 + 0.12*recency(i) } // mostly empty

// starterHabit is a default habit seeded for every account. Colors match the
// app's Monthly Overview ring palette.
type starterHabit struct {
	name   string
	kind   string
	target int
	color  string
	order  int
}

var starterHabits = []starterHabit{
	{"Morning Meditation", habit.KindDaily, 1, "#86A889", 0},
	{"Deep Work Block", habit.KindDaily, 1, "#5A7EAA", 1},
	{"Digital Sunset", habit.KindDaily, 1, "#C78D86", 2},
	{"Movement", habit.KindDaily, 1, "#9FB48D", 3},
	{"Reading", habit.KindWeekly, 3, "#5A7EAA", 4},
	{"Nature Walk", habit.KindWeekly, 1, "#9FB48D", 5},
}

// ensureHabits upserts the starter habits for a user and returns their ids by
// name. Idempotent: re-running keeps the same rows (and un-archives them).
func ensureHabits(db *sql.DB, userID string) (map[string]int64, error) {
	ids := make(map[string]int64, len(starterHabits))
	for _, sh := range starterHabits {
		var id int64
		err := db.QueryRow(
			`INSERT INTO habits (user_id, name, kind, target, color, sort_order)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (user_id, name) DO UPDATE SET
			    kind = EXCLUDED.kind,
			    target = EXCLUDED.target,
			    color = EXCLUDED.color,
			    sort_order = EXCLUDED.sort_order,
			    archived = FALSE
			 RETURNING id`,
			userID, sh.name, sh.kind, sh.target, sh.color, sh.order,
		).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids[sh.name] = id
	}
	return ids, nil
}

// seedHabitLogs fills the current month using a per-day probability from probFn,
// so an improving persona's rings fill in toward today while a struggling one
// stays sparse. When forceToday is set, the first two dailies are logged today
// for a guaranteed live-demo tick.
func seedHabitLogs(db *sql.DB, userID string, ids map[string]int64, rng *rand.Rand, probFn func(int) float64, forceToday bool) int {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	count := 0
	logOne := func(habitID int64, day time.Time) {
		_, err := db.Exec(
			`INSERT INTO habit_logs (user_id, habit_id, logged_on, count)
			 VALUES ($1, $2, $3, 1)
			 ON CONFLICT (habit_id, logged_on) DO UPDATE SET count = 1`,
			userID, habitID, day,
		)
		if err != nil {
			log.Fatalf("insert habit log: %v", err)
		}
		count++
	}

	for _, sh := range starterHabits {
		id := ids[sh.name]
		for d := monthStart; !d.After(today); d = d.AddDate(0, 0, 1) {
			dayIdx := int(today.Sub(d).Hours() / 24)
			p := probFn(dayIdx)
			if sh.kind == habit.KindWeekly {
				p *= 0.5 // weekly goals land less often
			}
			if rng.Float64() < p {
				logOne(id, d)
			}
		}
	}

	if forceToday {
		logOne(ids["Morning Meditation"], today)
		logOne(ids["Deep Work Block"], today)
	}
	return count
}

// --- freshen: re-anchor stored demo data to today without regenerating ---

func runFreshen(db *sql.DB) {
	for _, user := range []string{demoUser, strugUser} {
		shifted, days := freshen(db, user)
		if days <= 0 {
			fmt.Printf("freshen %s: already current (or no data)\n", user)
			continue
		}
		fmt.Printf("freshen %s: shifted forward %d day(s) across %d rows\n", user, days, shifted)
	}
}

// freshen shifts every timestamp for userID forward by the whole-day gap between
// the account's newest usage event and today, so the curated history ends today.
func freshen(db *sql.DB, userID string) (rows, days int) {
	var newest sql.NullTime
	if err := db.QueryRow(
		`SELECT max(started_at) FROM usage_events WHERE user_id = $1`, userID,
	).Scan(&newest); err != nil {
		log.Fatalf("freshen read %s: %v", userID, err)
	}
	if !newest.Valid {
		return 0, 0
	}
	now := time.Now().UTC()
	newestDay := time.Date(newest.Time.Year(), newest.Time.Month(), newest.Time.Day(), 0, 0, 0, 0, time.UTC)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days = int(today.Sub(newestDay).Hours() / 24)
	if days <= 0 {
		return 0, 0
	}

	interval := fmt.Sprintf("%d days", days)

	// Timestamp columns have no dense unique-date constraint, so an in-place
	// shift is safe.
	shift := []struct{ table, col string }{
		{"usage_events", "started_at"},
		{"mood_checkins", "created_at"},
		{"focus_sessions", "started_at"},
		{"focus_sessions", "ended_at"},
	}
	for _, s := range shift {
		res, err := db.Exec(
			fmt.Sprintf("UPDATE %s SET %s = %s + $1::interval WHERE user_id = $2 AND %s IS NOT NULL",
				s.table, s.col, s.col, s.col),
			interval, userID,
		)
		if err != nil {
			log.Fatalf("freshen %s.%s: %v", s.table, s.col, err)
		}
		n, _ := res.RowsAffected()
		rows += int(n)
	}

	rows += shiftHabitLogs(db, userID, interval)
	return rows, days
}

// shiftHabitLogs moves a user's habit_logs forward by interval. habit_logs has a
// UNIQUE(habit_id, logged_on) over dense daily rows, so an in-place +N update
// collides mid-shift, and a single delete-and-reinsert CTE doesn't help (the
// statements in a data-modifying CTE share one snapshot, so the INSERT still
// sees the pre-delete index). We stash the rows in a temp table, delete, then
// reinsert shifted — sequential statements in one transaction, so the DELETE is
// visible to the INSERT and uniqueness holds.
func shiftHabitLogs(db *sql.DB, userID, interval string) int {
	tx, err := db.Begin()
	if err != nil {
		log.Fatalf("freshen habit_logs begin: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`CREATE TEMP TABLE tmp_habit_logs ON COMMIT DROP AS
		 SELECT habit_id, logged_on, count FROM habit_logs WHERE user_id = $1`, userID,
	); err != nil {
		log.Fatalf("freshen habit_logs stash: %v", err)
	}
	if _, err := tx.Exec(`DELETE FROM habit_logs WHERE user_id = $1`, userID); err != nil {
		log.Fatalf("freshen habit_logs delete: %v", err)
	}
	res, err := tx.Exec(
		`INSERT INTO habit_logs (user_id, habit_id, logged_on, count)
		 SELECT $1, habit_id, logged_on + $2::interval, count FROM tmp_habit_logs`,
		userID, interval,
	)
	if err != nil {
		log.Fatalf("freshen habit_logs reinsert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("freshen habit_logs commit: %v", err)
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// --- shared helpers ---

// ensureUser upserts a user with a known password and returns its id.
func ensureUser(db *sql.DB, username, password string) (string, error) {
	u := auth.User{Username: username}
	if err := u.SetPassword(password); err != nil {
		return "", err
	}
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3)
		 ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
		username, username, u.PasswordHash,
	); err != nil {
		return "", err
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM users WHERE username = $1`, username).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

func wipe(db *sql.DB, userID string) {
	for _, table := range demoTables {
		if _, err := db.Exec("DELETE FROM "+table+" WHERE user_id = $1", userID); err != nil {
			log.Fatalf("wipe %s: %v", table, err)
		}
	}
}

// dayStart returns UTC midnight for the date i days before today.
func dayStart(i int) time.Time {
	now := time.Now().UTC()
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return base.AddDate(0, 0, -i)
}

func randInt(rng *rand.Rand, min, max int) int { return min + rng.Intn(max-min+1) }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
