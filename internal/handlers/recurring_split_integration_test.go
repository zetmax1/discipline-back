package handlers

import (
	"fmt"
	"net/http"
	"testing"
)

// M5: "delete this and following" from a date after the rule already ends never lengthens it;
// a replay of the same delete is harmless.
func TestDeleteRuleFollowing_NeverExtendsTheRule(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	body := `{"categoryId":%d,"name":"Gym","weekdays":[3],"startMin":1080,"endMin":1170,"validFrom":"2026-11-02","validTo":"2026-11-20"}`
	r := createRule(t, e, fmt.Sprintf(body, gymCategory(t, e)))
	for range 2 {
		w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/recurring-rules/%d?scope=following&fromDate=2026-12-02", r.ID), "")
		if w.Code != http.StatusNoContent {
			t.Fatalf("delete following: %d %s", w.Code, w.Body.String())
		}
	}
	var validTo string
	e.db.Raw(`SELECT valid_to::text FROM recurring_rules WHERE id = ?`, r.ID).Scan(&validTo)
	if validTo != "2026-11-20" {
		t.Fatalf("the rule still ends on 2026-11-20, got %s", validTo)
	}
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/recurring-rules/%d?scope=following&fromDate=2026-11-11", r.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete following inside: %d", w.Code)
	}
	e.db.Raw(`SELECT valid_to::text FROM recurring_rules WHERE id = ?`, r.ID).Scan(&validTo)
	if validTo != "2026-11-10" {
		t.Fatalf("cut to the day before, got %s", validTo)
	}
}
