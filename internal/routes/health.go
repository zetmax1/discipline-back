package routes

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// HealthCheck reports whether one dependency is reachable.
type HealthCheck func(ctx context.Context) error

// healthCheckTimeout bounds each check so a hung dependency fails fast.
var healthCheckTimeout = 2 * time.Second

// SetupHealth registers GET /health: 200 when every check passes, 503 otherwise.
// The body names each dependency's state only; error details go to the log.
func SetupHealth(r *gin.Engine, checks map[string]HealthCheck) {
	r.GET("/health", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), healthCheckTimeout)
		defer cancel()

		var mu sync.Mutex
		var wg sync.WaitGroup
		results := make(map[string]string, len(checks))
		healthy := true
		for name, check := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := check(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					slog.ErrorContext(c.Request.Context(), "health check failed", "check", name, "error", err)
					results[name], healthy = "unavailable", false
					return
				}
				results[name] = "ok"
			}()
		}
		wg.Wait()

		c.Header("Cache-Control", "no-store")
		if !healthy {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "checks": results})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "checks": results})
	})
}
