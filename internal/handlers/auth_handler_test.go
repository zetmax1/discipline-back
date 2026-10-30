package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/models"
	"discipline-backend/internal/ratelimit"
	"discipline-backend/internal/services"
)

// fakeAuthService accepts exactly one email/password pair.
type fakeAuthService struct {
	email, password string
	loginCalls      int
}

func (f *fakeAuthService) Register(name, email, password string) (*models.User, string, error) {
	return &models.User{ID: 1, Name: name, Email: email}, "token", nil
}
func (f *fakeAuthService) RegisterAdmin(name, email, password string) (*models.User, string, error) {
	return f.Register(name, email, password)
}
func (f *fakeAuthService) Login(email, password string) (*models.User, string, error) {
	f.loginCalls++
	if email == f.email && password == f.password {
		return &models.User{ID: 1, Email: email}, "token", nil
	}
	return nil, "", services.ErrInvalidCredentials
}
func (f *fakeAuthService) GetUserByID(id uint) (*models.User, error) {
	return &models.User{ID: id, Email: f.email}, nil
}
func (f *fakeAuthService) UpdateProfile(id uint, name, email, oldPassword, newPassword, timezone string) (*models.User, string, error) {
	return &models.User{ID: id, Name: name, Email: email}, "", nil
}
func (f *fakeAuthService) Logout(userID uint) error { return nil }

func authRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/auth/register", h.Register)
	r.POST("/auth/login", h.Login)
	authed := r.Group("", func(c *gin.Context) { c.Set("userID", uint(1)) })
	authed.PUT("/me", h.UpdateProfile)
	return r
}

func loginBody(email, password string) string {
	return `{"email":"` + email + `","password":"` + password + `"}`
}

func loginBodyWithCaptcha(email, password, token string) string {
	return `{"email":"` + email + `","password":"` + password + `","turnstileToken":"` + token + `"}`
}

// fakeVerifier accepts the token "ok"; err simulates Cloudflare being unreachable.
type fakeVerifier struct {
	err   error
	calls int
}

func (f *fakeVerifier) Verify(_ context.Context, token string) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return token == "ok", nil
}

func newLoginEnv() (*fakeAuthService, *fakeVerifier, *gin.Engine) {
	svc := &fakeAuthService{email: "user@example.com", password: "correct-password"}
	v := &fakeVerifier{}
	return svc, v, authRouter(NewAuthHandler(svc, services.NewLoginThrottle(ratelimit.NewMemoryStore()), v))
}

