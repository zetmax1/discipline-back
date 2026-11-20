package handlers

import (
	"fmt"
	"net/http"
	"testing"
)

// M3: the API resolves a legacy category string itself, so its writes keep working once
// migration 00015 drops the trigger. M8: the answer carries the resolved categoryId and kind.

func dropTrigger(t *testing.T, e *taskEnv) {
	t.Helper()
	mustExecDB(t, e, `DROP TRIGGER trg_tasks_fill_category_id ON tasks`)
}

type catRow struct {
	CategoryID *uint
	Kind       *string
	Category   string
}

func blockCategory(t *testing.T, e *taskEnv, name string) catRow {
	t.Helper()
	var r catRow
	e.db.Raw(`SELECT t.category_id, c.kind, t.category FROM tasks t LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.name = ? ORDER BY t.id DESC LIMIT 1`, name).Scan(&r)
	return r
}

func TestLegacyCategory_ResolvedByTheAPIWithoutTheTrigger(t *testing.T) {
	e := newTaskEnv(t)
	dropTrigger(t, e)

	w := doRequest(e.r, http.MethodPost, "/tasks", `{"name":"Lift","category":"gym","startTime":"07:00","endTime":"08:00","energy":3,"date":"2026-11-30"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	b := blockFrom(t, w)
	if b.CategoryID == nil || b.CategoryKind == nil || *b.CategoryKind != "exercise" {
		t.Fatalf("the 201 body carries the category (M8): %+v", b)
	}
	if r := blockCategory(t, e, "Lift"); r.CategoryID == nil || *r.CategoryID != *b.CategoryID || r.Category != "gym" {
		t.Fatalf("stored with its category, legacy string kept: %+v", r)
	}
	var kinds []string
	e.db.Raw(`SELECT props->>'category_kind' FROM product_events WHERE name = 'block_created'`).Scan(&kinds)
	if len(kinds) != 1 || kinds[0] != "exercise" {
		t.Fatalf("block_created carries the kind (M8): %v", kinds)
	}

	// The same string twice finds the same (default) category, never a duplicate.
	for i, day := range []string{"2026-11-30", "2026-12-01"} {
		body := fmt.Sprintf(`{"name":"Admin%d","category":"admin","startTime":"10:00","endTime":"11:00","energy":3,"date":%q}`, i, day)
		if w := doRequest(e.r, http.MethodPost, "/tasks", body); w.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
	}
	a, c := blockCategory(t, e, "Admin0"), blockCategory(t, e, "Admin1")
	if a.CategoryID == nil || c.CategoryID == nil || *a.CategoryID != *c.CategoryID || *a.Kind != "admin" {
		t.Fatalf("one Admin category: %+v %+v", a, c)
	}
	if n := countRows(t, e, `SELECT count(*) FROM categories WHERE lower(name) = 'admin'`); n != 1 {
		t.Fatalf("no duplicate category: %d", n)
	}

	// Bulk and the Smart Schedule plan send only legacy strings too.
	if w := doRequest(e.r, http.MethodPost, "/tasks/bulk", `{"tasks":[{"name":"Bulk","category":"study","startTime":"12:00","endTime":"13:00","energy":3,"date":"2026-11-30"}]}`); w.Code != http.StatusCreated {
		t.Fatalf("bulk: %d %s", w.Code, w.Body.String())
	}
	if r := blockCategory(t, e, "Bulk"); r.Kind == nil || *r.Kind != "study" {
		t.Fatalf("bulk resolved: %+v", r)
	}
	if code, out := e.savePlan(t, planBody("2026-11-30", `{"name":"Planned","category":"deep-work","startTime":"14:00","endTime":"15:00","energy":3,"date":"2026-12-02"}`)); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("plan: %d %v", code, out)
	}
	if r := blockCategory(t, e, "Planned"); r.Kind == nil || *r.Kind != "focus" {
		t.Fatalf("plan resolved: %+v", r)
	}

	// A legacy-only update moves the block to the matching category.
	w = doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", b.ID), `{"category":"rest"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if u := blockFrom(t, w); u.CategoryKind == nil || *u.CategoryKind != "rest" {
		t.Fatalf("update resolved: %+v", u)
	}
	if n := countRows(t, e, `SELECT count(*) FROM tasks WHERE category_id IS NULL`); n != 0 {
		t.Fatalf("no block without a category: %d", n)
	}
}
