package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"discipline-backend/internal/contracttest"
)

// An offline client replays its queue after a lost response: a create sent twice with the same
// Idempotency-Key must create exactly one row and answer the first result both times.

func postWithKey(e *taskEnv, target, body, key, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func idOf(t *testing.T, w *httptest.ResponseRecorder, field string) uint {
	t.Helper()
	var body map[string]json.RawMessage
	var item struct{ ID uint }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: %v", w.Body.String(), err)
	}
	if err := json.Unmarshal(body[field], &item); err != nil {
		t.Fatalf("%s: %v", w.Body.String(), err)
	}
	return item.ID
}

func countRows(t *testing.T, e *taskEnv, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := e.db.Raw(sql, args...).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIdempotentCreate_ReplayedBlockCreatesOneRow(t *testing.T) {
	e := newTaskEnv(t)
	body := taskJSON("Replayed", "09:00", "10:00", "2026-11-30")
	first := postWithKey(e, "/tasks", body, "block-key-0001", "")
	again := postWithKey(e, "/tasks", body, "block-key-0001", "")
	if first.Code != http.StatusCreated || again.Code != http.StatusCreated {
		t.Fatalf("both answer 201: %d %s / %d %s", first.Code, first.Body.String(), again.Code, again.Body.String())
	}
	if a, b := idOf(t, first, "task"), idOf(t, again, "task"); a == 0 || a != b {
		t.Fatalf("the replay returns the first block: %d vs %d", a, b)
	}
	if n := countRows(t, e, `SELECT count(*) FROM tasks WHERE name = 'Replayed'`); n != 1 {
		t.Fatalf("one row, got %d", n)
	}
	if n := countRows(t, e, `SELECT count(*) FROM product_events WHERE name = 'block_created'`); n != 1 {
		t.Fatalf("block_created is recorded once, not on the replay: %d", n)
	}
}

func TestIdempotentCreate_ReplayedRuleCreatesOneRow(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	body := ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02")
	first := postWithKey(e, "/recurring-rules", body, "rule-key-0001", "")
	again := postWithKey(e, "/recurring-rules", body, "rule-key-0001", "")
	if first.Code != http.StatusCreated || again.Code != http.StatusCreated {
		t.Fatalf("both answer 201: %d %s / %d %s", first.Code, first.Body.String(), again.Code, again.Body.String())
	}
	contracttest.Validate(t, "recurring", "rule_response", again.Body.Bytes())
	if a, b := idOf(t, first, "rule"), idOf(t, again, "rule"); a == 0 || a != b {
		t.Fatalf("the replay returns the first rule: %d vs %d", a, b)
	}
	if n := countRows(t, e, `SELECT count(*) FROM recurring_rules`); n != 1 {
		t.Fatalf("one rule, got %d", n)
	}
}

func TestIdempotentCreate_ConcurrentReplaysOfARuleCreateOneRow(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	body := ruleBody(gymCategory(t, e), "Gym", "[2]", 600, 660, "2026-11-02")
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = postWithKey(e, "/recurring-rules", body, "rule-key-race1", "").Code
		}()
	}
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusCreated {
			t.Fatalf("every replay answers 201: %v", codes)
		}
	}
	if n := countRows(t, e, `SELECT count(*) FROM recurring_rules`); n != 1 {
		t.Fatalf("one rule, got %d", n)
	}
}

func TestIdempotentCreate_ReplayedCategoryCreatesOneRow(t *testing.T) {
	e := newTaskEnv(t)
	body := `{"name":"Reading","color":"#3B82F6","icon":"book-open","kind":"study"}`
	first := postWithKey(e, "/categories", body, "category-key-1", "")
	again := postWithKey(e, "/categories", body, "category-key-1", "")
	if first.Code != http.StatusCreated || again.Code != http.StatusCreated {
		t.Fatalf("both answer 201 (not 409 name taken): %d %s / %d %s", first.Code, first.Body.String(), again.Code, again.Body.String())
	}
	contracttest.Validate(t, "categories", "category_response", again.Body.Bytes())
	if a, b := idOf(t, first, "category"), idOf(t, again, "category"); a == 0 || a != b {
		t.Fatalf("the replay returns the first category: %d vs %d", a, b)
	}
	if n := countRows(t, e, `SELECT count(*) FROM categories WHERE name = 'Reading'`); n != 1 {
		t.Fatalf("one category, got %d", n)
	}
	// Without a key, the same name is a real second create and is refused.
	if w := postWithKey(e, "/categories", body, "", ""); w.Code != http.StatusConflict {
		t.Fatalf("a second create without a key: %d", w.Code)
	}
}

func TestIdempotentCreate_KeysBelongToOneUser(t *testing.T) {
	e := newTaskEnv(t)
	body := taskJSON("Mine", "09:00", "10:00", "2026-11-30")
	a := postWithKey(e, "/tasks", body, "shared-key-01", "")
	b := postWithKey(e, "/tasks", body, "shared-key-01", "2")
	if a.Code != http.StatusCreated || b.Code != http.StatusCreated {
		t.Fatalf("%d / %d %s", a.Code, b.Code, b.Body.String())
	}
	if idOf(t, a, "task") == idOf(t, b, "task") {
		t.Fatal("another user's key never returns this user's block")
	}
	if n := countRows(t, e, `SELECT count(*) FROM tasks WHERE name = 'Mine'`); n != 2 {
		t.Fatalf("one block each, got %d", n)
	}
}

func TestIdempotentCreate_RejectsABadKeyAndWorksWithout(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	gym := gymCategory(t, e)
	for target, body := range map[string]string{
		"/tasks":           taskJSON("K", "11:00", "12:00", "2026-11-30"),
		"/recurring-rules": ruleBody(gym, "K", "[4]", 60, 120, "2026-11-02"),
		"/categories":      `{"name":"K","color":"#3B82F6","icon":"book-open","kind":"study"}`,
	} {
		for _, key := range []string{"short", strings.Repeat("k", 101)} {
			w := postWithKey(e, target, body, key, "")
			if w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != "idempotency_key_required" {
				t.Errorf("%s with a %d-character key: %d %s", target, len(key), w.Code, w.Body.String())
			}
		}
		// The previous web version sends no key: still accepted.
		if w := postWithKey(e, target, body, "", ""); w.Code != http.StatusCreated {
			t.Errorf("%s without a key: %d %s", target, w.Code, w.Body.String())
		}
	}
	if n := countRows(t, e, `SELECT count(*) FROM idempotency_keys`); n != 0 {
		t.Fatalf("no key stored for keyless or rejected requests: %d", n)
	}
}
