package captcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// siteverify fakes Cloudflare's endpoint: it accepts "good-token" for "test-secret".
func siteverify(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("siteverify must be a POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("secret") == "test-secret" && r.PostForm.Get("response") == "good-token" {
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTurnstile_AcceptsOnlyTokensCloudflareConfirms(t *testing.T) {
	var calls atomic.Int32
	srv := siteverify(t, &calls)
	v := NewTurnstile("test-secret")
	v.verifyURL = srv.URL

	ok, err := v.Verify(context.Background(), "good-token")
	if err != nil || !ok {
		t.Fatalf("good token: ok=%v err=%v", ok, err)
	}
	ok, err = v.Verify(context.Background(), "forged")
	if err != nil || ok {
		t.Fatalf("bad token must be rejected without an error, ok=%v err=%v", ok, err)
	}
}

func TestTurnstile_EmptyTokenIsRejectedWithoutACall(t *testing.T) {
	var calls atomic.Int32
	srv := siteverify(t, &calls)
	v := NewTurnstile("test-secret")
	v.verifyURL = srv.URL
	if ok, err := v.Verify(context.Background(), "  "); ok || err != nil {
		t.Fatalf("empty token: ok=%v err=%v", ok, err)
	}
	if calls.Load() != 0 {
		t.Fatal("an empty token must not reach Cloudflare")
	}
}

func TestTurnstile_UpstreamFailureIsAnError(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer down.Close()
	v := NewTurnstile("test-secret")
	v.verifyURL = down.URL
	if ok, err := v.Verify(context.Background(), "good-token"); ok || err == nil {
		t.Fatalf("a 502 from siteverify must be an error (callers fail closed), ok=%v err=%v", ok, err)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer slow.Close()
	v = NewTurnstile("test-secret")
	v.verifyURL = slow.URL
	v.client.Timeout = 50 * time.Millisecond
	if ok, err := v.Verify(context.Background(), "good-token"); ok || err == nil {
		t.Fatalf("a timeout must be an error, ok=%v err=%v", ok, err)
	}
}
