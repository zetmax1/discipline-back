package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func clientIPFor(t *testing.T, proxies []string, header string, remote string, headers map[string]string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := ConfigureClientIP(r, proxies, header); err != nil {
		t.Fatalf("ConfigureClientIP: %v", err)
	}
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func TestClientIP_NoTrustedProxiesIgnoresForwardedHeaders(t *testing.T) {
	got := clientIPFor(t, nil, "", "10.0.0.5:1234", map[string]string{
		"X-Forwarded-For": "6.6.6.1, 203.0.113.9",
		"X-Real-IP":       "6.6.6.2",
	})
	if got != "10.0.0.5" {
		t.Fatalf("with no trusted proxies ClientIP must be the socket peer, got %s", got)
	}
}

func TestClientIP_TrustedProxyUsesRightmostUntrustedHop(t *testing.T) {
	got := clientIPFor(t, []string{"10.0.0.0/8"}, "", "10.0.0.5:1234", map[string]string{
		"X-Forwarded-For": "6.6.6.1, 203.0.113.9",
	})
	if got != "203.0.113.9" {
		t.Fatalf("spoofed left-most XFF must be ignored, got %s", got)
	}
}

func TestClientIP_UntrustedPeerCannotSpoof(t *testing.T) {
	got := clientIPFor(t, []string{"10.0.0.0/8"}, "", "198.51.100.7:1234", map[string]string{
		"X-Forwarded-For": "6.6.6.1",
	})
	if got != "198.51.100.7" {
		t.Fatalf("an untrusted peer's XFF must be ignored, got %s", got)
	}
}

func TestClientIP_TrustedPlatformHeader(t *testing.T) {
	got := clientIPFor(t, nil, "X-Real-IP", "10.0.0.5:1234", map[string]string{
		"X-Real-IP":       "203.0.113.9",
		"X-Forwarded-For": "6.6.6.1",
	})
	if got != "203.0.113.9" {
		t.Fatalf("TRUSTED_IP_HEADER must be used, got %s", got)
	}
}

func TestConfigureClientIP_RejectsInvalidProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := ConfigureClientIP(gin.New(), []string{"not-a-cidr"}, ""); err == nil {
		t.Fatal("expected an error for an invalid trusted proxy")
	}
}
