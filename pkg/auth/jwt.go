package auth

import (
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	jwtKeyOnce sync.Once
	jwtKey     []byte
)

// jwtSecret returns the signing key, read once from JWT_SECRET. Reading it
// lazily (instead of at package init) lets main load a .env file first.
func jwtSecret() []byte {
	jwtKeyOnce.Do(func() {
		secret := os.Getenv("JWT_SECRET")
		if secret == "" {
			log.Println("warning: JWT_SECRET not set — using an insecure dev default")
			secret = "nudge-dev-insecure-change-me"
		}
		jwtKey = []byte(secret)
	})
	return jwtKey
}

// Token types, carried in the "typ" claim so an access token can't be used
// where a refresh token is expected and vice versa. An empty type is treated
// as an access token for backward compatibility with tokens issued before
// refresh support existed.
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"

	accessTokenTTL  = 24 * time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
)

// Claims defines custom claims for JWT tokens
type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Type     string `json:"typ,omitempty"`
	jwt.RegisteredClaims
}

// GenerateToken generates a new short-lived access token for a user.
func GenerateToken(userID string, username string) (string, error) {
	return signToken(userID, username, TokenTypeAccess, accessTokenTTL)
}

// GenerateRefreshToken generates a long-lived refresh token, exchanged at
// POST /auth/refresh for a fresh access token.
func GenerateRefreshToken(userID string, username string) (string, error) {
	return signToken(userID, username, TokenTypeRefresh, refreshTokenTTL)
}

func signToken(userID, username, typ string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID:   userID,
		Username: username,
		Type:     typ,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret())
}

// ValidateToken parses and validates a JWT token
func ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return jwtSecret(), nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}
