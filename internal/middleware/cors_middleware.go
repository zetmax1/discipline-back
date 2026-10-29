package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware allows cross-origin requests only from origins in the allowlist.
// allowedOrigins is a comma-separated list like "http://localhost:8081,https://example.com".
// Origins are compared exactly (a trailing "/" in the config is ignored); "*" is not special.
func CORSMiddleware(allowedOrigins string) gin.HandlerFunc {
	// Parse comma-separated origins into a set for fast lookup
	originSet := make(map[string]bool)
	for _, o := range strings.Split(allowedOrigins, ",") {
		trimmed := strings.TrimRight(strings.TrimSpace(o), "/")
		if trimmed != "" {
			originSet[trimmed] = true
		}
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin == "" {
			c.Next()
			return
		}

		c.Writer.Header().Add("Vary", "Origin")
		if !originSet[origin] {
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
				return
			}
			c.Next()
			return
		}

		c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
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
