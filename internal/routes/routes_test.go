package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/handlers"
)

func newTestRouter(enableDocs bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetupRoutes(r, &handlers.AuthHandler{}, &handlers.TaskHandler{}, &handlers.QuoteHandler{}, &handlers.VocabHandler{},
		"jwt-secret-for-tests-0123456789abcdef", "https://app.example.com", "http://py.invalid",
		"internal-secret-for-tests-0123456789", enableDocs)
	return r
}

func TestSwaggerHiddenByDefault(t *testing.T) {
	w := httptest.NewRecorder()
	newTestRouter(false).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for swagger when docs are disabled, got %d", w.Code)
	}
}

func TestSwaggerServedWhenEnabled(t *testing.T) {
	w := httptest.NewRecorder()
	newTestRouter(true).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for swagger when docs are enabled, got %d", w.Code)
	}
}
