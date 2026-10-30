package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"discipline-backend/internal/logging"
)

const testInternalSecret = "internal-secret-for-tests-0123456789"

// upstreamRecorder is a fake Python analytics service that records what Gin forwards.
type upstreamRecorder struct {
	mu       sync.Mutex
	calls    int
	lastURL  *url.URL
	lastBody []byte
	lastAuth string
	lastRID  string
}

func (u *upstreamRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.calls++
		u.lastURL = r.URL
		u.lastBody = body
		u.lastAuth = r.Header.Get("Authorization")
		u.lastRID = r.Header.Get("X-Request-ID")
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// analyticsRouter mounts the analytics handler behind a fake auth middleware for user 1.
func analyticsRouter(h *AnalyticsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", uint(1)) })
	r.GET("/analytics", h.GetAnalytics)
	r.GET("/analytics/focus-patterns", h.GetFocusPatterns)
	r.POST("/analytics/smart-schedule", h.GenerateSmartSchedule)
	return r
}

func newTestAnalyticsHandler(t *testing.T, upstreamURL string) *AnalyticsHandler {
	t.Helper()
	return NewAnalyticsHandler(upstreamURL, testInternalSecret)
}

// assertForwardedUser checks that the upstream request identifies the user only
// through a valid internal JWT and carries no user_id query parameter.
func assertForwardedUser(t *testing.T, up *upstreamRecorder, wantSub string) {
	t.Helper()
	if got, ok := up.lastURL.Query()["user_id"]; ok {
		t.Fatalf("upstream must not receive a user_id query param, got %v", got)
	}
	raw, ok := strings.CutPrefix(up.lastAuth, "Bearer ")
	if !ok {
		t.Fatalf("upstream request must carry a Bearer token, got %q", up.lastAuth)
	}
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(tok *jwt.Token) (interface{}, error) {
		return []byte(testInternalSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("discipline-py"), jwt.WithExpirationRequired())
	if err != nil {
		t.Fatalf("internal token invalid: %v", err)
	}
	if claims.Subject != wantSub {
		t.Fatalf("internal token sub must be %q, got %q", wantSub, claims.Subject)
	}
	if ttl := time.Until(claims.ExpiresAt.Time); ttl <= 0 || ttl > 60*time.Second {
		t.Fatalf("internal token must expire within 60s, ttl=%v", ttl)
	}
}

func doRequest(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestFocusPatterns_InjectedDaysCannotChangeUser(t *testing.T) {
	up := &upstreamRecorder{}
	srv := up.server(t)
	r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))

	// days decodes to "30&user_id=2" — the F-01 attack.
	w := doRequest(r, http.MethodGet, "/analytics/focus-patterns?days=30%26user_id%3D2", "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-integer days, got %d: %s", w.Code, w.Body.String())
	}
	if up.calls != 0 {
		t.Fatalf("upstream must not be called for invalid input, got %d calls (url=%v)", up.calls, up.lastURL)
	}
}

func TestFocusPatterns_DaysValidation(t *testing.T) {
	cases := []struct {
		query string
		want  int
	}{
		{"days=6", http.StatusBadRequest},
		{"days=366", http.StatusBadRequest},
		{"days=abc", http.StatusBadRequest},
		{"days=7", http.StatusOK},
		{"days=365", http.StatusOK},
		{"", http.StatusOK}, // defaults to 30
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			up := &upstreamRecorder{}
			srv := up.server(t)
			r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))
			w := doRequest(r, http.MethodGet, "/analytics/focus-patterns?"+tc.query, "")
			if w.Code != tc.want {
				t.Fatalf("query %q: expected %d, got %d: %s", tc.query, tc.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestFocusPatterns_ForwardsOnlyValidatedParams(t *testing.T) {
	up := &upstreamRecorder{}
	srv := up.server(t)
	r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))

	w := doRequest(r, http.MethodGet, "/analytics/focus-patterns?days=14&user_id=2&extra=x", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertForwardedUser(t, up, "1")
	q := up.lastURL.Query()
	if got := q["days"]; len(got) != 1 || got[0] != "14" {
		t.Fatalf("upstream days must be [14], got %v", got)
	}
	if _, ok := q["extra"]; ok {
		t.Fatalf("client query params must not be forwarded, got %v", up.lastURL.RawQuery)
	}
}

func TestGetAnalytics_RangeValidation(t *testing.T) {
	cases := []struct {
		query    string
		want     int
		wantPath string
	}{
		{"range=week", http.StatusOK, "/analytics/weekly"},
		{"range=month", http.StatusOK, "/analytics/monthly"},
		{"", http.StatusOK, "/analytics/weekly"},
		{"range=year", http.StatusBadRequest, ""},
		{"range=month%26user_id%3D2", http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			up := &upstreamRecorder{}
			srv := up.server(t)
			r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))
			w := doRequest(r, http.MethodGet, "/analytics?"+tc.query+"&user_id=2", "")
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
			if tc.wantPath == "" {
				if up.calls != 0 {
					t.Fatalf("upstream must not be called, got %d", up.calls)
				}
				return
			}
			if up.lastURL.Path != tc.wantPath {
				t.Fatalf("expected upstream path %s, got %s", tc.wantPath, up.lastURL.Path)
			}
			assertForwardedUser(t, up, "1")
		})
	}
}

