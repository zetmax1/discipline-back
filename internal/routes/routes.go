package routes

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"discipline-backend/internal/handlers"
	"discipline-backend/internal/middleware"
	"discipline-backend/internal/models"
)

// SetupRoutes registers all API routes on the Gin engine.
func SetupRoutes(
	r *gin.Engine,
	authHandler *handlers.AuthHandler,
	taskHandler *handlers.TaskHandler,
	quoteHandler *handlers.QuoteHandler,
	vocabHandler *handlers.VocabHandler,
	jwtSecret string,
	allowedOrigins string,
) {
	// Global Middlewares
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.GlobalRateLimiter())
	r.Use(middleware.CORSMiddleware(allowedOrigins))

	// Swagger UI
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Health check
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "backend running"})
	})

	api := r.Group("/api")
	{
		// Public auth routes with strict rate limiting
		auth := api.Group("/auth")
		auth.Use(middleware.AuthRateLimiter())
		{
			auth.POST("/register", authHandler.Register)
			auth.POST("/login", authHandler.Login)
		}

		// Protected routes (any authenticated user)
		protected := api.Group("")
		protected.Use(middleware.AuthMiddleware(jwtSecret))
		{
			// User
			protected.GET("/me", authHandler.Me)
			protected.PUT("/me", authHandler.UpdateProfile)

			// Tasks
			protected.POST("/tasks", taskHandler.CreateTask)
			protected.GET("/tasks", taskHandler.GetTasks)
			protected.PUT("/tasks/:id", taskHandler.UpdateTask)
			protected.DELETE("/tasks/:id", taskHandler.DeleteTask)

			// Dashboard
			protected.GET("/dashboard/stats", taskHandler.GetDashboardStats)

			// Quotes
			protected.GET("/quote", quoteHandler.GetQuote)

			// Vocabulary
			protected.GET("/vocabulary/today", vocabHandler.GetTodayWords)
			protected.GET("/vocabulary", vocabHandler.GetAllWords)
			protected.PUT("/vocabulary/:id/learned", vocabHandler.ToggleLearned)
			protected.PUT("/vocabulary/:id/saved", vocabHandler.ToggleSaved)
		}

		// Admin-only routes
		admin := api.Group("/admin")
		admin.Use(middleware.AuthMiddleware(jwtSecret))
		admin.Use(middleware.RequireRole(models.RoleAdmin))
		{
			admin.POST("/register", authHandler.RegisterAdmin)
		}
	}
}
