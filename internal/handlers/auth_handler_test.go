package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/models"
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
func (f *fakeAuthService) UpdateProfile(id uint, name, email, oldPassword, newPassword string) (*models.User, error) {
	return &models.User{ID: id, Name: name, Email: email}, nil
}

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

func TestLogin_LocksEmailAfterFiveFailures(t *testing.T) {
	svc := &fakeAuthService{email: "user@example.com", password: "correct-password"}
	r := authRouter(NewAuthHandler(svc, services.NewLoginThrottle(1000)))

	for i := 0; i < 5; i++ {
		w := doRequest(r, http.MethodPost, "/auth/login", loginBody("user@example.com", "wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, w.Code)
		}
	}
	callsBefore := svc.loginCalls
	w := doRequest(r, http.MethodPost, "/auth/login", loginBody("user@example.com", "correct-password"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked email must get 429 even with the right password, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("429 must include Retry-After")
	}
	if svc.loginCalls != callsBefore {
		t.Fatal("a locked email must not reach the password check")
	}

	other := doRequest(r, http.MethodPost, "/auth/login", loginBody("other@example.com", "x"))
	if other.Code != http.StatusUnauthorized {
		t.Fatalf("other emails must not be locked, got %d", other.Code)
	}
}

func TestLogin_UnknownEmailIsThrottledToo(t *testing.T) {
	svc := &fakeAuthService{email: "user@example.com", password: "correct-password"}
	r := authRouter(NewAuthHandler(svc, services.NewLoginThrottle(1000)))
	for i := 0; i < 5; i++ {
		doRequest(r, http.MethodPost, "/auth/login", loginBody("ghost@example.com", "x"))
	}
	w := doRequest(r, http.MethodPost, "/auth/login", loginBody("ghost@example.com", "x"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("lockout must behave the same for unknown emails, got %d", w.Code)
	}
}

func TestLogin_SuccessResetsFailures(t *testing.T) {
	svc := &fakeAuthService{email: "user@example.com", password: "correct-password"}
	r := authRouter(NewAuthHandler(svc, services.NewLoginThrottle(1000)))
	for i := 0; i < 4; i++ {
		doRequest(r, http.MethodPost, "/auth/login", loginBody("user@example.com", "wrong"))
	}
	if w := doRequest(r, http.MethodPost, "/auth/login", loginBody("user@example.com", "correct-password")); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	for i := 0; i < 4; i++ {
		doRequest(r, http.MethodPost, "/auth/login", loginBody("user@example.com", "wrong"))
	}
	if w := doRequest(r, http.MethodPost, "/auth/login", loginBody("user@example.com", "correct-password")); w.Code != http.StatusOK {
		t.Fatalf("counter should have been reset by the earlier success, got %d", w.Code)
	}
}

func realAuthRouter(t *testing.T) *gin.Engine {
	t.Helper()
	return authRouter(NewAuthHandler(services.NewAuthService(newHandlerUserRepo(), "jwt-secret-for-tests-0123456789abcdef", 1), services.NewLoginThrottle(100)))
}

func TestRegister_WeakPasswordIs400AndDuplicateIs409(t *testing.T) {
	r := realAuthRouter(t)
	w := doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"123456789"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("weak password must be 400, got %d: %s", w.Code, w.Body.String())
	}
	w = doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	w = doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate email must stay 409, got %d", w.Code)
	}
}

func TestUpdateProfile_WeakNewPasswordIs400(t *testing.T) {
	r := realAuthRouter(t)
	doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890"}`)
	w := doRequest(r, http.MethodPut, "/me", `{"oldPassword":"1234567890","newPassword":"short"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func registerAnn(t *testing.T, r *gin.Engine) {
	t.Helper()
	w := doRequest(r, http.MethodPost, "/auth/register", `{"name":"Ann","email":"ann@example.com","password":"1234567890"}`)
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
