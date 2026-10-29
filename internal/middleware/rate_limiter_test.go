package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/ratelimit"
)

func limitedRouter(store ratelimit.Store, p Policy) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	_ = ConfigureClientIP(r, nil, "") // production default: trust no proxy
	r.Use(RateLimit(store, p, ByClientIP))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func get(r http.Handler, remoteAddr string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRateLimit_RefusesOverLimitWithRetryAfter(t *testing.T) {
	r := limitedRouter(ratelimit.NewMemoryStore(), Policy{Name: "t", Limit: 2, Window: time.Minute})
	for i := 0; i < 2; i++ {
		if w := get(r, "203.0.113.1:1000", nil); w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}
	w := get(r, "203.0.113.1:1000", nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("429 must carry a positive Retry-After, got %q", ra)
	}
	if w := get(r, "203.0.113.2:1000", nil); w.Code != http.StatusOK {
		t.Fatalf("another client must have its own limit, got %d", w.Code)
	}
}

func TestRateLimit_SpoofedForwardedForDoesNotChangeTheKey(t *testing.T) {
	r := limitedRouter(ratelimit.NewMemoryStore(), Policy{Name: "t", Limit: 1, Window: time.Minute})
	get(r, "203.0.113.1:1000", map[string]string{"X-Forwarded-For": "198.51.100.1"})
	if w := get(r, "203.0.113.1:1000", map[string]string{"X-Forwarded-For": "198.51.100.2"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("an untrusted peer must not get a fresh limit by changing X-Forwarded-For, got %d", w.Code)
	}
}

func TestRateLimit_PoliciesHaveSeparateCounters(t *testing.T) {
	store := ratelimit.NewMemoryStore()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/a", RateLimit(store, Policy{Name: "a", Limit: 1, Window: time.Minute}, ByClientIP), func(c *gin.Context) { c.Status(200) })
	r.GET("/b", RateLimit(store, Policy{Name: "b", Limit: 1, Window: time.Minute}, ByClientIP), func(c *gin.Context) { c.Status(200) })
	for _, path := range []string{"/a", "/b"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, w.Code)
		}
	}
}

// brokenStore fails every call, like an unreachable Redis.
type brokenStore struct{}

var errDown = errors.New("redis down")

func (brokenStore) Hit(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errDown
}
func (brokenStore) Record(context.Context, string, time.Duration) error { return errDown }
func (brokenStore) Events(context.Context, string, time.Duration) ([]time.Time, error) {
	return nil, errDown
}
func (brokenStore) Clear(context.Context, string) error { return errDown }
func (brokenStore) Ping(context.Context) error          { return errDown }

func TestRateLimit_StoreDownFailsOpenOrClosedPerPolicy(t *testing.T) {
	open := limitedRouter(brokenStore{}, Policy{Name: "o", Limit: 1, Window: time.Minute})
	if w := get(open, "203.0.113.1:1000", nil); w.Code != http.StatusOK {
		t.Fatalf("a fail-open policy must let requests through when the store is down, got %d", w.Code)
	}
	closed := limitedRouter(brokenStore{}, Policy{Name: "c", Limit: 1, Window: time.Minute, FailClosed: true})
	if w := get(closed, "203.0.113.1:1000", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a fail-closed policy must return 503 when the store is down, got %d", w.Code)
	}
}
