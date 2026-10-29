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

// defaultMaxClients caps how many client IPs a limiter tracks at once.
const defaultMaxClients = 10000

// rateLimiter keeps one token bucket per client IP, up to maxClients entries.
// Clients beyond the cap share a single overflow bucket instead of growing the map.
type rateLimiter struct {
	mu         sync.Mutex
	clients    map[string]*client
	overflow   *rate.Limiter
	r          rate.Limit
	b          int
	maxClients int
}

func newRateLimiter(r rate.Limit, b int, maxClients int) *rateLimiter {
	return &rateLimiter{
		clients:    make(map[string]*client),
		overflow:   rate.NewLimiter(r, b),
		r:          r,
		b:          b,
		maxClients: maxClients,
	}
}

// limiterFor returns the bucket for ip, creating one if there is room.
func (rl *rateLimiter) limiterFor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if c, found := rl.clients[ip]; found {
		c.lastSeen = time.Now()
		return c.limiter
	}
	if len(rl.clients) >= rl.maxClients {
		rl.pruneLocked(time.Minute)
	}
	if len(rl.clients) >= rl.maxClients {
		return rl.overflow
	}
	c := &client{limiter: rate.NewLimiter(rl.r, rl.b), lastSeen: time.Now()}
	rl.clients[ip] = c
	return c.limiter
}

// pruneLocked removes clients idle for longer than maxIdle. Must be called with mu held.
func (rl *rateLimiter) pruneLocked(maxIdle time.Duration) {
	for ip, c := range rl.clients {
		if time.Since(c.lastSeen) > maxIdle {
			delete(rl.clients, ip)
		}
	}
}

func (rl *rateLimiter) size() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.clients)
}

func (rl *rateLimiter) handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.limiterFor(c.ClientIP()).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many requests. Please try again later.",
			})
			return
		}

		c.Next()
	}
}

// RateLimiter returns a middleware that limits requests by IP.
// r is the rate in requests per second, b is the burst size.
func RateLimiter(r rate.Limit, b int) gin.HandlerFunc {
	rl := newRateLimiter(r, b, defaultMaxClients)

	// Cleanup stale clients periodically
	go func() {
		for {
			time.Sleep(time.Minute)
			rl.mu.Lock()
			rl.pruneLocked(3 * time.Minute)
			rl.mu.Unlock()
		}
	}()

	return rl.handler()
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
