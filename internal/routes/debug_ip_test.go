package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func debugIPRequest(r http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/debug/ip", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	req.Header.Add("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	req.Header.Add("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "198.51.100.7")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDebugIP_HiddenUnlessEnabled(t *testing.T) {
	r := newTestRouter(false)
	SetupIPDebug(r, false)
	if w := debugIPRequest(r); w.Code != http.StatusNotFound {
		t.Fatalf("/debug/ip must not exist unless ENABLE_IP_DEBUG=true, got %d", w.Code)
	}
}

func TestDebugIP_ReturnsRawHeadersAndRemoteAddr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := newTestRouter(false)
	SetupIPDebug(r, true)
	w := debugIPRequest(r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("debug output must not be cached, got %q", cc)
	}
	var body struct {
		XForwardedFor []string `json:"xForwardedFor"`
		XRealIP       []string `json:"xRealIp"`
		RemoteAddr    string   `json:"remoteAddr"`
		ClientIP      string   `json:"clientIp"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.XForwardedFor) != 2 || body.XForwardedFor[0] != "198.51.100.7, 10.0.0.1" || body.XForwardedFor[1] != "203.0.113.9" {
		t.Errorf("X-Forwarded-For must be returned raw, every occurrence: %v", body.XForwardedFor)
	}
	if len(body.XRealIP) != 1 || body.XRealIP[0] != "198.51.100.7" {
		t.Errorf("X-Real-IP: %v", body.XRealIP)
	}
	if body.RemoteAddr != "10.1.2.3:4567" {
		t.Errorf("RemoteAddr: %q", body.RemoteAddr)
	}
	if body.ClientIP == "" {
		t.Error("clientIp (what the rate limiter uses) must be reported")
	}
}
