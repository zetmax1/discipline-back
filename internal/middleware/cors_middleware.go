package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware allows cross-origin requests from specific allowed origins.
func CORSMiddleware(allowedOrigins string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		
		// Very basic check: in production you might want a strict slice of origins.
		// For simplicity, we are setting it directly if it matches our allowed or if running local dev it's often hardcoded.
		// Since we just have one frontend origin usually, let's just use it.
		// If allowedOrigins is "*", all origins are allowed.
		if allowedOrigins == "*" || origin == allowedOrigins {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			// Fallback if no origin matches, we just set the strictly allowed one
			c.Writer.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
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