func loginFrom(r http.Handler, ip, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	req.RemoteAddr = ip + ":1000"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func captchaRequired(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	var body struct {
		CaptchaRequired *bool `json:"captchaRequired"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.CaptchaRequired == nil {
		t.Fatalf("response must carry captchaRequired: %d %s", w.Code, w.Body.String())
	}
	return *body.CaptchaRequired
}

// downStore fails every call, like an unreachable Redis.
type downStore struct{}

var errStoreDown = errors.New("redis down")

func (downStore) Hit(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errStoreDown
}
func (downStore) Record(context.Context, string, time.Duration) error { return errStoreDown }
func (downStore) Events(context.Context, string, time.Duration) ([]time.Time, error) {
	return nil, errStoreDown
}
func (downStore) Clear(context.Context, string) error { return errStoreDown }
func (downStore) Ping(context.Context) error          { return errStoreDown }

func TestLogin_ThrottleStoreDownIs503(t *testing.T) {
	svc := &fakeAuthService{email: "user@example.com", password: "correct-password"}
	r := authRouter(NewAuthHandler(svc, services.NewLoginThrottle(downStore{}), &fakeVerifier{}))
	w := doRequest(r, http.MethodPost, "/auth/login", loginBody("user@example.com", "correct-password"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("login must fail closed while the throttle store is down, got %d", w.Code)
	}
	if svc.loginCalls != 0 {
		t.Fatal("no password check may run without throttling")
	}
}

func TestLogin_NoHardLockoutForTheOwner(t *testing.T) {
	_, _, r := newLoginEnv()
	for i := 0; i < 10; i++ { // attacker on one IP
		loginFrom(r, "203.0.113.66", loginBodyWithCaptcha("user@example.com", "wrong", "ok"))
	}
	w := loginFrom(r, "198.51.100.5", loginBodyWithCaptcha("user@example.com", "correct-password", "ok"))
	if w.Code != http.StatusOK {
		t.Fatalf("the owner on another IP must still log in (with a captcha), got %d %s", w.Code, w.Body.String())
	}
}

func TestLogin_CaptchaRequiredAfterThreeFailures(t *testing.T) {
	svc, v, r := newLoginEnv()
	for i := 0; i < 3; i++ {
		w := loginFrom(r, "203.0.113.1", loginBody("user@example.com", "wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, w.Code)
		}
		if got, want := captchaRequired(t, w), i == 2; got != want {
			t.Fatalf("attempt %d: captchaRequired=%v, want %v", i+1, got, want)
		}
	}
	if v.calls != 0 {
		t.Fatal("no captcha check before it is required")
	}

	calls := svc.loginCalls
	w := loginFrom(r, "198.51.100.5", loginBody("user@example.com", "correct-password"))
	if w.Code != http.StatusUnauthorized || !captchaRequired(t, w) {
		t.Fatalf("missing token must be 401 captchaRequired, got %d %s", w.Code, w.Body.String())
	}
	w = loginFrom(r, "198.51.100.5", loginBodyWithCaptcha("user@example.com", "correct-password", "forged"))
	if w.Code != http.StatusUnauthorized || !captchaRequired(t, w) {
		t.Fatalf("invalid token must be 401 captchaRequired, got %d", w.Code)
	}
	if svc.loginCalls != calls {
		t.Fatal("without a valid captcha the password must not be checked")
	}
	if w := loginFrom(r, "198.51.100.5", loginBodyWithCaptcha("user@example.com", "correct-password", "ok")); w.Code != http.StatusOK {
		t.Fatalf("valid captcha + right password must log in, got %d", w.Code)
	}
}

func TestLogin_UnknownEmailIsTreatedTheSame(t *testing.T) {
	_, _, r := newLoginEnv()
	for i := 0; i < 3; i++ {
		loginFrom(r, "203.0.113.1", loginBody("ghost@example.com", "x"))
	}
	if w := loginFrom(r, "203.0.113.2", loginBody("ghost@example.com", "x")); w.Code != http.StatusUnauthorized || !captchaRequired(t, w) {
		t.Fatalf("unknown emails must need a captcha too, got %d", w.Code)
	}
}

func TestLogin_ProgressiveDelayIs429WithRetryAfter(t *testing.T) {
	svc, _, r := newLoginEnv()
	for i := 0; i < 4; i++ {
		loginFrom(r, "203.0.113.1", loginBodyWithCaptcha("user@example.com", "wrong", "ok"))
	}
	calls := svc.loginCalls
	w := loginFrom(r, "203.0.113.1", loginBodyWithCaptcha("user@example.com", "correct-password", "ok"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after the 4th failure the same (email, IP) must wait, got %d", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra != "2" {
		t.Fatalf("Retry-After must be 2 seconds after the 4th failure, got %q", ra)
	}
	if svc.loginCalls != calls {
		t.Fatal("a delayed attempt must not reach the password check")
	}
}

func TestLogin_SuccessClearsTheDelay(t *testing.T) {
	_, _, r := newLoginEnv()
	for i := 0; i < 3; i++ {
		loginFrom(r, "203.0.113.1", loginBodyWithCaptcha("user@example.com", "wrong", "ok"))
	}
	if w := loginFrom(r, "203.0.113.1", loginBodyWithCaptcha("user@example.com", "correct-password", "ok")); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	for i := 0; i < 3; i++ {
		loginFrom(r, "203.0.113.1", loginBodyWithCaptcha("user@example.com", "wrong", "ok"))
	}
	if w := loginFrom(r, "203.0.113.1", loginBodyWithCaptcha("user@example.com", "correct-password", "ok")); w.Code != http.StatusOK {
		t.Fatalf("the earlier success must have cleared the (email, IP) count, got %d", w.Code)
	}
}

func TestLogin_CaptchaServiceDownIs503(t *testing.T) {
	svc, v, r := newLoginEnv()
	for i := 0; i < 3; i++ {
		loginFrom(r, "203.0.113.1", loginBody("user@example.com", "wrong"))
	}
	v.err = errors.New("cloudflare unreachable")
	calls := svc.loginCalls
	if w := loginFrom(r, "203.0.113.1", loginBodyWithCaptcha("user@example.com", "correct-password", "ok")); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unverifiable captcha must fail closed with 503, got %d", w.Code)
	}
	if svc.loginCalls != calls {
		t.Fatal("no password check without a verified captcha")
	}
}

func TestRegister_RequiresAValidCaptcha(t *testing.T) {
	v := &fakeVerifier{}
	r := authRouter(NewAuthHandler(services.NewAuthService(newHandlerUserRepo(), "jwt-secret-for-tests-0123456789abcdef"),
		services.NewLoginThrottle(ratelimit.NewMemoryStore()), v))
	for _, body := range []string{
		`{"name":"Ann","email":"ann@example.com","password":"1234567890"}`,
		`{"name":"Ann","email":"ann@example.com","password":"1234567890","turnstileToken":"forged"}`,
	} {
		w := doRequest(r, http.MethodPost, "/auth/register", body)
		if w.Code != http.StatusBadRequest || !captchaRequired(t, w) {
			t.Fatalf("registration without a valid captcha must be 400 captchaRequired, got %d %s", w.Code, w.Body.String())
		}
	}
	v.err = errors.New("cloudflare unreachable")
	if w := doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890","turnstileToken":"ok"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unverifiable captcha must fail closed with 503, got %d", w.Code)
	}
	v.err = nil
	if w := doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890","turnstileToken":"ok"}`); w.Code != http.StatusCreated {
		t.Fatalf("valid captcha must register, got %d %s", w.Code, w.Body.String())
	}
}

func realAuthRouter(t *testing.T) *gin.Engine {
	t.Helper()
	return authRouter(NewAuthHandler(services.NewAuthService(newHandlerUserRepo(), "jwt-secret-for-tests-0123456789abcdef"), services.NewLoginThrottle(ratelimit.NewMemoryStore()), &fakeVerifier{}))
}

func TestRegister_WeakPasswordIs400AndDuplicateIs409(t *testing.T) {
	r := realAuthRouter(t)
	w := doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"123456789","turnstileToken":"ok"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("weak password must be 400, got %d: %s", w.Code, w.Body.String())
	}
	w = doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890","turnstileToken":"ok"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	w = doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890","turnstileToken":"ok"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate email must stay 409, got %d", w.Code)
	}
}

