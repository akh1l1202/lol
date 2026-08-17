package mood

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"
)

//go:embed schema.sql
var schemaSQL string

// Checkin is a single mood check-in.
type Checkin struct {
	ID          int64     `json:"id"`
	UserID      string    `json:"user_id"`
	Energy      int       `json:"energy"`
	Stress      int       `json:"stress"`
	BurnoutRisk int       `json:"burnout_risk"`
	Reflection  string    `json:"reflection"`
	CreatedAt   time.Time `json:"created_at"`
}

// Repository is the storage abstraction for mood check-ins.
type Repository interface {
	Create(c Checkin) (Checkin, error)
	ListByUser(userID string, limit int) ([]Checkin, error)
}

// Migrate applies the embedded mood schema (idempotent).
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("mood schema: %w", err)
	}
	return nil
}

// PostgresRepository is a Postgres-backed mood Repository.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a repository backed by the given DB.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create stores a check-in and returns it with the generated id/created_at.
func (r *PostgresRepository) Create(c Checkin) (Checkin, error) {
	err := r.db.QueryRow(
		`INSERT INTO mood_checkins (user_id, energy, stress, burnout_risk, reflection)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at`,
		c.UserID, c.Energy, c.Stress, c.BurnoutRisk, c.Reflection,
	).Scan(&c.ID, &c.CreatedAt)
	return c, err
}

// ListByUser returns a user's check-ins, newest first.
func (r *PostgresRepository) ListByUser(userID string, limit int) ([]Checkin, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, energy, stress, burnout_risk, reflection, created_at
		   FROM mood_checkins
		  WHERE user_id = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Checkin, 0)
	for rows.Next() {
		var c Checkin
		if err := rows.Scan(
			&c.ID, &c.UserID, &c.Energy, &c.Stress, &c.BurnoutRisk, &c.Reflection, &c.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
