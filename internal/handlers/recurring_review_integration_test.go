package handlers

import (
	"fmt"
	"net/http"
	"testing"
)

// M1: a plain DELETE /tasks/:id on an occurrence (legacy planner, cached clients) cancels that
// date like "delete just this one"; the next read must not bring the block back.
func TestDeleteTask_OnAnOccurrenceCancelsItsDate(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[3]", 1080, 1170, "2026-11-02"))
	wed := taskOn(t, getRange(t, e, "2026-11-02", "2026-11-08"), r.ID, "2026-11-04")
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/tasks/%d", wed.ID), ""); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	b := getRange(t, e, "2026-11-02", "2026-11-08")
	if got := occurrences(b, r.ID); len(got) != 0 {
		t.Fatalf("the deleted occurrence stays deleted: %v", got)
	}
	if len(b.Exceptions) != 1 || b.Exceptions[0].Reason != "cancelled" || string(b.Exceptions[0].Date) != "2026-11-04" {
		t.Fatalf("a cancelled exception for that date: %+v", b.Exceptions)
	}
	// A plain block still deletes as before, without an exception.
	_, id := e.create(t, taskJSON("Plain", "09:00", "10:00", "2026-11-05"))
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/tasks/%d", id), ""); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete plain: %d", w.Code)
	}
	if n := countRows(t, e, `SELECT count(*) FROM recurring_exceptions`); n != 1 {
		t.Fatalf("no exception for a plain block: %d", n)
	}
}
