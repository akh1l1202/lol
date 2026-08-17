package auth

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)


//go:embed schema.sql
var schemaSQL string

// OpenDB opens a Postgres connection pool and verifies it with a ping.
func OpenDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)
	db.SetConnMaxIdleTime(15 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

// Migrate applies the embedded schema. It is idempotent (CREATE TABLE IF NOT
// EXISTS), so it is safe to run on every startup.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("run schema: %w", err)
	}
	return nil
}

// PostgresUserRepository is a Postgres-backed UserRepository.
type PostgresUserRepository struct {
	db *sql.DB
}

// NewPostgresUserRepository returns a repository backed by the given DB.
func NewPostgresUserRepository(db *sql.DB) *PostgresUserRepository {
	return &PostgresUserRepository{db: db}
}

// Create inserts a new user, returning an error if the username already exists.
func (r *PostgresUserRepository) Create(user User) error {
	_, err := r.db.Exec(
		`INSERT INTO users (id, username, name, password_hash) VALUES ($1, $2, $3, $4)`,
		user.ID, user.Username, user.Name, user.PasswordHash,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			// unique_violation on the username constraint
			return errors.New("user already exists")
		}
		return err
	}
	return nil
}

// GetByUsername returns the user with the given username, if present.
func (r *PostgresUserRepository) GetByUsername(username string) (User, bool) {
	var u User
	// Use sql.NullString for name so that old users whose name column is NULL
	// (created before the column was added) can still log in successfully.
	var nullName sql.NullString
	var nullFCM sql.NullString
	err := r.db.QueryRow(
		`SELECT id, username, name, password_hash, fcm_token FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &nullName, &u.PasswordHash, &nullFCM)
	if err != nil {
		return User{}, false
	}
	if nullName.Valid {
		u.Name = nullName.String
	} else {
		// Fallback: use username as display name for legacy accounts
		u.Name = u.Username
	}
	if nullFCM.Valid {
		u.FCMToken = nullFCM.String
	}
	return u, true
}

// GetByID retrieves a user by their user ID.
func (r *PostgresUserRepository) GetByID(id string) (User, bool) {
	var u User
	var nullName sql.NullString
	var nullFCM sql.NullString
	err := r.db.QueryRow(
		`SELECT id, username, name, password_hash, fcm_token FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.Username, &nullName, &u.PasswordHash, &nullFCM)
	if err != nil {
		return User{}, false
	}
	if nullName.Valid {
		u.Name = nullName.String
	} else {
		u.Name = u.Username
	}
	if nullFCM.Valid {
		u.FCMToken = nullFCM.String
	}
	return u, true
}

// UpdatePassword updates the password hash for a given username.
func (r *PostgresUserRepository) UpdatePassword(username, newPasswordHash string) error {
	result, err := r.db.Exec(
		`UPDATE users SET password_hash = $1 WHERE username = $2`,
		newPasswordHash, username,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("user not found")
	}
	return nil
}

// UpdateFCMToken updates the FCM token for a given user ID.
func (r *PostgresUserRepository) UpdateFCMToken(userID string, token string) error {
	_, err := r.db.Exec(`UPDATE users SET fcm_token = $1 WHERE id = $2`, token, userID)
	return err
}

// GetFCMToken retrieves the FCM token for a given user ID.
func (r *PostgresUserRepository) GetFCMToken(userID string) (string, bool) {
	var token sql.NullString
	err := r.db.QueryRow(`SELECT fcm_token FROM users WHERE id = $1`, userID).Scan(&token)
	if err != nil || !token.Valid {
		return "", false
	}
	return token.String, true
}
