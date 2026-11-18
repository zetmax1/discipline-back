package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/logging"
)

func requestIDRouter(seen *string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), AccessLog())
	r.GET("/x", func(c *gin.Context) {
		*seen = logging.RequestID(c.Request.Context())
		c.Status(http.StatusTeapot)
	})
	return r
}

func TestRequestID_GeneratedKeptOrReplaced(t *testing.T) {
	var seen string
	r := requestIDRouter(&seen)
	cases := []struct {
		incoming string
		keep     bool
	}{
		{"", false},
		{"edge-0123456789abcdef", true},
		{"has space and\nnewline", false},
		{strings.Repeat("a", 200), false},
		{"short", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if tc.incoming != "" {
			req.Header.Set("X-Request-ID", tc.incoming)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		got := w.Header().Get("X-Request-ID")
		if got != seen {
			t.Fatalf("response header %q and request context %q must match", got, seen)
		}
		if tc.keep && got != tc.incoming {
			t.Errorf("a valid incoming ID must be kept: %q -> %q", tc.incoming, got)
		}
		if !tc.keep && !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(got) {
			t.Errorf("incoming %q must be replaced by a generated ID, got %q", tc.incoming, got)
		}
	}
}

func TestAccessLog_OneJSONLineWithoutQueryOrSecrets(t *testing.T) {
	var buf bytes.Buffer
	logging.Setup(&buf)
	var seen string
	r := requestIDRouter(&seen)
	req := httptest.NewRequest(http.MethodGet, "/x?date=2026-10-29&token=leak-me", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	r.ServeHTTP(httptest.NewRecorder(), req)

	out := strings.TrimSpace(buf.String())
	if strings.Count(out, "\n") != 0 {
		t.Fatalf("expected one access line, got:\n%s", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %s", out)
	}
	if m["request_id"] != seen || m["method"] != "GET" || m["path"] != "/x" || m["status"] != float64(http.StatusTeapot) {
		t.Fatalf("unexpected access line %v", m)
	}
	if _, ok := m["duration_ms"]; !ok {
		t.Fatal("duration_ms missing")
	}
	for _, secret := range []string{"leak-me", "secret-token"} {
		if strings.Contains(out, secret) {
			t.Fatalf("access log must not contain %q: %s", secret, out)
		}
	}
}
