package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"discipline-backend/internal/apierr"
)

// AuthUser is the per-request user state loaded from the database.
type AuthUser struct {
	Role         string
	Timezone     string
	TokenVersion int64
}

// UserLoader loads a user's current auth state; it returns gorm.ErrRecordNotFound
// when the user no longer exists.
type UserLoader func(userID uint) (*AuthUser, error)

// hmacKey accepts only HMAC-signed tokens, verified with secret.
func hmacKey(secret string) jwt.Keyfunc {
	return func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	}
}

// AuthMiddleware validates the JWT token from the Authorization header, then loads
// the user from the database. Sets "userID", "userRole" and "userTimezone".
func AuthMiddleware(jwtSecret string, loadUser UserLoader) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			apierr.Abort(c, http.StatusUnauthorized, apierr.AuthRequired, "Authorization header is required")
			return
		}

		// Expect "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			apierr.Abort(c, http.StatusUnauthorized, apierr.TokenInvalid, "Authorization header must be in format: Bearer <token>")
			return
		}

		tokenString := parts[1]

		// Parse and validate the token
		token, err := jwt.Parse(tokenString, hmacKey(jwtSecret))

		if err != nil || !token.Valid {
			apierr.Abort(c, http.StatusUnauthorized, apierr.TokenInvalid, "Invalid or expired token")
			return
		}

		// Extract claims
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			apierr.Abort(c, http.StatusUnauthorized, apierr.TokenInvalid, "Invalid token claims")
			return
		}

		// Get user ID from claims
		userIDFloat, ok := claims["user_id"].(float64)
		if !ok {
			apierr.Abort(c, http.StatusUnauthorized, apierr.TokenInvalid, "Invalid user ID in token")
			return
		}

		// Tokens issued before the user's last logout or password change are revoked.
		tokenVersion, ok := claims["tv"].(float64)
		if !ok {
			apierr.Abort(c, http.StatusUnauthorized, apierr.TokenInvalid, "Invalid or expired token")
			return
		}

		userID := uint(userIDFloat)
		user, err := loadUser(userID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			apierr.Abort(c, http.StatusUnauthorized, apierr.TokenInvalid, "Invalid or expired token")
			return
		}
		if err != nil {
			apierr.Abort(c, http.StatusInternalServerError, apierr.InternalError, "Failed to load user")
			return
		}

		if int64(tokenVersion) != user.TokenVersion {
			apierr.Abort(c, http.StatusUnauthorized, apierr.TokenInvalid, "Invalid or expired token")
			return
		}

		// Role and time zone come from the database, so changes apply immediately.
		c.Set("userID", userID)
		c.Set("userRole", user.Role)
		c.Set("userTimezone", user.Timezone)
		c.Next()
	}
}
