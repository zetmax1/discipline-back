package routes

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/errtrack"
	"discipline-backend/internal/handlers"
	"discipline-backend/internal/middleware"
	"discipline-backend/internal/models"
	"discipline-backend/internal/ratelimit"
	"discipline-backend/internal/repositories"
)

// UserLoader adapts the user repository for AuthMiddleware.
func UserLoader(users repositories.UserRepository) middleware.UserLoader {
	return func(id uint) (*middleware.AuthUser, error) {
		u, err := users.FindByID(id)
		if err != nil {
			return nil, err
		}
		return &middleware.AuthUser{Role: u.Role, Timezone: u.Timezone, TokenVersion: u.TokenVersion}, nil
	}
}

func SetupRoutes(
	r *gin.Engine,
	authHandler *handlers.AuthHandler,
	taskHandler *handlers.TaskHandler,
	quoteHandler *handlers.QuoteHandler,
	vocabHandler *handlers.VocabHandler,
	featureHandler *handlers.FeatureHandler,
	dailyHandler *handlers.DailyHandler,
	plannerHandler *handlers.PlannerHandlers,
	jwtSecret string,
	allowedOrigins string,
	pythonServiceURL string,
	internalJWTSecret string,
	enableDocs bool,
	loadUser middleware.UserLoader,
	limits ratelimit.Store,
) {
	// Global Middlewares (request ID and access log first, so even a recovered panic is logged)
	r.Use(middleware.RequestID(), middleware.AccessLog(), middleware.Recovery())
	r.Use(errtrack.Middleware()...) // no-op unless SENTRY_DSN is set
	r.Use(middleware.BodyLimit(middleware.MaxRequestBodyBytes))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.RateLimit(limits, middleware.GlobalPolicy, middleware.ByUserOrIP(jwtSecret)))
	r.Use(middleware.CORSMiddleware(allowedOrigins))

	r.NoRoute(middleware.CORSMiddleware(allowedOrigins), func(c *gin.Context) {
		apierr.JSON(c, 404, apierr.NotFound, "route not found")
	})
	r.NoMethod(middleware.CORSMiddleware(allowedOrigins), func(c *gin.Context) {
		apierr.JSON(c, 405, apierr.MethodNotAllowed, "method not allowed")
	})

	// Swagger UI (only when ENABLE_DOCS=true)
	if enableDocs {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// Health check
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "backend running"})
	})

	api := r.Group("/api")
	{
		// Public auth routes with strict rate limiting
		auth := api.Group("/auth")
		auth.Use(middleware.RateLimit(limits, middleware.AuthPolicy, middleware.ByClientIP))
		{
			auth.POST("/register", authHandler.Register)
			auth.POST("/login", authHandler.Login)
		}

		// Protected routes (any authenticated user)
		protected := api.Group("")
		protected.Use(middleware.AuthMiddleware(jwtSecret, loadUser))
		{
			// User
			protected.GET("/me", authHandler.Me)
			protected.PUT("/me", authHandler.UpdateProfile)
			protected.POST("/auth/logout", authHandler.Logout)

			// Feature flags (FEATURE_<id>=true on the server)
			protected.GET("/features", featureHandler.List)

			// Tasks
			protected.POST("/tasks", taskHandler.CreateTask)
			protected.POST("/tasks/bulk", taskHandler.BulkCreateTasks)
			protected.PUT("/tasks/plan", taskHandler.SavePlan)
			planner := featureHandler.Enabled("F13") && plannerHandler != nil // categories and recurring blocks
			if planner {
				protected.GET("/tasks", handlers.WithRange(plannerHandler.Recurring.Range, taskHandler.GetTasks))
			} else {
				protected.GET("/tasks", taskHandler.GetTasks)
			}
			protected.PUT("/tasks/:id", taskHandler.UpdateTask)
			protected.POST("/tasks/:id/distractions", taskHandler.AdjustDistractions)
			protected.DELETE("/tasks/:id", taskHandler.DeleteTask)
			if featureHandler.Enabled("F1") { // the daily loop
				protected.POST("/tasks/:id/check-in", taskHandler.CheckIn)
				protected.POST("/tasks/:id/move", taskHandler.MoveTask)
				handlers.RegisterDailyRoutes(protected, dailyHandler)
			}
			if planner {
				handlers.RegisterCategoryRoutes(protected, plannerHandler.Categories)
				handlers.RegisterRecurringRoutes(protected, plannerHandler.Recurring)
			}

			analyticsHandler := handlers.NewAnalyticsHandler(pythonServiceURL, internalJWTSecret)

			// Dashboard: today's metrics come from the analytics service's single metrics module
			protected.GET("/dashboard/today", analyticsHandler.GetToday)

			// Analytics (proxied to Python service with strict rate limiting)
			analytics := protected.Group("/analytics")
			analytics.Use(middleware.RateLimit(limits, middleware.AnalyticsPolicy, middleware.ByUserID))
			{
				analytics.GET("", analyticsHandler.GetAnalytics)
				analytics.GET("/focus-patterns", analyticsHandler.GetFocusPatterns)
				analytics.POST("/smart-schedule", analyticsHandler.GenerateSmartSchedule)
			}
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
		admin.Use(middleware.AuthMiddleware(jwtSecret, loadUser))
		admin.Use(middleware.RequireRole(models.RoleAdmin))
		{
			admin.POST("/register", authHandler.RegisterAdmin)
		}
	}
}