func TestUpdateProfile_WeakNewPasswordIs400(t *testing.T) {
	r := realAuthRouter(t)
	doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890","turnstileToken":"ok"}`)
	w := doRequest(r, http.MethodPut, "/me", `{"oldPassword":"1234567890","newPassword":"short"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func registerAnn(t *testing.T, r *gin.Engine) {
	t.Helper()
	w := doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890","turnstileToken":"ok"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup register failed: %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateProfile_EmailChangeRequiresCurrentPassword(t *testing.T) {
	r := realAuthRouter(t)
	registerAnn(t, r)

	if w := doRequest(r, http.MethodPut, "/me", `{"email":"new@example.com"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("email change without password must be 400, got %d", w.Code)
	}
	if w := doRequest(r, http.MethodPut, "/me", `{"email":"new@example.com","oldPassword":"wrong-password"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("email change with wrong password must be 400, got %d", w.Code)
	}
	w := doRequest(r, http.MethodPut, "/me", `{"email":"new@example.com","oldPassword":"1234567890"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "new@example.com") {
		t.Fatalf("email change with correct password must succeed, got %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateProfile_RejectsInvalidEmail(t *testing.T) {
	r := realAuthRouter(t)
	registerAnn(t, r)
	w := doRequest(r, http.MethodPut, "/me", `{"email":"x","oldPassword":"1234567890"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid email must be 400, got %d", w.Code)
	}
}

func TestUpdateProfile_NameLength(t *testing.T) {
	r := realAuthRouter(t)
	registerAnn(t, r)
	if w := doRequest(r, http.MethodPut, "/me", `{"name":"`+strings.Repeat("a", 101)+`"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("101-char name must be 400, got %d", w.Code)
	}
	if w := doRequest(r, http.MethodPut, "/me", `{"name":"`+strings.Repeat("a", 100)+`"}`); w.Code != http.StatusOK {
		t.Fatalf("100-char name must be accepted, got %d", w.Code)
	}
}

func TestUpdateProfile_NameOnlyNeedsNoPassword(t *testing.T) {
	r := realAuthRouter(t)
	registerAnn(t, r)
	if w := doRequest(r, http.MethodPut, "/me", `{"name":"Annie"}`); w.Code != http.StatusOK {
		t.Fatalf("name-only change must not require a password, got %d", w.Code)
	}
}
