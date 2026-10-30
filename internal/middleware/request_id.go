package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/logging"
)

// RequestIDHeader carries the request ID to clients and to the analytics service.
const RequestIDHeader = "X-Request-ID"

// validRequestID accepts IDs from a proxy in front of us; anything else is replaced.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)

// RequestID keeps a well-formed incoming X-Request-ID or generates one, stores it
// in the request context for logging and echoes it in the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			buf := make([]byte, 16)
			_, _ = rand.Read(buf)
			id = hex.EncodeToString(buf)
		}
		c.Request = c.Request.WithContext(logging.WithRequestID(c.Request.Context(), id))
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// AccessLog writes one JSON line per request. It logs the path without the query
// string and no headers, so tokens and personal data stay out of the logs.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
		}
		if id, ok := c.Get("userID"); ok {
			attrs = append(attrs, "user_id", id)
		}
		level := slog.LevelInfo
		if c.Writer.Status() >= 500 {
			level = slog.LevelError
		}
		slog.Log(c.Request.Context(), level, "request", attrs...)
	}
}

// Recovery turns a panic into a 500 and logs it (with the stack) as JSON.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, err any) {
		slog.ErrorContext(c.Request.Context(), "panic", "error", fmt.Sprint(err), "stack", string(debug.Stack()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
	})
}
