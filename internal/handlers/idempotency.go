package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
)

// optionalIdempotencyKey reads the Idempotency-Key header of a replay-safe create (F13). No
// header is fine (the previous web version sends none); one outside 8-100 characters answers
// 400 and ok is false.
func optionalIdempotencyKey(c *gin.Context) (key string, ok bool) {
	key = c.GetHeader("Idempotency-Key")
	if n := len(key); key != "" && (n < 8 || n > 100) {
		apierr.JSON(c, http.StatusBadRequest, apierr.IdempotencyKeyRequired, "Idempotency-Key header must be 8-100 characters")
		return "", false
	}
	return key, true
}
