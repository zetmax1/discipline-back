package handlers

import (
	"net/http"
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
