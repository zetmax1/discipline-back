package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// client represents a rate limited client.
type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter returns a middleware that limits requests by IP.
// r is the rate in requests per second, b is the burst size.
func RateLimiter(r rate.Limit, b int) gin.HandlerFunc {
	clients := make(map[string]*client)
	var mu sync.Mutex

	// Cleanup stale clients periodically
	go func() {
		for {
			time.Sleep(time.Minute)
			mu.Lock()
			for ip, c := range clients {
				if time.Since(c.lastSeen) > 3*time.Minute {
					delete(clients, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()
		if _, found := clients[ip]; !found {
			clients[ip] = &client{limiter: rate.NewLimiter(r, b)}
		}
		clients[ip].lastSeen = time.Now()
		limiter := clients[ip].limiter
		mu.Unlock()

		if !limiter.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many requests. Please try again later.",
			})
			return
		}

		c.Next()
	}
}

// GlobalRateLimiter allows 100 requests per minute (~1.6 per sec) with a burst of 20
func GlobalRateLimiter() gin.HandlerFunc {
	return RateLimiter(rate.Every(time.Minute/100), 20)
}

// AuthRateLimiter allows 5 requests per minute with a burst of 5
func AuthRateLimiter() gin.HandlerFunc {
	return RateLimiter(rate.Every(time.Minute/5), 5)
}

// AnalyticsRateLimiter allows 10 requests per minute with a burst of 5
// Analytics queries can be database-heavy, so they have a stricter limit than global.
func AnalyticsRateLimiter() gin.HandlerFunc {
	return RateLimiter(rate.Every(time.Minute/10), 5)
}
