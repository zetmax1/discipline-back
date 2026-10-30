package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/handlers"
	"discipline-backend/internal/middleware"
	"discipline-backend/internal/ratelimit"
)

func newTestRouter(enableDocs bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRoutes(r, &handlers.AuthHandler{}, &handlers.TaskHandler{}, &handlers.QuoteHandler{}, &handlers.VocabHandler{}, handlers.NewFeatureHandler([]string{"F1"}), &handlers.DailyHandler{},
		"jwt-secret-for-tests-0123456789abcdef", "https://app.example.com", "http://py.invalid",
		"internal-secret-for-tests-0123456789", enableDocs,
		func(uint) (*middleware.AuthUser, error) {
			return &middleware.AuthUser{Role: "user", Timezone: "UTC"}, nil
		}, ratelimit.NewMemoryStore())
	return r
}

func TestSwaggerHiddenByDefault(t *testing.T) {
	w := httptest.NewRecorder()
	newTestRouter(false).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for swagger when docs are disabled, got %d", w.Code)
	}
}

func TestSwaggerServedWhenEnabled(t *testing.T) {
	w := httptest.NewRecorder()
	newTestRouter(true).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for swagger when docs are enabled, got %d", w.Code)
	}
}

func TestRouter_RejectsBodyOverOneMB(t *testing.T) {
	big := strings.NewReader(`{"tasks":[` + strings.Repeat(" ", 1<<20) + `]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", big)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newTestRouter(false).ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 from the real router, got %d", w.Code)
	}
}

func TestRouter_RejectsUnknownCORSOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/tasks", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	newTestRouter(false).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("expected 403 without ACAO, got %d %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}

	req = httptest.NewRequest(http.MethodOptions, "/api/tasks", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w = httptest.NewRecorder()
	newTestRouter(false).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("allowed origin preflight should succeed, got %d", w.Code)
	}
}

func TestDashboardRoutes(t *testing.T) {
	r := newTestRouter(false)
	// The old Go-computed stats endpoint is gone; today's metrics come from Python.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("/api/dashboard/stats must be removed, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/today", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/api/dashboard/today must exist behind auth, got %d", w.Code)
	}
}

func TestRouter_EveryResponseCarriesARequestID(t *testing.T) {
	r := newTestRouter(false)
	for _, path := range []string{"/ping", "/no-such-route", "/api/me"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Header().Get("X-Request-ID") == "" {
			t.Errorf("%s: missing X-Request-ID", path)
		}
	}
}

func TestRouter_PanicIsRecoveredAs500(t *testing.T) {
	r := newTestRouter(false)
	r.GET("/boom", func(*gin.Context) { panic("boom") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if w.Code != http.StatusInternalServerError || w.Header().Get("X-Request-ID") == "" {
		t.Fatalf("a panic must become a 500 that still carries the request ID, got %d", w.Code)
	}
}

func TestFeaturesRoute_OnlyForSignedInUsers(t *testing.T) {
	r := newTestRouter(false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/features", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request: want 401, got %d", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/features", nil)
	req.Header.Set("Authorization", "Bearer "+userToken(t, 7))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != `{"features":["F1"]}` {
		t.Fatalf("signed in: want 200 with [F1], got %d %s", w.Code, w.Body.String())
	}
}

func TestDailyLoopRoutesFollowFeatureF1(t *testing.T) {
	served := func(r *gin.Engine) bool {
		for _, rt := range r.Routes() {
			if rt.Path == "/api/tasks/:id/check-in" {
				return true
			}
		}
		return false
	}
	if !served(newTestRouter(false)) {
		t.Fatal("with F1 on, the check-in route must exist")
	}
	gin.SetMode(gin.TestMode)
	off := gin.New()
	SetupRoutes(off, &handlers.AuthHandler{}, &handlers.TaskHandler{}, &handlers.QuoteHandler{}, &handlers.VocabHandler{},
		handlers.NewFeatureHandler(nil), &handlers.DailyHandler{}, "jwt-secret-for-tests-0123456789abcdef", "https://app.example.com", "http://py.invalid",
		"internal-secret-for-tests-0123456789", false,
		func(uint) (*middleware.AuthUser, error) {
			return &middleware.AuthUser{Role: "user", Timezone: "UTC"}, nil
		},
		ratelimit.NewMemoryStore())
	if served(off) {
		t.Fatal("with F1 off, the check-in route must not exist")
	}
}
