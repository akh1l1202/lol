package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// AuthHandler holds dependencies for auth endpoints
type AuthHandler struct {
	Repo UserRepository
}

// NewAuthHandler creates a new handler instance
func NewAuthHandler(repo UserRepository) *AuthHandler {
	return &AuthHandler{
		Repo: repo,
	}
}

// RegisterInput defines inputs required for registration
type RegisterInput struct {
	Name     string `json:"name" binding:"required"`
	Username string `json:"username" binding:"required,min=3"`
	Password string `json:"password" binding:"required,min=8"`
}

// LoginInput defines inputs required for login
type LoginInput struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// RefreshInput is the body of POST /auth/refresh.
type RefreshInput struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type ChangePasswordInput struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

// friendlyError converts go-playground/validator errors to human-readable
// messages so the raw struct field paths never reach the client.
func friendlyError(err error) string {
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		fe := ve[0]
		switch fe.Field() {
		case "Name":
			return "Full Name is required"
		case "Username":
			switch fe.Tag() {
			case "required":
				return "Username is required"
			case "min":
				return "Username must be at least 3 characters"
			}
		case "Password":
			switch fe.Tag() {
			case "required":
				return "Password is required"
			case "min":
				return "Password must be at least 8 characters"
			}
		case "RefreshToken":
			return "Refresh token is required"
		}
	}
	return "Invalid request — please check your input"
}

// validatePassword enforces strength: at least one uppercase letter and one digit.
func validatePassword(pw string) error {
	var hasUpper, hasDigit bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasUpper {
		return errors.New("password must contain at least one uppercase letter")
	}
	if !hasDigit {
		return errors.New("password must contain at least one number")
	}
	return nil
}

// Register handles POST /auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var input RegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyError(err)})
		return
	}

	if err := validatePassword(input.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyError(err)})
		return
	}

	// Generate random ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate user ID"})
		return
	}
	userID := hex.EncodeToString(idBytes)

	user := User{
		ID:       userID,
		Username: input.Username,
		Name:     input.Name,
	}

	if err := user.SetPassword(input.Password); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	if err := h.Repo.Create(user); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "User registered successfully",
		"user_id": user.ID,
	})
}

// ChangePassword handles PUT /auth/password
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	// Require the user to be logged in (handled by middleware, username in context)
	username, exists := c.Get("username")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var input ChangePasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyError(err)})
		return
	}

	if err := validatePassword(input.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyError(err)})
		return
	}

	user, exists := h.Repo.GetByUsername(username.(string))
	if !exists || !user.CheckPassword(input.CurrentPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Incorrect current password"})
		return
	}

	if err := user.SetPassword(input.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash new password"})
		return
	}

	if err := h.Repo.UpdatePassword(user.Username, user.PasswordHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password updated successfully"})
}

// Login handles POST /auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var input LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyError(err)})
		return
	}

	user, exists := h.Repo.GetByUsername(input.Username)
	if !exists || !user.CheckPassword(input.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	token, err := GenerateToken(user.ID, user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	refresh, err := GenerateRefreshToken(user.ID, user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Login successful",
		"token":         token,
		"refresh_token": refresh,
	})
}

// Refresh handles POST /auth/refresh — exchanges a valid refresh token for a
// fresh access token (and a rotated refresh token), so a client can stay signed
// in without re-entering credentials once the access token expires.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var input RefreshInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyError(err)})
		return
	}

	claims, err := ValidateToken(input.RefreshToken)
	if err != nil || claims.Type != TokenTypeRefresh {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}

	token, err := GenerateToken(claims.UserID, claims.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	refresh, err := GenerateRefreshToken(claims.UserID, claims.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":         token,
		"refresh_token": refresh,
	})
}

type fcmTokenInput struct {
	Token string `json:"token" binding:"required"`
}

// UpdateFCMToken handles PUT /api/profile/fcm-token
func (h *AuthHandler) UpdateFCMToken(c *gin.Context) {
	userID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing user context"})
		return
	}

	var input fcmTokenInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Repo.UpdateFCMToken(userID.(string), input.Token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update FCM token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "FCM token updated successfully"})
}
