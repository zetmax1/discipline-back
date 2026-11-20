package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"discipline-backend/internal/contracttest"
)

func TestMaterialize_AClashCreatesNoBlockAndRecordsAConflict(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	// Wednesday 18:30-19:00 is taken; Monday's clash is skipped and Friday's moved: neither occupies time.
	e.create(t, taskJSON("Dentist", "18:30", "19:00", "2026-11-04"))
	_, mon := e.create(t, taskJSON("Call", "18:00", "18:30", "2026-11-02"))
	doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", mon), `{"status":"skipped"}`)
	_, fri := e.create(t, taskJSON("Essay", "18:00", "19:00", "2026-11-06"))
	doRequestWithKey(e, http.MethodPost, fmt.Sprintf("/tasks/%d/move", fri), `{"date":"2026-11-07","startMin":540,"endMin":600}`, "move-essay-0001")

	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	b := getRange(t, e, "2026-11-02", "2026-11-08")
	if got := occurrences(b, r.ID); fmt.Sprint(got) != "[2026-11-02 1080-1170 planned 2026-11-06 1080-1170 planned]" {
		t.Fatalf("skipped and moved blocks never count for overlaps; the clash creates nothing: %v", got)
	}
	if len(b.Exceptions) != 1 || b.Exceptions[0].Reason != "conflict" || b.Exceptions[0].Date != "2026-11-04" {
		t.Fatalf("one conflict exception, got %+v", b.Exceptions)
	}
	var props []string
	e.db.Raw(`SELECT props::text FROM product_events WHERE name = 'week_planned'`).Scan(&props)
	if len(props) != 1 || !strings.Contains(props[0], `"conflicts": 1`) {
		t.Fatalf("week_planned counts the conflict: %v", props)
	}
	// Reading again does not retry the conflict or duplicate anything.
	if again := getRange(t, e, "2026-11-02", "2026-11-08"); len(occurrences(again, r.ID)) != 2 || len(again.Exceptions) != 1 {
		t.Fatalf("idempotent: %+v", again)
	}
}

func TestConflict_PlaceAnywayOrSkipThisWeek(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	e.create(t, taskJSON("Dentist", "18:30", "19:00", "2026-11-04"))
	e.create(t, taskJSON("Exam", "18:00", "19:00", "2026-11-06"))
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	getRange(t, e, "2026-11-02", "2026-11-08")

	base := fmt.Sprintf("/recurring-rules/%d/occurrences/", r.ID)
	if w := doRequest(e.r, http.MethodPost, base+"2026-11-02/place-anyway", ""); w.Code != http.StatusNotFound || codeOf(w.Body.Bytes()) != "occurrence_not_found" {
		t.Fatalf("only a conflict can be placed anyway: %d %s", w.Code, w.Body.String())
	}
	if w := doRequestAs(e, http.MethodPost, base+"2026-11-04/place-anyway", "", "2"); w.Code != http.StatusNotFound {
		t.Fatalf("another user's rule: %d", w.Code)
	}
	w := doRequest(e.r, http.MethodPost, base+"2026-11-04/place-anyway", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("place anyway: %d %s", w.Code, w.Body.String())
	}
	contracttest.Validate(t, "blocks", "block_response", w.Body.Bytes())
	if w := doRequest(e.r, http.MethodPost, base+"2026-11-06/skip", ""); w.Code != http.StatusNoContent {
		t.Fatalf("skip this week: %d %s", w.Code, w.Body.String())
	}
	b := getRange(t, e, "2026-11-02", "2026-11-08")
	if got := occurrences(b, r.ID); fmt.Sprint(got) != "[2026-11-02 1080-1170 planned 2026-11-04 1080-1170 planned]" {
		t.Fatalf("placed on purpose despite the overlap: %v", got)
	}
	if len(b.Exceptions) != 1 || b.Exceptions[0].Reason != "cancelled" || b.Exceptions[0].Date != "2026-11-06" {
		t.Fatalf("skip turns the conflict into cancelled: %+v", b.Exceptions)
	}
	if w := doRequest(e.r, http.MethodPost, base+"2026-13-01/skip", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad date: %d", w.Code)
	}
}
