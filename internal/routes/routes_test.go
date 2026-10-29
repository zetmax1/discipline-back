package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/handlers"
	"discipline-backend/internal/middleware"
)

func newTestRouter(enableDocs bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRoutes(r, &handlers.AuthHandler{}, &handlers.TaskHandler{}, &handlers.QuoteHandler{}, &handlers.VocabHandler{},
		"jwt-secret-for-tests-0123456789abcdef", "https://app.example.com", "http://py.invalid",
		"internal-secret-for-tests-0123456789", enableDocs,
		func(uint) (*middleware.AuthUser, error) { return &middleware.AuthUser{Role: "user", Timezone: "UTC"}, nil })
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
