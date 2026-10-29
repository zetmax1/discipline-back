package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

func TestRateLimiter_MapSizeIsCapped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := newRateLimiter(rate.Every(1), 100, 3)
	r := gin.New()
	r.Use(rl.handler())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = fmt.Sprintf("203.0.113.%d:1000", i)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, w.Code)
		}
	}
	if n := rl.size(); n > 3 {
		t.Fatalf("rate limiter tracked %d clients, cap is 3", n)
	}
}

func TestRateLimiter_OverflowClientsShareALimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// burst 1, effectively no refill during the test, cap 1 tracked client
	rl := newRateLimiter(rate.Every(1e12), 1, 1)
	r := gin.New()
	r.Use(rl.handler())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	codes := []int{}
	for _, ip := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":1000"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	// .1 is tracked; .2 and .3 overflow into one shared bucket of burst 1
	want := []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, codes)
		}
	}
}
