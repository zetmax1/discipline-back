package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func health(t *testing.T, checks map[string]HealthCheck) (int, map[string]any, string) {
	t.Helper()
	r := newTestRouter(false)
	SetupHealth(r, checks)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %s", w.Body.String())
	}
	return w.Code, body, w.Body.String()
}

func ok(context.Context) error { return nil }

func TestHealth_AllDependenciesUp(t *testing.T) {
	code, body, _ := health(t, map[string]HealthCheck{"database": ok, "redis": ok})
	checks := body["checks"].(map[string]any)
	if code != http.StatusOK || body["status"] != "ok" || checks["database"] != "ok" || checks["redis"] != "ok" {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestHealth_OneDependencyDownIs503WithoutDetails(t *testing.T) {
	down := func(context.Context) error {
		return errors.New("dial tcp redis.railway.internal:6379: connection refused")
	}
	code, body, raw := health(t, map[string]HealthCheck{"database": ok, "redis": down})
	checks := body["checks"].(map[string]any)
	if code != http.StatusServiceUnavailable || body["status"] != "unavailable" || checks["redis"] != "unavailable" || checks["database"] != "ok" {
		t.Fatalf("got %d %v", code, body)
	}
	if strings.Contains(raw, "railway.internal") || strings.Contains(raw, "refused") {
		t.Fatalf("health output must not expose error details: %s", raw)
	}
}

func TestHealth_SlowDependencyTimesOut(t *testing.T) {
	prev := healthCheckTimeout
	healthCheckTimeout = 50 * time.Millisecond
	t.Cleanup(func() { healthCheckTimeout = prev })
	slow := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	start := time.Now()
	code, _, _ := health(t, map[string]HealthCheck{"database": slow})
	if code != http.StatusServiceUnavailable || time.Since(start) > time.Second {
		t.Fatalf("a hung dependency must fail fast with 503, got %d after %v", code, time.Since(start))
	}
}
