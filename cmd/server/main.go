package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"discipline-backend/config"
	_ "discipline-backend/docs" // Swagger generated docs
	"discipline-backend/internal/handlers"
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

	// Initialize layers: repository → service → handler

	// Auth
	userRepo := repositories.NewUserRepository(db)
	authService := services.NewAuthService(userRepo, cfg.JWTSecret, cfg.JWTExpiryHrs)
	authHandler := handlers.NewAuthHandler(authService)

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

	// Setup Gin router
	r := gin.Default()

	// Register all routes
	routes.SetupRoutes(r, authHandler, taskHandler, quoteHandler, vocabHandler, cfg.JWTSecret, cfg.AllowedOrigins, cfg.AnalyticsAPIKey)

	// Start server
	log.Printf("Server starting on port %s", cfg.Port)
	log.Printf("Swagger UI: http://localhost:%s/swagger/index.html", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}