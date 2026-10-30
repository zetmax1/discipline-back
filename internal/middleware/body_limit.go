package middleware

import (
	"discipline-backend/internal/apierr"

	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxRequestBodyBytes is the largest request body the API accepts.
const MaxRequestBodyBytes = 1 << 20 // 1 MB

// BodyLimit rejects requests whose declared body exceeds maxBytes with 413 and
// caps undeclared (chunked) bodies so reads fail once the limit is passed.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			apierr.Abort(c, http.StatusRequestEntityTooLarge, apierr.BodyTooLarge, "Request body too large")
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
