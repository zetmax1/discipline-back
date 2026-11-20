package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"discipline-backend/internal/models"
)

// M6 (owner decision): recurring blocks appear without opening the Planner. Every read of blocks
// for a date (Today's GET /tasks?date=, /dashboard/today) plans that date first, with the range's
// bounds: never before today, never more than 8 weeks ahead.

func dayTasks(t *testing.T, e *taskEnv, date string) []models.Task {
	t.Helper()
	w := doRequest(e.r, http.MethodGet, "/tasks?date="+date, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /tasks?date=%s: %d %s", date, w.Code, w.Body.String())
	}
	var b struct{ Tasks []models.Task }
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b.Tasks
}

func ruleRows(t *testing.T, e *taskEnv, ruleID uint) int64 {
	return countRows(t, e, `SELECT count(*) FROM tasks WHERE recurring_rule_id = ?`, ruleID)
}

func TestPlanOnRead_ARuleShowsOnTodayNextWeekWithoutThePlanner(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent) // Mon 2026-11-02
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))

	setNow(t, mondayTashkent.AddDate(0, 0, 7)) // next Monday; no range was ever read
	got := dayTasks(t, e, "2026-11-09")
	if len(got) != 1 || got[0].RecurringRuleID == nil || *got[0].RecurringRuleID != r.ID || got[0].StartMin != 1080 {
		t.Fatalf("Today shows the gym occurrence: %+v", got)
	}
	if n := ruleRows(t, e, r.ID); n != 1 {
		t.Fatalf("only the day that was read is planned, got %d rows", n)
	}
	dayTasks(t, e, "2026-11-09") // a second read creates nothing more
	if n := ruleRows(t, e, r.ID); n != 1 {
		t.Fatalf("idempotent, got %d rows", n)
	}
}

func TestPlanOnRead_KeepsTheBounds(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent.AddDate(0, 0, 7)) // Mon 2026-11-09
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	if got := dayTasks(t, e, "2026-11-04"); len(got) != 0 { // a past Wednesday
		t.Fatalf("a past day is never planned: %+v", got)
	}
	ahead := mondayTashkent.AddDate(0, 0, 7+56+2).Format("2006-01-02") // Wednesday beyond 8 weeks
	if got := dayTasks(t, e, ahead); len(got) != 0 {
		t.Fatalf("nothing more than 8 weeks ahead: %+v", got)
	}
	doRequest(e.r, http.MethodGet, "/tasks?date=2026-11-31", "") // an unreadable date plans nothing
	if n := ruleRows(t, e, r.ID); n != 0 {
		t.Fatalf("no occurrence created, got %d", n)
	}
}

func TestPlanOnRead_TheDashboardPlansTodayInTheUsersZone(t *testing.T) {
	e := newTaskEnv(t)
	e.tz = "Asia/Tashkent"
	// Sunday 2026-11-08 20:00 UTC is already Monday 01:00 in Tashkent.
	setNow(t, time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC))
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1]", 1080, 1170, "2026-11-02"))
	if w := doRequest(e.r, http.MethodGet, "/dashboard/today", ""); w.Code != http.StatusNoContent {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
	var dates []string
	e.db.Raw(`SELECT date::text FROM tasks WHERE recurring_rule_id = ?`, r.ID).Scan(&dates)
	if len(dates) != 1 || dates[0] != "2026-11-09" {
		t.Fatalf("the user's Monday is planned before the metrics are read: %v", dates)
	}
}
