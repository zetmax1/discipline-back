package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"discipline-backend/internal/models"
)

// 2026-11-02 is a Monday.
const planWeek = "2026-11-02"

func planBody(weekStart string, items ...string) string {
	return fmt.Sprintf(`{"weekStart":%q,"tasks":[%s]}`, weekStart, strings.Join(items, ","))
}

func (e *taskEnv) savePlan(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	w := doRequest(e.r, http.MethodPut, "/tasks/plan", body)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *taskEnv) tasksBy(t *testing.T, where string, args ...any) []models.Task {
	t.Helper()
	var tasks []models.Task
	if err := e.db.Where(where, args...).Order("date, start_time").Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestSavePlan_SameWeekTwiceReplacesInsteadOfDuplicating(t *testing.T) {
	e := newTaskEnv(t)
	manual, _ := e.create(t, taskJSON("Manual", "18:00", "19:00", "2026-11-03"))
	if manual != http.StatusCreated {
		t.Fatalf("setup manual task: %d", manual)
	}
	plan := []string{
		taskJSON("Deep Work", "09:00", "11:00", "2026-11-02"),
		taskJSON("Deep Work", "09:00", "11:00", "2026-11-03"),
	}
	code, first := e.savePlan(t, planBody(planWeek, plan...))
	if code != http.StatusOK {
		t.Fatalf("first save: %d %v", code, first)
	}
	code, second := e.savePlan(t, planBody(planWeek, plan...))
	if code != http.StatusOK {
		t.Fatalf("second save: %d %v", code, second)
	}
	if second["replaced"].(float64) != 2 || second["count"].(float64) != 2 || second["planId"] == first["planId"] {
		t.Fatalf("second save must replace the 2 generated blocks with a new plan, got %v", second)
	}

	generated := e.tasksBy(t, "source = ?", "smart-schedule")
	if len(generated) != 2 {
		t.Fatalf("saving the same week twice must leave 2 generated blocks, found %d", len(generated))
	}
	for _, g := range generated {
		if g.PlanID == nil || *g.PlanID != second["planId"] {
			t.Fatalf("generated blocks must carry the latest plan id, got %+v", g)
		}
	}
	if m := e.tasksBy(t, "source = ?", "manual"); len(m) != 1 || m[0].Name != "Manual" {
		t.Fatalf("manual blocks must never be replaced, got %+v", m)
	}
}

func TestSavePlan_OnlyTouchesTheTargetWeek(t *testing.T) {
	e := newTaskEnv(t)
	if code, _ := e.savePlan(t, planBody("2026-11-09", taskJSON("Next", "09:00", "10:00", "2026-11-09"))); code != http.StatusOK {
		t.Fatalf("setup: %d", code)
	}
	if code, _ := e.savePlan(t, planBody(planWeek, taskJSON("This", "09:00", "10:00", "2026-11-02"))); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	if got := e.tasksBy(t, "source = ?", "smart-schedule"); len(got) != 2 {
		t.Fatalf("plans for other weeks must survive, got %d blocks", len(got))
	}
}

func TestSavePlan_OverlapWithManualBlockChangesNothing(t *testing.T) {
	e := newTaskEnv(t)
	e.savePlan(t, planBody(planWeek, taskJSON("Old plan", "07:00", "08:00", "2026-11-02")))
	e.create(t, taskJSON("Manual", "09:00", "10:00", "2026-11-02"))

	code, _ := e.savePlan(t, planBody(planWeek, taskJSON("Clash", "09:30", "10:30", "2026-11-02")))
	if code != http.StatusConflict {
		t.Fatalf("overlap with a manual block must be 409, got %d", code)
	}
	if got := e.tasksBy(t, "source = ?", "smart-schedule"); len(got) != 1 || got[0].Name != "Old plan" {
		t.Fatalf("a rejected save must leave the previous plan in place, got %+v", got)
	}
}

func TestSavePlan_RejectsBadInput(t *testing.T) {
	e := newTaskEnv(t)
	cases := map[string]string{
		"week does not start on Monday": planBody("2026-11-03", taskJSON("x", "09:00", "10:00", "2026-11-03")),
		"task outside the week":         planBody(planWeek, taskJSON("x", "09:00", "10:00", "2026-11-09")),
		"invalid block":                 planBody(planWeek, taskJSON("x", "10:00", "09:00", "2026-11-02")),
		"bad weekStart":                 planBody("next week", taskJSON("x", "09:00", "10:00", "2026-11-02")),
		"overlap inside the plan":       planBody(planWeek, taskJSON("a", "09:00", "10:00", "2026-11-02"), taskJSON("b", "09:30", "10:30", "2026-11-02")),
	}
	for name, body := range cases {
		code, _ := e.savePlan(t, body)
		want := http.StatusBadRequest
		if name == "overlap inside the plan" {
			want = http.StatusConflict
		}
		if code != want {
			t.Errorf("%s: expected %d, got %d", name, want, code)
		}
	}
	if got := e.tasksBy(t, "1 = 1"); len(got) != 0 {
		t.Fatalf("rejected saves must insert nothing, found %d", len(got))
	}
}

func TestSavePlan_SkippedManualBlockDoesNotBlockThePlan(t *testing.T) {
	e := newTaskEnv(t)
	_, manual := e.create(t, taskJSON("Manual", "09:00", "10:00", "2026-11-02"))
	if c := doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", manual), `{"status":"skipped"}`).Code; c != http.StatusOK {
		t.Fatalf("skip: %d", c)
	}
	if code, _ := e.savePlan(t, planBody(planWeek, taskJSON("Plan", "09:30", "10:30", "2026-11-02"))); code != http.StatusOK {
		t.Fatalf("a skipped manual block must not block the plan, got %d", code)
	}
}
