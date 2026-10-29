package middleware

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

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

// Rate limit policies (sliding windows shared through Redis). Global counts per
// user when the request carries a valid token and per IP otherwise; Auth counts
// per IP; Analytics counts per authenticated user.
var (
	GlobalPolicy    = Policy{Name: "global", Limit: 100, Window: time.Minute}
	AuthPolicy      = Policy{Name: "auth", Limit: 5, Window: time.Minute, FailClosed: true}
	AnalyticsPolicy = Policy{Name: "analytics", Limit: 10, Window: time.Minute} // database-heavy
)

// KeyFunc names the client a request is counted against.
type KeyFunc func(c *gin.Context) string

// ByClientIP counts requests per client IP (see ConfigureClientIP).
func ByClientIP(c *gin.Context) string { return "ip:" + c.ClientIP() }

// ByUserID counts requests per authenticated user; it must run after AuthMiddleware.
func ByUserID(c *gin.Context) string {
	return "user:" + strconv.FormatUint(uint64(c.MustGet("userID").(uint)), 10)
}

// ByUserOrIP counts requests per user when they carry a validly signed, unexpired
// access token, and per client IP otherwise. It checks only the signature (no
// database), so it can run before routing; AuthMiddleware still decides access.
func ByUserOrIP(jwtSecret string) KeyFunc {
	return func(c *gin.Context) string {
		scheme, raw, ok := strings.Cut(c.GetHeader("Authorization"), " ")
		if ok && strings.EqualFold(scheme, "bearer") {
			token, err := jwt.Parse(raw, hmacKey(jwtSecret))
			if err == nil && token.Valid {
				if claims, ok := token.Claims.(jwt.MapClaims); ok {
					if id, ok := claims["user_id"].(float64); ok && id >= 1 {
						return "user:" + strconv.FormatUint(uint64(id), 10)
					}
				}
			}
		}
		return ByClientIP(c)
	}
}

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
