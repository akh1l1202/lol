package auth

import (
	"errors"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// User represents a user inside the application
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	FCMToken     string `json:"-"`
}

// SetPassword hashes and sets the user's password
func (u *User) SetPassword(password string) error {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(bytes)
	return nil
}

// CheckPassword verifies the user's password against the hash
func (u *User) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	return err == nil
}

// UserRepository is the storage abstraction used by the auth handlers so the
// backing store can be swapped (in-memory for tests, Postgres in production).
type UserRepository interface {
	Create(user User) error
	GetByUsername(username string) (User, bool)
	GetByID(id string) (User, bool)
	UpdatePassword(username, newPasswordHash string) error
	UpdateFCMToken(userID string, token string) error
	GetFCMToken(userID string) (string, bool)
}

// InMemoryUserRepository is a thread-safe in-memory store for users
type InMemoryUserRepository struct {
	mu    sync.RWMutex
	users map[string]User
}

// NewInMemoryUserRepository creates a new user repository instance
func NewInMemoryUserRepository() *InMemoryUserRepository {
	return &InMemoryUserRepository{
		users: make(map[string]User),
	}
}

// Create stores a new user in the repository
func (r *InMemoryUserRepository) Create(user User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.users[user.Username]; exists {
		return errors.New("user already exists")
	}

	r.users[user.Username] = user
	return nil
}

// GetByUsername retrieves a user by their username
func (r *InMemoryUserRepository) GetByUsername(username string) (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	user, exists := r.users[username]
	return user, exists
}

func (r *InMemoryUserRepository) UpdatePassword(username, newPasswordHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	user, exists := r.users[username]
	if !exists {
		return errors.New("user not found")
	}
	user.PasswordHash = newPasswordHash
	r.users[username] = user
	return nil
}

func (r *InMemoryUserRepository) GetByID(id string) (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.users {
		if u.ID == id {
			return u, true
		}
	}
	return User{}, false
}

func (r *InMemoryUserRepository) UpdateFCMToken(userID string, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for username, u := range r.users {
		if u.ID == userID {
			u.FCMToken = token
			r.users[username] = u
			return nil
		}
	}
	return errors.New("user not found")
}

func (r *InMemoryUserRepository) GetFCMToken(userID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.users {
		if u.ID == userID {
			return u.FCMToken, u.FCMToken != ""
		}
	}
	return "", false
}
