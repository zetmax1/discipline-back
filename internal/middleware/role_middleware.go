package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
)

// RequireRole returns a middleware that only allows users with the specified role.
// Must be used AFTER AuthMiddleware (which sets "userRole" in context).
func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("userRole")
		if !exists {
			apierr.Abort(c, http.StatusUnauthorized, apierr.AuthRequired, "Authentication required")
			return
		}

		if userRole.(string) != role {
			apierr.Abort(c, http.StatusForbidden, apierr.Forbidden, "Insufficient permissions. Required role: "+role)
			return
		}

		c.Next()
	}
}
