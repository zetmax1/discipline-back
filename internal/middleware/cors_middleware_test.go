package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const testOrigins = "https://app.example.com, https://trailing.example.com/"

func corsRequest(allowed, method, origin string) (*httptest.ResponseRecorder, bool) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORSMiddleware(allowed))
	reached := false
	handler := func(c *gin.Context) { reached = true; c.Status(http.StatusOK) }
	r.GET("/api/x", handler)
	r.OPTIONS("/api/x", handler)

	req := httptest.NewRequest(method, "/api/x", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", "GET")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, reached
}

func TestCORS_AllowedOriginIsEchoed(t *testing.T) {
	w, _ := corsRequest(testOrigins, http.MethodGet, "https://app.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("expected allowed origin echoed, got %q", got)
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Fatal("responses that depend on Origin must set Vary: Origin")
	}
}

func TestCORS_UnknownOriginGetsNoAllowHeader(t *testing.T) {
	w, _ := corsRequest(testOrigins, http.MethodGet, "https://evil.example")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unknown origin must not be allowed, got %q", got)
	}
}

func TestCORS_UnknownOriginPreflightRejected(t *testing.T) {
	w, reached := corsRequest(testOrigins, http.MethodOptions, "https://evil.example")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an unknown-origin preflight, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" || reached {
		t.Fatal("rejected preflight must not carry CORS headers or reach the handler")
	}
}

func TestCORS_AllowedOriginPreflight(t *testing.T) {
	w, _ := corsRequest(testOrigins, http.MethodOptions, "https://app.example.com")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("preflight must list allowed methods")
	}
}

func TestCORS_PrefixAndSuffixLookalikesRejected(t *testing.T) {
	for _, origin := range []string{
		"https://app.example.com.evil.example",
		"https://evilapp.example.com",
		"http://app.example.com",
		"https://app.example.com:8443",
	} {
		w, _ := corsRequest(testOrigins, http.MethodGet, origin)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s must not match the allowlist, got %q", origin, got)
		}
	}
}

func TestCORS_ConfigTrailingSlashIsNormalized(t *testing.T) {
	w, _ := corsRequest(testOrigins, http.MethodGet, "https://trailing.example.com")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://trailing.example.com" {
		t.Fatalf("configured origin with trailing slash must still match, got %q", got)
	}
}

func TestCORS_WildcardIsNotSpecial(t *testing.T) {
	w, _ := corsRequest("*", http.MethodGet, "https://evil.example")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("\"*\" must not allow arbitrary origins, got %q", got)
	}
}

func TestCORS_NoOriginPassesThrough(t *testing.T) {
	w, reached := corsRequest(testOrigins, http.MethodGet, "")
	if !reached || w.Code != http.StatusOK {
		t.Fatalf("same-origin/non-browser requests must pass, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no CORS headers without an Origin")
	}
}

// Every header the web sends must pass the preflight: writes that are safe to replay carry an
// Idempotency-Key (F1, F13, F9a). Without it the browser blocks the request and the web queues
// it as if offline, so nothing ever syncs.
func TestCORS_PreflightAllowsEveryHeaderTheWebSends(t *testing.T) {
	w, _ := corsRequest(testOrigins, http.MethodOptions, "https://app.example.com")
	allowed := map[string]bool{}
	for _, h := range strings.Split(w.Header().Get("Access-Control-Allow-Headers"), ",") {
		allowed[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for _, h := range []string{"Content-Type", "Authorization", "Idempotency-Key"} {
		if !allowed[strings.ToLower(h)] {
			t.Errorf("Access-Control-Allow-Headers %q lacks %s", w.Header().Get("Access-Control-Allow-Headers"), h)
		}
	}
}