const validSchedule = `{
  "commitments": [
    {"id": "1", "name": "Sleep", "startTime": "23:00", "endTime": "07:00",
     "days": ["Monday","Tuesday","Wednesday","Thursday","Friday","Saturday","Sunday"]}
  ],
  "goals": {"deepWorkHours": 15, "studyHours": 10, "restHours": 7}
}`

func TestSmartSchedule_ValidBodyIsReencoded(t *testing.T) {
	up := &upstreamRecorder{}
	srv := up.server(t)
	r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))

	w := doRequest(r, http.MethodPost, "/analytics/smart-schedule?user_id=2", validSchedule)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertForwardedUser(t, up, "1")
	var fwd map[string]json.RawMessage
	if err := json.Unmarshal(up.lastBody, &fwd); err != nil {
		t.Fatalf("forwarded body is not JSON: %v", err)
	}
	if len(fwd) != 2 || fwd["commitments"] == nil || fwd["goals"] == nil {
		t.Fatalf("forwarded body must contain only commitments and goals, got %s", up.lastBody)
	}
}

// Structural problems are rejected by Gin itself and never reach the analytics service.
func TestSmartSchedule_RejectsMalformedBodies(t *testing.T) {
	cases := map[string]string{
		"unknown top-level field": `{"commitments": [], "goals": {}, "user_id": 2}`,
		"unknown nested field":    `{"commitments": [{"id":"1","name":"X","startTime":"09:00","endTime":"10:00","days":["Monday"],"x":1}], "goals": {}}`,
		"long name":               `{"commitments": [{"id":"1","name":"` + strings.Repeat("a", 101) + `","startTime":"09:00","endTime":"10:00","days":["Monday"]}], "goals": {}}`,
		"too many days":           `{"commitments": [{"id":"1","name":"X","startTime":"09:00","endTime":"10:00","days":["Monday","Monday","Monday","Monday","Monday","Monday","Monday","Monday"]}], "goals": {}}`,
		"wrong type":              `{"commitments": [{"id":"1","name":"X","startTime":900,"endTime":"10:00","days":["Monday"]}], "goals": {}}`,
		"not json":                `user_id=2`,
		"trailing data":           `{"commitments": [], "goals": {}} {"x":1}`,
		"goal overflow":           `{"commitments": [], "goals": {"studyHours": 1e309}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			up := &upstreamRecorder{}
			srv := up.server(t)
			r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))
			w := doRequest(r, http.MethodPost, "/analytics/smart-schedule", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if up.calls != 0 {
				t.Fatalf("upstream must not be called, got %d", up.calls)
			}
		})
	}
}

// Semantic rules (time format, end != start, day names, goal range/steps) live in the
// analytics service; Gin relays its 422 and message unchanged.
func TestSmartSchedule_PassesThroughValidation422(t *testing.T) {
	const msg = "commitments[0].startTime: must be HH:MM between 00:00 and 24:00"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"` + msg + `","details":[{"field":"commitments[0].startTime","message":"must be HH:MM between 00:00 and 24:00"}]}`))
	}))
	t.Cleanup(srv.Close)
	r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))

	for _, body := range []string{
		`{"commitments": [{"id":"1","name":"X","startTime":"9am","endTime":"10:00","days":["Monday"]}], "goals": {}}`,
		`{"commitments": [{"id":"1","name":"X","startTime":"09:00","endTime":"10:00","days":["monday"]}], "goals": {}}`,
		`{"commitments": [], "goals": {"studyHours": 0.3}}`,
		`{"commitments": []}`,
	} {
		w := doRequest(r, http.MethodPost, "/analytics/smart-schedule", body)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected the upstream 422 to be relayed, got %d: %s", w.Code, w.Body.String())
		}
		var got struct {
			Error   string           `json:"error"`
			Details []map[string]any `json:"details"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Error != msg || len(got.Details) != 1 {
			t.Fatalf("422 body must carry the analytics service's message, got %s", w.Body.String())
		}
	}
}

func TestSmartSchedule_TooManyCommitments(t *testing.T) {
	up := &upstreamRecorder{}
	srv := up.server(t)
	r := analyticsRouter(newTestAnalyticsHandler(t, srv.URL))

	one := `{"id":"1","name":"X","startTime":"09:00","endTime":"10:00","days":["Monday"]}`
	items := make([]string, 51)
	for i := range items {
		items[i] = one
	}
	body := `{"commitments": [` + strings.Join(items, ",") + `], "goals": {}}`
	w := doRequest(r, http.MethodPost, "/analytics/smart-schedule", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for 51 commitments, got %d", w.Code)
	}
}

func TestAnalyticsProxy_UpstreamTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	h := NewAnalyticsHandler(srv.URL, testInternalSecret)
	h.client = &http.Client{Timeout: 100 * time.Millisecond}
	start := time.Now()
	w := doRequest(analyticsRouter(h), http.MethodGet, "/analytics/focus-patterns", "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on upstream timeout, got %d", w.Code)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("handler must not wait past the client timeout")
	}
}

func TestAnalyticsProxy_DefaultClientHasTimeout(t *testing.T) {
	h := NewAnalyticsHandler("http://example.invalid", testInternalSecret)
	if h.client == nil || h.client.Timeout != 10*time.Second {
		t.Fatalf("expected the shared client with a 10s timeout, got %+v", h.client)
	}
	if NewAnalyticsHandler("http://x", testInternalSecret).client != h.client {
		t.Fatal("handlers must share one http.Client")
	}
}

func TestAnalyticsProxy_OversizeUpstreamResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pad":"` + strings.Repeat("a", 2048) + `"}`))
	}))
	t.Cleanup(srv.Close)

	h := NewAnalyticsHandler(srv.URL, testInternalSecret)
	h.maxResponseBytes = 1024
	w := doRequest(analyticsRouter(h), http.MethodGet, "/analytics", "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for an oversize upstream response, got %d", w.Code)
	}
}

