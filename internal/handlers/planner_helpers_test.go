package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// doRequestAs sends a request as user "1" (default) or "2".
func doRequestAs(e *taskEnv, method, target, body, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func codeOf(body []byte) string {
	var b struct{ Code string }
	_ = json.Unmarshal(body, &b)
	return b.Code
}

func mustExecDB(t *testing.T, e *taskEnv, sql string, args ...any) {
	t.Helper()
	if err := e.db.Exec(sql, args...).Error; err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
