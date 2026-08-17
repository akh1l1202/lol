package habit

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"
)

//go:embed schema.sql
var schemaSQL string

// Kind values for a habit.
const (
	KindDaily  = "daily"
	KindWeekly = "weekly"
)

// Habit is a tracked habit belonging to a user.
type Habit struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Target    int       `json:"target"`
	Color     string    `json:"color"`
	SortOrder int       `json:"sort_order"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
}

// Log is a single day's completion count for a habit.
type Log struct {
	HabitID  int64
	LoggedOn time.Time
	Count    int
}

// Repository is the storage abstraction for habits and their logs.
type Repository interface {
	ListHabits(userID string) ([]Habit, error)
	CreateHabit(h Habit) (Habit, error)
	UpdateHabit(userID string, id int64, name *string, target *int, color *string, archived *bool) (Habit, error)
	DeleteHabit(userID string, id int64) error
	ListLogs(userID string, from, to time.Time) ([]Log, error)
	// ApplyLog adjusts the count for (habit, day) by delta, clamped at >= 0,
	// and returns the new count. Errors with sql.ErrNoRows if the habit is not
	// owned by the user.
	ApplyLog(userID string, habitID int64, day time.Time, delta int) (int, error)
}

// Migrate applies the embedded habit schema (idempotent). Must run after the
// auth schema (habits references users).
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("habit schema: %w", err)
	}
	return nil
}

// PostgresRepository is a Postgres-backed habit Repository.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a repository backed by the given DB.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// ListHabits returns a user's habits in display order.
func (r *PostgresRepository) ListHabits(userID string) ([]Habit, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, name, kind, target, color, sort_order, archived, created_at
		   FROM habits
		  WHERE user_id = $1
		  ORDER BY sort_order, id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Habit, 0)
	for rows.Next() {
		var h Habit
		if err := rows.Scan(
			&h.ID, &h.UserID, &h.Name, &h.Kind, &h.Target, &h.Color, &h.SortOrder, &h.Archived, &h.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CreateHabit inserts a habit and returns it with the generated id/created_at.
func (r *PostgresRepository) CreateHabit(h Habit) (Habit, error) {
	if h.Kind == "" {
		h.Kind = KindDaily
	}
	if h.Target < 1 {
		h.Target = 1
	}
	err := r.db.QueryRow(
		`INSERT INTO habits (user_id, name, kind, target, color, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		h.UserID, h.Name, h.Kind, h.Target, h.Color, h.SortOrder,
	).Scan(&h.ID, &h.CreatedAt)
	return h, err
}

// UpdateHabit applies the non-nil fields and returns the updated habit.
func (r *PostgresRepository) UpdateHabit(userID string, id int64, name *string, target *int, color *string, archived *bool) (Habit, error) {
	var h Habit
	err := r.db.QueryRow(
		`UPDATE habits SET
		    name     = COALESCE($3, name),
		    target   = COALESCE($4, target),
		    color    = COALESCE($5, color),
		    archived = COALESCE($6, archived)
		  WHERE id = $1 AND user_id = $2
		  RETURNING id, user_id, name, kind, target, color, sort_order, archived, created_at`,
		id, userID, name, target, color, archived,
	).Scan(
		&h.ID, &h.UserID, &h.Name, &h.Kind, &h.Target, &h.Color, &h.SortOrder, &h.Archived, &h.CreatedAt,
	)
	return h, err
}

// DeleteHabit permanently deletes a habit. Related logs are removed by the
// database's ON DELETE CASCADE foreign key.
func (r *PostgresRepository) DeleteHabit(userID string, id int64) error {
	res, err := r.db.Exec(
		`DELETE FROM habits WHERE id = $1 AND user_id = $2`,
		id, userID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListLogs returns the user's logs in [from, to).
func (r *PostgresRepository) ListLogs(userID string, from, to time.Time) ([]Log, error) {
	rows, err := r.db.Query(
		`SELECT habit_id, logged_on, count
		   FROM habit_logs
		  WHERE user_id = $1 AND logged_on >= $2 AND logged_on < $3`,
		userID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Log, 0)
	for rows.Next() {
		var l Log
		if err := rows.Scan(&l.HabitID, &l.LoggedOn, &l.Count); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ApplyLog upserts the (habit, day) count by delta, clamped at >= 0. The insert
// is gated on habit ownership, so logging a habit the user does not own returns
// sql.ErrNoRows.
func (r *PostgresRepository) ApplyLog(userID string, habitID int64, day time.Time, delta int) (int, error) {
	var count int
	err := r.db.QueryRow(
		`INSERT INTO habit_logs (user_id, habit_id, logged_on, count)
		 SELECT $1, h.id, $3, GREATEST(0, $4)
		   FROM habits h
		  WHERE h.id = $2 AND h.user_id = $1
		 ON CONFLICT (habit_id, logged_on)
		 DO UPDATE SET count = GREATEST(0, habit_logs.count + $4)
		 RETURNING count`,
		userID, habitID, day, delta,
	).Scan(&count)
	return count, err
}
