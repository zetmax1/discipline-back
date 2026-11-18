package routes

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The generated Swagger (docs/, `swag init`) must describe exactly the /api routes
// the router serves. Regenerate after changing a route or its annotations:
//
//	go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/server/main.go -o docs
func TestSwagger_MatchesTheRouter(t *testing.T) {
	raw, err := os.ReadFile("../../docs/swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		BasePath    string                                `json:"basePath"`
		Paths       map[string]map[string]json.RawMessage `json:"paths"`
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	param := regexp.MustCompile(`:([A-Za-z]+)`)
	var served []string
	for _, rt := range newTestRouter(false).Routes() {
		if !strings.HasPrefix(rt.Path, doc.BasePath+"/") {
			continue
		}
		p := param.ReplaceAllString(strings.TrimPrefix(rt.Path, doc.BasePath), "{$1}")
		served = append(served, strings.ToLower(rt.Method)+" "+p)
	}
	var documented []string
	for p, methods := range doc.Paths {
		for m := range methods {
			documented = append(documented, m+" "+p)
		}
	}
	sort.Strings(served)
	sort.Strings(documented)
	if strings.Join(served, "\n") != strings.Join(documented, "\n") {
		t.Fatalf("docs/swagger.json is stale.\nserved:\n  %s\ndocumented:\n  %s",
			strings.Join(served, "\n  "), strings.Join(documented, "\n  "))
	}

	for def, field := range map[string]string{
		"handlers.LoginRequest":       "turnstileToken",
		"handlers.RegisterRequest":    "turnstileToken",
		"handlers.LoginErrorResponse": "captchaRequired",
	} {
		if _, ok := doc.Definitions[def].Properties[field]; !ok {
			t.Errorf("docs/swagger.json: %s lacks %s", def, field)
		}
	}
}
