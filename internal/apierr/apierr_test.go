package apierr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/contracttest"
)

func respond(t *testing.T, h gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w
}

func TestJSON_WritesErrorCodeAndExtras(t *testing.T) {
	w := respond(t, func(c *gin.Context) {
		JSON(c, http.StatusUnauthorized, InvalidCredentials, "invalid email or password", gin.H{"captchaRequired": true})
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "invalid_credentials" || body["error"] != "invalid email or password" || body["captchaRequired"] != true {
		t.Fatalf("unexpected body %v", body)
	}
	contracttest.Validate(t, "errors", "error", w.Body.Bytes())
}

func TestAbort_StopsTheChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) { Abort(c, http.StatusForbidden, Forbidden, "no") }, func(c *gin.Context) {
		t.Fatal("handler after Abort must not run")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestWrap_KeepsTheBaseErrorAndCode(t *testing.T) {
	base := errors.New("invalid task")
	err := fmt.Errorf("tasks[3]: %w", Wrap(base, TaskEndBeforeStart, "endTime must be after startTime"))
	if !errors.Is(err, base) {
		t.Fatal("errors.Is must still find the base error")
	}
	if code, ok := CodeOf(err); !ok || code != TaskEndBeforeStart {
		t.Fatalf("want %s, got %q %v", TaskEndBeforeStart, code, ok)
	}
	if got := err.Error(); got != "tasks[3]: invalid task: endTime must be after startTime" {
		t.Fatalf("message changed: %q", got)
	}
	if _, ok := CodeOf(errors.New("plain")); ok {
		t.Fatal("a plain error has no code")
	}
}

func TestCodes_AreUniqueSnakeCase(t *testing.T) {
	snake := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	seen := map[Code]bool{}
	for _, c := range All {
		if !snake.MatchString(string(c)) || seen[c] {
			t.Errorf("code %q is duplicated or not snake_case", c)
		}
		seen[c] = true
	}
}

// The web translates these codes; its contract lists exactly the codes the API can send.
func TestCodes_MatchTheWebContract(t *testing.T) {
	dir, err := contracttest.Dir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "errors.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Error struct {
			Properties struct {
				Code struct {
					Enum []string `json:"enum"`
				} `json:"code"`
			} `json:"properties"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var ours []string
	for _, c := range All {
		ours = append(ours, string(c))
	}
	theirs := doc.Error.Properties.Code.Enum
	slices.Sort(ours)
	slices.Sort(theirs)
	if !slices.Equal(ours, theirs) {
		t.Fatalf("apierr.All and contracts/errors.schema.json differ:\napi: %v\nweb: %v", ours, theirs)
	}
}

// Every error body goes through this package, so none can leave without a code.
func TestNoErrorBodyWithoutCode(t *testing.T) {
	errorKey := regexp.MustCompile(`"error"\s*:`)
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "docs" || d.Name() == "apierr" || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if errorKey.MatchString(line) {
				offenders = append(offenders, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("write error bodies with apierr.JSON/Abort (they add the code):\n  %s", strings.Join(offenders, "\n  "))
	}
}
