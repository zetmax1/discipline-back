package main

import (
	"log"
	"net/http"
	"time"
	_ "time/tzdata" // the alpine runtime image has no zoneinfo; users' IANA zones must load

	"github.com/gin-gonic/gin"

	"discipline-backend/config"
	_ "discipline-backend/docs" // Swagger generated docs
	"discipline-backend/internal/handlers"
	"discipline-backend/internal/middleware"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/routes"
	"discipline-backend/internal/services"
)

// @title           Discipline API
// @version         1.0
// @description     Backend API for the Discipline productivity app
// @host            localhost:8080
// @BasePath        /api

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Enter your JWT token with the Bearer prefix: "Bearer <token>"

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Startup aborted: %v", err)
	}

	// Connect to database
	db := config.ConnectDB(cfg)

	// Seed the first admin user if none exists
	config.SeedAdmin(db, cfg)

	// Shared rate-limit and login-throttle state
	limits := config.ConnectRedis(cfg)

	// Initialize layers: repository → service → handler

	// Auth
	userRepo := repositories.NewUserRepository(db)
	authService := services.NewAuthService(userRepo, cfg.JWTSecret)
	authHandler := handlers.NewAuthHandler(authService, services.NewLoginThrottle(limits))

	// Tasks
	taskRepo := repositories.NewTaskRepository(db)
	taskService := services.NewTaskService(taskRepo)
	taskHandler := handlers.NewTaskHandler(taskService)

	// Quotes
	quoteHandler := handlers.NewQuoteHandler()

	// Vocabulary
	vocabRepo := repositories.NewVocabRepository(db)
	vocabService := services.NewVocabService(vocabRepo)
	vocabHandler := handlers.NewVocabHandler(vocabService)

	// Setup Gin router (release mode unless APP_ENV=development)
	gin.SetMode(cfg.GinMode())
	r := gin.Default()
	if err := middleware.ConfigureClientIP(r, cfg.TrustedProxies, cfg.TrustedIPHeader); err != nil {
		log.Fatalf("Invalid trusted proxy configuration: %v", err)
	}

	// Register all routes
	routes.SetupRoutes(r, authHandler, taskHandler, quoteHandler, vocabHandler, cfg.JWTSecret, cfg.AllowedOrigins, cfg.PythonServiceURL, cfg.InternalJWTSecret, cfg.EnableDocs, routes.UserLoader(userRepo), limits)
	routes.SetupIPDebug(r, cfg.EnableIPDebug)

	// Start server
	log.Printf("Server starting on port %s", cfg.Port)
	if cfg.EnableDocs {
		log.Printf("Swagger UI: http://localhost:%s/swagger/index.html", cfg.Port)
	}
	if err := newHTTPServer(":"+cfg.Port, r).ListenAndServe(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// newHTTPServer wraps the handler with read/write/idle timeouts. WriteTimeout
// leaves room for the analytics proxy's upstream timeout.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      handlers.AnalyticsClientTimeout + 20*time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
