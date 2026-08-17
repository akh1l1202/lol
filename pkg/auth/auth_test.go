package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupAuthRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewAuthHandler(NewInMemoryUserRepository())
	r.POST("/auth/register", h.Register)
	r.POST("/auth/login", h.Login)
	r.POST("/auth/refresh", h.Refresh)
	return r
}

func postJSON(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestPasswordHashing(t *testing.T) {
	u := User{Username: "testuser"}
	err := u.SetPassword("supersecret")
	if err != nil {
		t.Fatalf("Expected no error when setting password, got %v", err)
	}

	if u.PasswordHash == "" {
		t.Fatal("Expected password hash to be populated")
	}

	if !u.CheckPassword("supersecret") {
		t.Fatal("Expected password check to pass for correct password")
	}

	if u.CheckPassword("wrongpassword") {
		t.Fatal("Expected password check to fail for incorrect password")
	}
}

func TestUserRepository(t *testing.T) {
	repo := NewInMemoryUserRepository()
	u := User{ID: "1", Username: "testuser"}
	_ = u.SetPassword("password123")

	err := repo.Create(u)
	if err != nil {
		t.Fatalf("Expected to create user successfully, got %v", err)
	}

	// Try registering duplicate user
	err = repo.Create(u)
	if err == nil {
		t.Fatal("Expected error when creating duplicate user, got nil")
	}

	// Get user
	fetchedUser, exists := repo.GetByUsername("testuser")
	if !exists {
		t.Fatal("Expected to retrieve user, got exists=false")
	}

	if fetchedUser.Username != "testuser" {
		t.Errorf("Expected username to be 'testuser', got %s", fetchedUser.Username)
	}
}

func TestJWTGenerationAndValidation(t *testing.T) {
	userID := "user123"
	username := "testuser"

	token, err := GenerateToken(userID, username)
	if err != nil {
		t.Fatalf("Expected no error when generating token, got %v", err)
	}

	if token == "" {
		t.Fatal("Expected token string to be non-empty")
	}

	claims, err := ValidateToken(token)
	if err != nil {
		t.Fatalf("Expected token validation to succeed, got %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("Expected claims UserID to be %s, got %s", userID, claims.UserID)
	}

	if claims.Username != username {
		t.Errorf("Expected claims Username to be %s, got %s", username, claims.Username)
	}
}

func TestAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"user_id": userID, "username": username})
	})

	// Test missing header
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/protected", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 Unauthorized for missing header, got %d", w.Code)
	}

	// Test invalid header format
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "InvalidTokenFormat")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 Unauthorized for invalid header format, got %d", w.Code)
	}

	// Test valid token
	token, _ := GenerateToken("123", "alice")
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 OK for valid token, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), `"user_id":"123"`) || !strings.Contains(w.Body.String(), `"username":"alice"`) {
		t.Errorf("Expected body to contain user_id and username, got %s", w.Body.String())
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		pw string
		ok bool
	}{
		{"Abc12345", true},
		{"LettersOnly", false}, // no digit
		{"12345678", false},     // no letter
	}
	for _, tc := range cases {
		err := validatePassword(tc.pw)
		if tc.ok && err != nil {
			t.Errorf("expected %q to pass, got %v", tc.pw, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("expected %q to fail validation", tc.pw)
		}
	}
}

func TestRegisterRejectsWeakPassword(t *testing.T) {
	r := setupAuthRouter()

	// Too short (binding min=8).
	if w := postJSON(r, "/auth/register", `{"name":"New User","username":"newuser","password":"Ab12"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d", w.Code)
	}
	// Long enough but no digit (custom rule).
	if w := postJSON(r, "/auth/register", `{"name":"New User","username":"newuser","password":"Abcdefghij"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for letters-only password, got %d", w.Code)
	}
	// Valid.
	if w := postJSON(r, "/auth/register", `{"name":"New User","username":"newuser","password":"Abcd1234"}`); w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid registration, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestRefreshExchangesRefreshTokenForAccess(t *testing.T) {
	r := setupAuthRouter()

	refresh, err := GenerateRefreshToken("u1", "alice")
	if err != nil {
		t.Fatalf("could not mint refresh token: %v", err)
	}

	w := postJSON(r, "/auth/refresh", `{"refresh_token":"`+refresh+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from refresh, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"token"`) || !strings.Contains(w.Body.String(), `"refresh_token"`) {
		t.Errorf("expected token + refresh_token in body, got %s", w.Body.String())
	}
}

func TestRefreshRejectsAccessToken(t *testing.T) {
	r := setupAuthRouter()

	access, _ := GenerateToken("u1", "alice") // access token, not refresh
	w := postJSON(r, "/auth/refresh", `{"refresh_token":"`+access+`"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when using an access token to refresh, got %d", w.Code)
	}
}

func TestRefreshTokenRejectedAsAccessToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	refresh, _ := GenerateRefreshToken("u1", "alice")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+refresh)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when a refresh token hits a protected route, got %d", w.Code)
	}
}

func TestRateLimiterAllowsThenBlocks(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	if !rl.allow("ip") || !rl.allow("ip") {
		t.Fatal("expected first two requests to be allowed")
	}
	if rl.allow("ip") {
		t.Fatal("expected third request to be blocked")
	}
	// A different client is tracked independently.
	if !rl.allow("other") {
		t.Fatal("expected a different key to be allowed")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	base := time.Now()
	rl.now = func() time.Time { return base }
	if !rl.allow("ip") {
		t.Fatal("first request should be allowed")
	}
	if rl.allow("ip") {
		t.Fatal("second request in-window should be blocked")
	}
	rl.now = func() time.Time { return base.Add(2 * time.Minute) }
	if !rl.allow("ip") {
		t.Fatal("request after the window should be allowed again")
	}
}

func TestUpdateFCMToken(t *testing.T) {
	repo := NewInMemoryUserRepository()
	u := User{ID: "u123", Username: "tokenuser"}
	_ = repo.Create(u)

	// Test GetByID / UpdateFCMToken / GetFCMToken repository methods
	gotUser, ok := repo.GetByID("u123")
	if !ok || gotUser.Username != "tokenuser" {
		t.Fatalf("expected to find user u123 by ID")
	}

	err := repo.UpdateFCMToken("u123", "fcm_test_token")
	if err != nil {
		t.Fatalf("failed to update FCM token: %v", err)
	}

	token, found := repo.GetFCMToken("u123")
	if !found || token != "fcm_test_token" {
		t.Fatalf("expected token to be 'fcm_test_token', got %q", token)
	}

	// Test API Handler
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewAuthHandler(repo)
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "u123")
		c.Next()
	})
	r.PUT("/api/profile/fcm-token", h.UpdateFCMToken)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/api/profile/fcm-token", strings.NewReader(`{"token":"fcm_api_token"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}

	token, found = repo.GetFCMToken("u123")
	if !found || token != "fcm_api_token" {
		t.Fatalf("expected token to be 'fcm_api_token', got %q", token)
	}
}
