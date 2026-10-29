package main

import (
	"net/http"
	"testing"

	"discipline-backend/internal/handlers"
)

func TestNewHTTPServer_SetsTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("all server timeouts must be set: %+v", srv)
	}
	if srv.WriteTimeout <= handlers.AnalyticsClientTimeout {
		t.Fatal("write timeout must exceed the analytics upstream timeout")
	}
}
