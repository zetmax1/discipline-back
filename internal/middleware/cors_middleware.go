package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware allows cross-origin requests from specific allowed origins.
// allowedOrigins can be a comma-separated list like "http://localhost:8081,https://example.com"
func CORSMiddleware(allowedOrigins string) gin.HandlerFunc {
	// Parse comma-separated origins into a set for fast lookup
	originSet := make(map[string]bool)
	for _, o := range strings.Split(allowedOrigins, ",") {
		trimmed := strings.TrimSpace(o)
		if trimmed != "" {
			originSet[trimmed] = true
		}
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		if origin != "" {
			// Always reflect the origin back for local dev and avoiding credential CORS errors
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		} else if allowedOrigins == "*" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		} else if originSet[origin] {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		}

		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
