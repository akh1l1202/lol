package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/ai"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/auth"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/coach"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/habit"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/mood"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/notification"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/score"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/session"
	"github.com/SwDC-kjsse/app-dev-1/backend/pkg/usage"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load backend/.env if present; real environment variables still win.
	_ = godotenv.Load()

	r := gin.Default()

	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// Initialize the user store. Use Postgres when DATABASE_URL is set,
	// otherwise fall back to an in-memory store so the server still runs.
	var userRepo auth.UserRepository
	var db *sql.DB
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		var err error
		db, err = auth.OpenDB(dsn)
		if err != nil {
			log.Fatalf("connect to postgres: %v", err)
		}
		if err := auth.Migrate(db); err != nil {
			log.Fatalf("run migrations: %v", err)
		}
		if err := usage.Migrate(db); err != nil {
			log.Fatalf("run usage migrations: %v", err)
		}
		if err := mood.Migrate(db); err != nil {
			log.Fatalf("run mood migrations: %v", err)
		}
		if err := session.Migrate(db); err != nil {
			log.Fatalf("run session migrations: %v", err)
		}
		if err := habit.Migrate(db); err != nil {
			log.Fatalf("run habit migrations: %v", err)
		}
		userRepo = auth.NewPostgresUserRepository(db)
		log.Println("user store: postgres")
	} else {
		userRepo = auth.NewInMemoryUserRepository()
		log.Println("user store: in-memory (set DATABASE_URL to use postgres)")
	}

	// Initialize Notification Dispatcher
	var notificationDispatcher notification.Dispatcher
	if credJSON := os.Getenv("FIREBASE_CREDENTIALS_JSON"); credJSON != "" {
		fd, err := notification.NewFirebaseDispatcherFromJSON([]byte(credJSON))
		if err != nil {
			log.Printf("Failed to initialize Firebase dispatcher from JSON: %v. Falling back to Mock.", err)
			notificationDispatcher = notification.NewMockDispatcher()
		} else {
			notificationDispatcher = fd
			log.Println("Notification service: Firebase Cloud Messaging enabled (JSON)")
		}
	} else if credPath := os.Getenv("FIREBASE_CREDENTIALS_PATH"); credPath != "" {
		fd, err := notification.NewFirebaseDispatcher(credPath)
		if err != nil {
			log.Printf("Failed to initialize Firebase dispatcher: %v. Falling back to Mock.", err)
			notificationDispatcher = notification.NewMockDispatcher()
		} else {
			notificationDispatcher = fd
			log.Println("Notification service: Firebase Cloud Messaging enabled (File)")
		}
	} else {
		notificationDispatcher = notification.NewMockDispatcher()
		log.Println("Notification service: Mock/Stdout enabled (set FIREBASE_CREDENTIALS_JSON or FIREBASE_CREDENTIALS_PATH to enable FCM)")
	}

	authHandler := auth.NewAuthHandler(userRepo)

	// Public Routes
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Throttle the unauthenticated auth endpoints to blunt credential stuffing.
	authLimiter := auth.RateLimit(10, time.Minute)
	r.POST("/auth/register", authLimiter, authHandler.Register)
	r.POST("/auth/login", authLimiter, authHandler.Login)
	r.POST("/auth/refresh", authLimiter, authHandler.Refresh)

	// Protected Routes (guarded by JWT Auth Middleware)
	api := r.Group("/api")
	api.Use(auth.AuthMiddleware())
	{
		api.GET("/profile", func(c *gin.Context) {
			userID, _ := c.Get("user_id")
			username, _ := c.Get("username")

			// Return the full user profile including name
			user, _ := userRepo.GetByUsername(username.(string))

			c.JSON(http.StatusOK, gin.H{
				"user_id":  userID,
				"username": username,
				"name":     user.Name,
				"message":  "This is a protected route. Access granted.",
			})
		})

		api.PUT("/auth/password", authHandler.ChangePassword)
		api.PUT("/profile/fcm-token", authHandler.UpdateFCMToken)

		// Coaching fans out to the Python AI service; no DB needed, so it is
		// available even when running without Postgres. The phone calls this
		// instead of the Python service directly.
		aiClient := ai.NewClient(os.Getenv("AI_SERVICE_URL"))
		aiHandler := ai.NewHandler(aiClient)
		api.POST("/coach/analyze", aiHandler.Analyze)
		api.POST("/coach/chat", aiHandler.Chat)
		api.POST("/schedule/generate", aiHandler.GenerateSchedule)

		// Async coaching: a mood check-in enqueues a job that the worker pool
		// runs against the AI service; the phone polls the nudge here.
		coachSvc := coach.NewServiceWithNotification(aiClient, notificationDispatcher, userRepo, 2, 64)
		api.GET("/coach/nudges", coach.NewHandler(coachSvc).Nudges)

		// Data-backed routes need a database; only wired when Postgres is used.
		if db != nil {
			usageHandler := usage.NewHandlerWithAnalysis(usage.NewPostgresRepository(db), aiClient, notificationDispatcher, userRepo)
			api.POST("/usage", usageHandler.Ingest)
			api.GET("/usage", usageHandler.List)

			moodHandler := mood.NewHandlerWithCoach(mood.NewPostgresRepository(db), coachSvc)
			api.POST("/mood", moodHandler.Create)
			api.GET("/mood", moodHandler.List)

			sessionHandler := session.NewHandler(session.NewPostgresRepository(db))
			api.POST("/sessions", sessionHandler.Create)
			api.GET("/sessions", sessionHandler.List)
			api.PATCH("/sessions/:id", sessionHandler.Update)

			scoreHandler := score.NewHandler(score.NewService(db))
			api.GET("/score", scoreHandler.Get)

			habitHandler := habit.NewHandler(habit.NewPostgresRepository(db))
			api.GET("/habits", habitHandler.List)
			api.POST("/habits", habitHandler.Create)
			api.PATCH("/habits/:id", habitHandler.Update)
			api.DELETE("/habits/:id", habitHandler.Delete)
			api.POST("/habits/:id/log", habitHandler.Log)
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	if err := r.Run(":" + port); err != nil {
		panic(err)
	}
}
