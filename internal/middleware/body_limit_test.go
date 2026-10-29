package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func bodyLimitRouter(read *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(MaxRequestBodyBytes))
	r.POST("/", func(c *gin.Context) {
		b, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		*read = len(b)
		c.Status(http.StatusOK)
	})
	return r
}

func TestBodyLimit_RejectsDeclaredOversizeBody(t *testing.T) {
	read := -1
	r := bodyLimitRouter(&read)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, MaxRequestBodyBytes+1)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
	if read != -1 {
		t.Fatal("handler must not run for an oversize body")
	}
}

func TestBodyLimit_RejectsUndeclaredOversizeBody(t *testing.T) {
	read := -1
	r := bodyLimitRouter(&read)
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(bytes.NewReader(make([]byte, MaxRequestBodyBytes+1))))
	req.ContentLength = -1 // chunked: size unknown up front
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected the read to fail with 413, got %d", w.Code)
	}
}

func TestBodyLimit_AllowsBodyAtLimit(t *testing.T) {
	read := -1
	r := bodyLimitRouter(&read)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, MaxRequestBodyBytes)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || read != MaxRequestBodyBytes {
		t.Fatalf("expected 200 reading %d bytes, got %d reading %d", MaxRequestBodyBytes, w.Code, read)
	}
}
