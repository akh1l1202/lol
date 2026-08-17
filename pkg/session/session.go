package session

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"
)

//go:embed schema.sql
var schemaSQL string

// Session is a focus / Pomodoro session.
type Session struct {
	ID            int64      `json:"id"`
	UserID        string     `json:"user_id"`
	Task          string     `json:"task"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	Status        string     `json:"status"`
	PomodoroIndex int        `json:"pomodoro_index"`
	CreatedAt     time.Time  `json:"created_at"`
}

// SessionUpdate carries the patchable fields of a focus session. Nil fields are
// left unchanged, so a caller can end a running session by sending only
// ended_at + status without disturbing the rest of the row.
type SessionUpdate struct {
	Task          *string
	EndedAt       *time.Time
	Status        *string
	PomodoroIndex *int
}

// Repository is the storage abstraction for focus sessions.
type Repository interface {
	Create(s Session) (Session, error)
	ListByUser(userID string, limit int) ([]Session, error)
	Update(userID string, id int64, u SessionUpdate) (Session, error)
}

// Migrate applies the embedded session schema (idempotent).
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("session schema: %w", err)
	}
	return nil
}

// PostgresRepository is a Postgres-backed session Repository.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a repository backed by the given DB.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create stores a session and returns it with the generated id/created_at.
func (r *PostgresRepository) Create(s Session) (Session, error) {
	if s.Status == "" {
		s.Status = "completed"
	}
	err := r.db.QueryRow(
		`INSERT INTO focus_sessions (user_id, task, started_at, ended_at, status, pomodoro_index)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		s.UserID, s.Task, s.StartedAt, s.EndedAt, s.Status, s.PomodoroIndex,
	).Scan(&s.ID, &s.CreatedAt)
	return s, err
}

// ListByUser returns a user's sessions, newest first.
func (r *PostgresRepository) ListByUser(userID string, limit int) ([]Session, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, task, started_at, ended_at, status, pomodoro_index, created_at
		   FROM focus_sessions
		  WHERE user_id = $1
		  ORDER BY started_at DESC
		  LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Session, 0)
	for rows.Next() {
		var (
			s     Session
			ended sql.NullTime
		)
		if err := rows.Scan(
			&s.ID, &s.UserID, &s.Task, &s.StartedAt, &ended, &s.Status, &s.PomodoroIndex, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		if ended.Valid {
			s.EndedAt = &ended.Time
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Update applies a partial change to one of the user's sessions and returns the
// updated row. It returns sql.ErrNoRows if no session with that id belongs to
// the user. COALESCE leaves any field nil in the update untouched.
func (r *PostgresRepository) Update(userID string, id int64, u SessionUpdate) (Session, error) {
	var (
		s     Session
		ended sql.NullTime
	)
	err := r.db.QueryRow(
		`UPDATE focus_sessions SET
		    task           = COALESCE($3, task),
		    ended_at       = COALESCE($4, ended_at),
		    status         = COALESCE($5, status),
		    pomodoro_index = COALESCE($6, pomodoro_index)
		  WHERE id = $1 AND user_id = $2
		  RETURNING id, user_id, task, started_at, ended_at, status, pomodoro_index, created_at`,
		id, userID, u.Task, u.EndedAt, u.Status, u.PomodoroIndex,
	).Scan(
		&s.ID, &s.UserID, &s.Task, &s.StartedAt, &ended, &s.Status, &s.PomodoroIndex, &s.CreatedAt,
	)
	if ended.Valid {
		s.EndedAt = &ended.Time
	}
	return s, err
}
