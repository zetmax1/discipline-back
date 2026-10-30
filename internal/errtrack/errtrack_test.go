package errtrack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"

	"discipline-backend/internal/middleware"
)

// captureTransport keeps events instead of sending them.
type captureTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *captureTransport) Flush(time.Duration) bool              { return true }
func (t *captureTransport) FlushWithContext(context.Context) bool { return true }
func (t *captureTransport) Configure(sentry.ClientOptions)        {}
func (t *captureTransport) Close()                                {}
func (t *captureTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}

func TestInit_DisabledWithoutDSN(t *testing.T) {
	enabled, err := Init(Options{})
	if err != nil || enabled {
		t.Fatalf("no DSN must mean no Sentry, got enabled=%v err=%v", enabled, err)
	}
}

func TestPanicIsReportedWithRequestIDAndWithoutSecrets(t *testing.T) {
	tr := &captureTransport{}
	enabled, err := Init(Options{DSN: "https://public@o0.ingest.sentry.io/1", Environment: "test", transport: tr})
	if err != nil || !enabled {
		t.Fatalf("init: enabled=%v err=%v", enabled, err)
	}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recovery())
	r.Use(Middleware()...)
	r.POST("/boom", func(*gin.Context) { panic("boom") })

	req := httptest.NewRequest(http.MethodPost, "/boom?token=query-secret", strings.NewReader(`{"password":"body-secret"}`))
	req.Header.Set("Authorization", "Bearer header-secret")
	req.Header.Set("Cookie", "session=cookie-secret")
	req.Header.Set("X-Request-ID", "rid-0123456789")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("the app's Recovery must still answer 500, got %d", w.Code)
	}
	sentry.Flush(time.Second)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(tr.events))
	}
	e := tr.events[0]
	if e.Tags["request_id"] != "rid-0123456789" {
		t.Fatalf("event must be tagged with the request ID, tags=%v", e.Tags)
	}
	if e.Environment != "test" {
		t.Fatalf("environment: %q", e.Environment)
	}
	dump := strings.Join([]string{e.Request.URL, e.Request.QueryString, e.Request.Data, e.Request.Cookies, strings.Join(headerValues(e.Request.Headers), " ")}, " ")
	for _, secret := range []string{"query-secret", "body-secret", "header-secret", "cookie-secret"} {
		if strings.Contains(dump, secret) {
			t.Errorf("event must not contain %q: %s", secret, dump)
		}
	}
}

func headerValues(h map[string]string) []string {
	var out []string
	for k, v := range h {
		out = append(out, k+"="+v)
	}
	return out
}
