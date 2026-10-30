package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/handlers"
	"discipline-backend/internal/ratelimit"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
	"discipline-backend/internal/testdb"
)

// acceptAllCaptcha stands in for Turnstile in flows that are not about the captcha.
type acceptAllCaptcha struct{}

func (acceptAllCaptcha) Verify(context.Context, string) (bool, error) { return true, nil }

const flowJWTSecret = "jwt-secret-for-tests-0123456789abcdef"

// newAppRouter wires the real handlers, services and repositories to a migrated test DB.
func newAppRouter(t *testing.T) *gin.Engine {
	t.Helper()
	db, _ := testdb.NewMigrated(t)
	userRepo := repositories.NewUserRepository(db)
	authHandler := handlers.NewAuthHandler(services.NewAuthService(userRepo, flowJWTSecret), services.NewLoginThrottle(ratelimit.NewMemoryStore()), acceptAllCaptcha{})
	taskHandler := handlers.NewTaskHandler(services.NewTaskService(repositories.NewTaskRepository(db)))
	vocabHandler := handlers.NewVocabHandler(services.NewVocabService(repositories.NewVocabRepository(db)))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRoutes(r, authHandler, taskHandler, handlers.NewQuoteHandler(), vocabHandler,
		flowJWTSecret, "https://app.example.com", "http://py.invalid", "internal-secret-for-tests-0123456789", false,
		UserLoader(userRepo), ratelimit.NewMemoryStore())
	return r
}

func call(r http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func tokenFrom(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Token == "" {
		t.Fatalf("expected a token in %d %s", w.Code, w.Body.String())
	}
	return body.Token
}

func TestTokenRejectedAfterLogout(t *testing.T) {
	r := newAppRouter(t)
	reg := call(r, http.MethodPost, "/api/auth/register", "", `{"name":"Ann","email":"ann@example.com","password":"long-enough-pw","turnstileToken":"t"}`)
	token := tokenFrom(t, reg)
	other := tokenFrom(t, call(r, http.MethodPost, "/api/auth/login", "", `{"email":"ann@example.com","password":"long-enough-pw","turnstileToken":"t"}`))

	if w := call(r, http.MethodGet, "/api/me", token, ""); w.Code != http.StatusOK {
		t.Fatalf("fresh token must work, got %d", w.Code)
	}
	if w := call(r, http.MethodPost, "/api/auth/logout", token, ""); w.Code != http.StatusNoContent {
		t.Fatalf("logout must succeed with 204, got %d %s", w.Code, w.Body.String())
	}
	if w := call(r, http.MethodGet, "/api/me", token, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("token must be rejected after logout, got %d", w.Code)
	}
	if w := call(r, http.MethodGet, "/api/me", other, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("logout must revoke the user's other sessions too, got %d", w.Code)
	}
	if w := call(r, http.MethodPost, "/api/auth/logout", token, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("logout with a revoked token must be 401, got %d", w.Code)
	}

	fresh := tokenFrom(t, call(r, http.MethodPost, "/api/auth/login", "", `{"email":"ann@example.com","password":"long-enough-pw","turnstileToken":"t"}`))
	if w := call(r, http.MethodGet, "/api/me", fresh, ""); w.Code != http.StatusOK {
		t.Fatalf("logging in again must work, got %d", w.Code)
	}
}

func TestTokenRejectedAfterPasswordChange(t *testing.T) {
	r := newAppRouter(t)
	token := tokenFrom(t, call(r, http.MethodPost, "/api/auth/register", "", `{"name":"Ann","email":"ann@example.com","password":"long-enough-pw","turnstileToken":"t"}`))
	otherDevice := tokenFrom(t, call(r, http.MethodPost, "/api/auth/login", "", `{"email":"ann@example.com","password":"long-enough-pw","turnstileToken":"t"}`))

	w := call(r, http.MethodPut, "/api/me", token, `{"oldPassword":"long-enough-pw","newPassword":"another-long-pw"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("password change failed: %d %s", w.Code, w.Body.String())
	}
	replacement := tokenFrom(t, w)

	for name, tok := range map[string]string{"changing session's old token": token, "other device": otherDevice} {
		if w := call(r, http.MethodGet, "/api/me", tok, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s must be rejected after a password change, got %d", name, w.Code)
		}
	}
	if w := call(r, http.MethodGet, "/api/me", replacement, ""); w.Code != http.StatusOK {
		t.Fatalf("the replacement token returned by PUT /me must work, got %d", w.Code)
	}

	// A profile change without a new password keeps existing sessions.
	if w := call(r, http.MethodPut, "/api/me", replacement, `{"name":"Annie"}`); w.Code != http.StatusOK {
		t.Fatalf("name change failed: %d", w.Code)
	}
	if w := call(r, http.MethodGet, "/api/me", replacement, ""); w.Code != http.StatusOK {
		t.Fatalf("a name change must not revoke the session, got %d", w.Code)
	}
}

func TestPlanRouteIsNotShadowedByTaskID(t *testing.T) {
	r := newAppRouter(t)
	token := tokenFrom(t, call(r, http.MethodPost, "/api/auth/register", "", `{"name":"Ann","email":"ann@example.com","password":"long-enough-pw","turnstileToken":"t"}`))
	w := call(r, http.MethodPut, "/api/tasks/plan", token,
		`{"weekStart":"2026-11-02","tasks":[{"name":"Deep Work","category":"deep-work","startTime":"09:00","endTime":"10:00","energy":3,"date":"2026-11-02"}]}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"planId"`) {
		t.Fatalf("PUT /api/tasks/plan must reach SavePlan, got %d %s", w.Code, w.Body.String())
	}
}
