package usage

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"
)

//go:embed schema.sql
var schemaSQL string

// Event is a single app-usage record scoped to a user.
type Event struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id"`
	AppPackage string    `json:"app_package"`
	StartedAt  time.Time `json:"started_at"`
	DurationS  int       `json:"duration_s"`
	CreatedAt  time.Time `json:"created_at"`
}

// Repository is the storage abstraction for usage events.
type Repository interface {
	Insert(userID string, events []Event) error
	ListByUser(userID string, from, to time.Time, limit int) ([]Event, error)
}

// Migrate applies the embedded usage schema. Idempotent; safe on every startup.
// Must run after the auth schema (usage_events references users).
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("usage schema: %w", err)
	}
	return nil
}

// PostgresRepository is a Postgres-backed usage Repository.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository returns a repository backed by the given DB.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Insert stores a batch of events for the given user in a single transaction.
func (r *PostgresRepository) Insert(userID string, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}

	// Chunk inserts to avoid exceeding database placeholder limits and to optimize RTT.
	const chunkSize = 1000
	for i := 0; i < len(events); i += chunkSize {
		end := i + chunkSize
		if end > len(events) {
			end = len(events)
		}
		chunk := events[i:end]

		query := "INSERT INTO usage_events (user_id, app_package, started_at, duration_s) VALUES "
		args := make([]interface{}, 0, len(chunk)*4)

		for j, e := range chunk {
			if j > 0 {
				query += ", "
			}
			query += fmt.Sprintf("($%d, $%d, $%d, $%d)", j*4+1, j*4+2, j*4+3, j*4+4)
			args = append(args, userID, e.AppPackage, e.StartedAt, e.DurationS)
		}

		if _, err := tx.Exec(query, args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// ListByUser returns a user's events within [from, to), newest first.
func (r *PostgresRepository) ListByUser(userID string, from, to time.Time, limit int) ([]Event, error) {
	rows, err := r.db.Query(
		`SELECT id, user_id, app_package, started_at, duration_s, created_at
		   FROM usage_events
		  WHERE user_id = $1 AND started_at >= $2 AND started_at < $3
		  ORDER BY started_at DESC
		  LIMIT $4`,
		userID, from, to, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var e Event
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.AppPackage, &e.StartedAt, &e.DurationS, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		events = append(events, e)
	}

	return events, rows.Err()
}