func TestInternalToken_CarriesUserTimezone(t *testing.T) {
	up := &upstreamRecorder{}
	srv := up.server(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", uint(1)); c.Set("userTimezone", "Asia/Tashkent") })
	r.GET("/analytics", NewAnalyticsHandler(srv.URL, testInternalSecret).GetAnalytics)
	if w := doRequest(r, http.MethodGet, "/analytics", ""); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	raw := strings.TrimPrefix(up.lastAuth, "Bearer ")
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (interface{}, error) {
		return []byte(testInternalSecret), nil
	}, jwt.WithAudience("discipline-py")); err != nil {
		t.Fatal(err)
	}
	if claims["tz"] != "Asia/Tashkent" {
		t.Fatalf("internal token must carry tz claim, got %v", claims["tz"])
	}
}

func doRequestWithHeader(r http.Handler, method, target, body, key, value string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(key, value)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDashboardToday_ProxiesToPythonTodayWithUserToken(t *testing.T) {
	up := &upstreamRecorder{}
	srv := up.server(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", uint(1)); c.Set("userTimezone", "Asia/Tashkent") })
	r.GET("/dashboard/today", NewAnalyticsHandler(srv.URL, testInternalSecret).GetToday)

	w := doRequest(r, http.MethodGet, "/dashboard/today?user_id=2&date=2026-01-01", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if up.lastURL.Path != "/analytics/today" || up.lastURL.RawQuery != "" {
		t.Fatalf("must call /analytics/today with no client params, got %s?%s", up.lastURL.Path, up.lastURL.RawQuery)
	}
	assertForwardedUser(t, up, "1")
}

func TestAnalyticsProxy_ForwardsRequestID(t *testing.T) {
	up := &upstreamRecorder{}
	srv := up.server(t)
	h := newTestAnalyticsHandler(t, srv.URL)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Request = c.Request.WithContext(logging.WithRequestID(c.Request.Context(), "rid-0123456789"))
	})
	r.GET("/analytics", h.GetAnalytics)
	if w := doRequest(r, http.MethodGet, "/analytics?range=week", ""); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if up.lastRID != "rid-0123456789" {
		t.Fatalf("the request ID must reach the analytics service, got %q", up.lastRID)
	}
}
