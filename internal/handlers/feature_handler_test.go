package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/contracttest"
)

func getFeatures(t *testing.T, enabled []string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/features", NewFeatureHandler(enabled).List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/features", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	return w
}

func TestFeatures_ListsEnabledFlags(t *testing.T) {
	w := getFeatures(t, []string{"F1", "F12A"})
	if got := w.Body.String(); got != `{"features":["F1","F12A"]}` {
		t.Fatalf("unexpected body %s", got)
	}
	contracttest.Validate(t, "features", "features", w.Body.Bytes())
}

func TestFeatures_NoneIsAnEmptyList(t *testing.T) {
	w := getFeatures(t, nil)
	if got := w.Body.String(); got != `{"features":[]}` {
		t.Fatalf("want an empty list, not null: %s", got)
	}
	contracttest.Validate(t, "features", "features", w.Body.Bytes())
}
