package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"discipline-backend/internal/middleware"
)

const routesJWTSecret = "jwt-secret-for-tests-0123456789abcdef"

func userToken(t *testing.T, id uint) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": id, "role": "user", "tv": 0, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(routesJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// send makes a request that passes auth (when token is set) and stops at the
// handler's body validation, so only the limiter decides between 429 and not.
func send(r http.Handler, method, path, ip, token string) int {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.RemoteAddr = ip + ":1000"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRateLimit_AuthenticatedRequestsAreCountedPerUser(t *testing.T) {
	r := newTestRouter(false)
	alice, bob := userToken(t, 1), userToken(t, 2)
	for i := 0; i < middleware.GlobalPolicy.Limit; i++ {
		if c := send(r, http.MethodPost, "/api/tasks", "203.0.113.1", alice); c == http.StatusTooManyRequests {
			t.Fatalf("request %d of Alice's budget was refused", i+1)
		}
	}
	if c := send(r, http.MethodPost, "/api/tasks", "203.0.113.1", alice); c != http.StatusTooManyRequests {
		t.Fatalf("Alice over her limit must get 429, got %d", c)
	}
	if c := send(r, http.MethodPost, "/api/tasks", "203.0.113.1", bob); c == http.StatusTooManyRequests {
		t.Fatal("Bob behind the same IP (shared NAT) must have his own budget")
	}
	if c := send(r, http.MethodPost, "/api/tasks", "198.51.100.9", alice); c != http.StatusTooManyRequests {
		t.Fatalf("Alice's budget must follow her to another IP, got %d", c)
	}
}

func TestRateLimit_AnalyticsIsCountedPerUser(t *testing.T) {
	r := newTestRouter(false)
	alice, bob := userToken(t, 1), userToken(t, 2)
	for i := 0; i < middleware.AnalyticsPolicy.Limit; i++ {
		send(r, http.MethodPost, "/api/analytics/smart-schedule", "203.0.113.1", alice)
	}
	if c := send(r, http.MethodPost, "/api/analytics/smart-schedule", "198.51.100.9", alice); c != http.StatusTooManyRequests {
		t.Fatalf("Alice's analytics budget must follow her to another IP, got %d", c)
	}
	if c := send(r, http.MethodPost, "/api/analytics/smart-schedule", "203.0.113.1", bob); c == http.StatusTooManyRequests {
		t.Fatal("Bob behind the same IP must have his own analytics budget")
	}
}

func TestRateLimit_UnauthenticatedRequestsAreCountedPerIP(t *testing.T) {
	r := newTestRouter(false)
	for i := 0; i < middleware.GlobalPolicy.Limit; i++ {
		send(r, http.MethodGet, "/ping", "203.0.113.1", "")
	}
	if c := send(r, http.MethodGet, "/ping", "203.0.113.1", ""); c != http.StatusTooManyRequests {
		t.Fatalf("an IP over the limit must get 429, got %d", c)
	}
	if c := send(r, http.MethodGet, "/ping", "203.0.113.2", ""); c != http.StatusOK {
		t.Fatalf("another IP must have its own budget, got %d", c)
	}
	forged := "not-a-valid-token"
	if c := send(r, http.MethodGet, "/ping", "203.0.113.1", forged); c != http.StatusTooManyRequests {
		t.Fatalf("an invalid token must not escape the IP limit, got %d", c)
	}
}
