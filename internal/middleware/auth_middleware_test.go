package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const authTestSecret = "jwt-secret-for-tests-0123456789abcdef"

func signedToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(authTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func authRequest(t *testing.T, load UserLoader, token string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	seen := map[string]any{}
	r := gin.New()
	r.Use(AuthMiddleware(authTestSecret, load))
	r.GET("/x", func(c *gin.Context) {
		seen["userID"], _ = c.Get("userID")
		seen["userRole"], _ = c.Get("userRole")
		seen["userTimezone"], _ = c.Get("userTimezone")
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, seen
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{"user_id": 7, "role": "admin", "exp": time.Now().Add(time.Hour).Unix()}
}

func TestAuthMiddleware_LoadsTimezoneAndRoleFromDB(t *testing.T) {
	load := func(id uint) (*AuthUser, error) {
		if id != 7 {
			t.Fatalf("loader called with %d", id)
		}
		return &AuthUser{Role: "user", Timezone: "Asia/Tokyo"}, nil
	}
	w, seen := authRequest(t, load, signedToken(t, validClaims()))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if seen["userTimezone"] != "Asia/Tokyo" {
		t.Fatalf("userTimezone = %v", seen["userTimezone"])
	}
	if seen["userRole"] != "user" {
		t.Fatalf("role must come from the database, not the token; got %v", seen["userRole"])
	}
}

func TestAuthMiddleware_DeletedUserRejected(t *testing.T) {
	load := func(uint) (*AuthUser, error) { return nil, gorm.ErrRecordNotFound }
	if w, _ := authRequest(t, load, signedToken(t, validClaims())); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a deleted user, got %d", w.Code)
	}
}

func TestAuthMiddleware_LoaderFailureIs500(t *testing.T) {
	load := func(uint) (*AuthUser, error) { return nil, errors.New("db down") }
	if w, _ := authRequest(t, load, signedToken(t, validClaims())); w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the user cannot be loaded, got %d", w.Code)
	}
}
