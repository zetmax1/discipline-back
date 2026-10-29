package middleware

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/ratelimit"
)

// Policy is a sliding-window limit: at most Limit requests per Window and key.
// FailClosed answers 503 while the store is unreachable; otherwise requests pass.
type Policy struct {
	Name       string
	Limit      int
	Window     time.Duration
	FailClosed bool
}

// Rate limit policies (sliding windows shared through Redis).
var (
	GlobalPolicy    = Policy{Name: "global", Limit: 100, Window: time.Minute}
	AuthPolicy      = Policy{Name: "auth", Limit: 5, Window: time.Minute, FailClosed: true}
	AnalyticsPolicy = Policy{Name: "analytics", Limit: 10, Window: time.Minute} // database-heavy
)

// KeyFunc names the client a request is counted against.
type KeyFunc func(c *gin.Context) string

// ByClientIP counts requests per client IP (see ConfigureClientIP).
func ByClientIP(c *gin.Context) string { return "ip:" + c.ClientIP() }

// RetryAfterSeconds rounds a wait up to whole seconds, at least 1.
func RetryAfterSeconds(d time.Duration) string {
	return strconv.Itoa(max(1, int(math.Ceil(d.Seconds()))))
}

// RateLimit enforces p per key, answering 429 with Retry-After when exceeded.
func RateLimit(store ratelimit.Store, p Policy, key KeyFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, retryAfter, err := store.Hit(c.Request.Context(), "rl:"+p.Name+":"+key(c), p.Limit, p.Window)
		if err != nil {
			log.Printf("rate limit %s: store unavailable: %v", p.Name, err)
			if p.FailClosed {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"error": "Service temporarily unavailable. Please try again shortly.",
				})
				return
			}
			c.Next()
			return
		}
		if !allowed {
			c.Header("Retry-After", RetryAfterSeconds(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many requests. Please try again later.",
			})
			return
		}
		c.Next()
	}
}
