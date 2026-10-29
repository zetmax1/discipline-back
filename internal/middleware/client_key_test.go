package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func keyFor(t *testing.T, authorization string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	_ = ConfigureClientIP(r, nil, "")
	var got string
	r.GET("/", func(c *gin.Context) { got = ByUserOrIP(authTestSecret)(c) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:1000"
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestByUserOrIP(t *testing.T) {
	valid := signedToken(t, jwt.MapClaims{"user_id": 42, "tv": 0, "exp": time.Now().Add(time.Hour).Unix()})
	expired := signedToken(t, jwt.MapClaims{"user_id": 42, "tv": 0, "exp": time.Now().Add(-time.Hour).Unix()})
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": 42, "exp": time.Now().Add(time.Hour).Unix()}).
		SignedString([]byte("some-other-secret-0123456789abcdef"))
	noUser := signedToken(t, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})

	cases := map[string]string{
		"Bearer " + valid:   "user:42",
		"bearer " + valid:   "user:42",
		"":                  "ip:203.0.113.7",
		"Bearer " + expired: "ip:203.0.113.7",
		"Bearer " + forged:  "ip:203.0.113.7",
		"Bearer " + noUser:  "ip:203.0.113.7",
		"Basic abc":         "ip:203.0.113.7",
	}
	for header, want := range cases {
		if got := keyFor(t, header); got != want {
			t.Errorf("Authorization %.20q: key %q, want %q", header, got, want)
		}
	}
}
