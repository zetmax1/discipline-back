package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/models"
)

// Monday 2026-11-02, 10:00 in Asia/Tashkent (UTC+5, no DST).
var mondayTashkent = time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)

func setNow(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

type rangeBody struct {
	Tasks      []models.Task
	Exceptions []models.RecurringException
	Truncated  bool
}

func getRange(t *testing.T, e *taskEnv, from, to string) rangeBody {
	t.Helper()
	w := doRequest(e.r, http.MethodGet, "/tasks?startDate="+from+"&endDate="+to, "")
	if w.Code != http.StatusOK {
		t.Fatalf("range %s..%s: %d %s", from, to, w.Code, w.Body.String())
	}
	contracttest.Validate(t, "recurring", "block_range", w.Body.Bytes())
	var b rangeBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

type ruleJSON struct {
	ID              uint
	CategoryID      uint
	Name            string
	Weekdays        []int
	StartMin        int
	EndMin          int
	ValidFrom       string
	ValidTo         *string
	SplitFromRuleID *uint
}

func createRule(t *testing.T, e *taskEnv, body string) ruleJSON {
	t.Helper()
	w := doRequest(e.r, http.MethodPost, "/recurring-rules", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create rule: %d %s", w.Code, w.Body.String())
	}
	contracttest.Validate(t, "recurring", "rule_response", w.Body.Bytes())
	var b struct{ Rule ruleJSON }
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return b.Rule
}

func ruleBody(categoryID uint, name string, weekdays string, start, end int, from string) string {
	return fmt.Sprintf(`{"categoryId":%d,"name":%q,"weekdays":%s,"startMin":%d,"endMin":%d,"validFrom":%q}`,
		categoryID, name, weekdays, start, end, from)
}

// occurrences returns "date start-end status" for the rule's blocks in a range, sorted.
func occurrences(b rangeBody, ruleID uint) []string {
	out := []string{}
	for _, t := range b.Tasks {
		if t.RecurringRuleID != nil && *t.RecurringRuleID == ruleID && t.Status != models.StatusMoved {
			out = append(out, fmt.Sprintf("%s %d-%d %s", t.Date, t.StartMin, t.EndMin, t.Status))
		}
	}
	sort.Strings(out)
	return out
}

func taskOn(t *testing.T, b rangeBody, ruleID uint, date string) models.Task {
	t.Helper()
	for _, x := range b.Tasks {
		if x.RecurringRuleID != nil && *x.RecurringRuleID == ruleID && string(x.Date) == date && x.Status != models.StatusMoved {
			return x
		}
	}
	t.Fatalf("no occurrence of rule %d on %s in %+v", ruleID, date, b.Tasks)
	return models.Task{}
}

func gymCategory(t *testing.T, e *taskEnv) uint {
	return categoryByKey(listCategories(t, e, ""), "exercise").ID
}

func TestRecurringRules_ValidateAndNormalise(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	gym := gymCategory(t, e)
	r := createRule(t, e, ruleBody(gym, " Gym ", "[5,1,3,3]", 1080, 1170, "2026-11-02"))
	if strings.Join(strings.Fields(fmt.Sprint(r.Weekdays)), ",") != "[1,3,5]" || r.Name != "Gym" || r.ValidTo != nil {
		t.Fatalf("weekdays sorted and de-duplicated, name trimmed: %+v", r)
	}
	other := categoryByKey(listCategories(t, e, "2"), "exercise").ID
	for body, want := range map[string]string{
		ruleBody(gym, "G", "[]", 60, 120, "2026-11-02"):                                                       "recurring_weekdays_invalid",
		ruleBody(gym, "G", "[0]", 60, 120, "2026-11-02"):                                                      "recurring_weekdays_invalid",
		ruleBody(gym, "G", "[8]", 60, 120, "2026-11-02"):                                                      "recurring_weekdays_invalid",
		ruleBody(gym, "G", "[1]", 120, 120, "2026-11-02"):                                                     "task_end_before_start",
		ruleBody(gym, "G", "[1]", -5, 120, "2026-11-02"):                                                      "task_time_invalid",
		ruleBody(gym, "G", "[1]", 60, 1441, "2026-11-02"):                                                     "task_time_invalid",
		ruleBody(gym, "G", "[1]", 60, 120, "2026-02-30"):                                                      "recurring_dates_invalid",
		ruleBody(gym, "", "[1]", 60, 120, "2026-11-02"):                                                       "task_name_length",
		ruleBody(other, "G", "[1]", 60, 120, "2026-11-02"):                                                    "category_invalid",
		strings.Replace(ruleBody(gym, "G", "[1]", 60, 120, "2026-11-02"), "}", `,"validTo":"2026-11-01"}`, 1): "recurring_dates_invalid",
	} {
		w := doRequest(e.r, http.MethodPost, "/recurring-rules", body)
		if w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != want {
			t.Errorf("%s: want 400 %s, got %d %s", body, want, w.Code, w.Body.String())
		}
	}
	w := doRequest(e.r, http.MethodGet, "/recurring-rules", "")
	contracttest.Validate(t, "recurring", "rule_list", w.Body.Bytes())
	if !strings.Contains(w.Body.String(), `"name":"Gym"`) {
		t.Fatalf("list: %s", w.Body.String())
	}
	if w := doRequestAs(e, http.MethodGet, "/recurring-rules", "", "2"); strings.Contains(w.Body.String(), "Gym") {
		t.Fatalf("another user's rules are not listed: %s", w.Body.String())
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		if w := doRequestAs(e, m, fmt.Sprintf("/recurring-rules/%d", r.ID), `{"name":"x"}`, "2"); w.Code != http.StatusNotFound || codeOf(w.Body.Bytes()) != "recurring_rule_not_found" {
			t.Errorf("%s another user's rule: %d %s", m, w.Code, w.Body.String())
		}
	}
}

func TestRange_MaterializesTheRightWeekdaysOnceAndNeverInThePast(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	study := categoryByKey(listCategories(t, e, ""), "study").ID
	r := createRule(t, e, ruleBody(study, "Lectures", "[1,3,5]", 540, 720, "2026-10-26"))

	first := getRange(t, e, "2026-10-26", "2026-11-08")
	want := []string{"2026-11-02 540-720 planned", "2026-11-04 540-720 planned", "2026-11-06 540-720 planned"}
	if got := occurrences(first, r.ID); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Mon/Wed/Fri from today on (last week stays as it was): want %v, got %v", want, got)
	}
	occ := taskOn(t, first, r.ID, "2026-11-04")
	if occ.Source != models.SourceRecurring || occ.CategoryKind == nil || *occ.CategoryKind != "study" || occ.Name != "Lectures" || occ.RecurringEdited {
		t.Fatalf("an occurrence: %+v", occ)
	}
	if first.Truncated {
		t.Fatal("two weeks are not truncated")
	}
	again := getRange(t, e, "2026-11-02", "2026-11-08")
	if got := occurrences(again, r.ID); len(got) != 3 {
		t.Fatalf("a second read creates nothing, got %v", got)
	}

	for q, code := range map[string]string{
		"startDate=2026-11-02&endDate=2026-12-14": "range_too_long", // 43 days
		"startDate=2026-11-09&endDate=2026-11-02": "date_invalid",
		"startDate=2026-11-31&endDate=2026-12-02": "date_invalid",
		"startDate=2026-11-02":                    "date_invalid",
	} {
		if w := doRequest(e.r, http.MethodGet, "/tasks?"+q, ""); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != code {
			t.Errorf("%s: want 400 %s, got %d %s", q, code, w.Code, w.Body.String())
		}
	}
	if w := doRequest(e.r, http.MethodGet, "/tasks?startDate=2026-11-02&endDate=2026-12-13", ""); w.Code != http.StatusOK {
		t.Errorf("42 days are allowed, got %d", w.Code)
	}
}

func TestRange_PlansAtMostEightWeeksAhead(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Walk", "[1,2,3,4,5,6,7]", 420, 450, "2026-11-02"))
	// today + 56 days = 2026-12-28. Ask for 2026-12-22 .. 2027-01-01.
	b := getRange(t, e, "2026-12-22", "2027-01-01")
	got := occurrences(b, r.ID)
	if len(got) != 7 || !strings.HasPrefix(got[6], "2026-12-28") || !b.Truncated {
		t.Fatalf("up to today+56 and truncated, got %v truncated=%v", got, b.Truncated)
	}
}

func TestOccurrence_CancelWritesAnExceptionInsteadOfSkipping(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	b := getRange(t, e, "2026-11-02", "2026-11-08")
	wed := taskOn(t, b, r.ID, "2026-11-04")
	mon := taskOn(t, b, r.ID, "2026-11-02")

	if w := doRequestAs(e, http.MethodDelete, fmt.Sprintf("/tasks/%d/occurrence", wed.ID), "", "2"); w.Code != http.StatusNotFound {
		t.Fatalf("another user's occurrence: %d", w.Code)
	}
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/tasks/%d/occurrence", wed.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	doRequest(e.r, http.MethodPost, fmt.Sprintf("/tasks/%d/check-in", mon.ID), `{"status":"done"}`)
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/tasks/%d/occurrence", mon.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("cancel a done one: %d", w.Code)
	}
	after := getRange(t, e, "2026-11-02", "2026-11-08")
	if got := occurrences(after, r.ID); fmt.Sprint(got) != "[2026-11-02 1080-1170 done 2026-11-06 1080-1170 planned]" {
		t.Fatalf("the cancelled one is gone and not recreated, the done one is kept: %v", got)
	}
	reasons := map[string]string{}
	for _, x := range after.Exceptions {
		reasons[string(x.Date)] = x.Reason
	}
	if reasons["2026-11-04"] != "cancelled" || reasons["2026-11-02"] != "cancelled" {
		t.Fatalf("cancelled exceptions, got %+v", after.Exceptions)
	}
	var skipped int64
	e.db.Raw(`SELECT count(*) FROM tasks WHERE status = 'skipped'`).Scan(&skipped)
	if skipped != 0 {
		t.Fatal("cancelling never writes status = 'skipped'")
	}
	_, manual := e.create(t, taskJSON("Manual", "07:00", "08:00", "2026-11-03"))
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/tasks/%d/occurrence", manual), ""); w.Code != http.StatusNotFound || codeOf(w.Body.Bytes()) != "occurrence_not_found" {
		t.Fatalf("a manual block is not an occurrence: %d %s", w.Code, w.Body.String())
	}
}

func TestOccurrence_ThisOneAndMoveKeepTheRule(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	b := getRange(t, e, "2026-11-02", "2026-11-08")
	wed := taskOn(t, b, r.ID, "2026-11-04")
	fri := taskOn(t, b, r.ID, "2026-11-06")
	mon := taskOn(t, b, r.ID, "2026-11-02")

	edited := blockFrom(t, doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", wed.ID), `{"startTime":"19:00","endTime":"20:30"}`))
	if !edited.RecurringEdited || edited.RecurringRuleID == nil || *edited.RecurringRuleID != r.ID || edited.StartMin != 1140 {
		t.Fatalf("this one: %+v", edited)
	}
	// Move Friday to Saturday; same-day move of Monday (no collision with the old moved row).
	moved := blockFrom(t, doRequestWithKey(e, http.MethodPost, fmt.Sprintf("/tasks/%d/move", fri.ID), `{"date":"2026-11-07","startMin":600,"endMin":690}`, "move-fri-0001"))
	if moved.RecurringRuleID == nil || moved.OriginalDate == nil || *moved.OriginalDate != "2026-11-06" || !moved.RecurringEdited || moved.Source != models.SourceRecurring {
		t.Fatalf("moved occurrence: %+v", moved)
	}
	w := doRequestWithKey(e, http.MethodPost, fmt.Sprintf("/tasks/%d/move", mon.ID), `{"date":"2026-11-02","startMin":1200,"endMin":1290}`, "move-mon-0001")
	if w.Code != http.StatusCreated {
		t.Fatalf("same-day move: %d %s", w.Code, w.Body.String())
	}
	after := getRange(t, e, "2026-11-02", "2026-11-08")
	want := "[2026-11-02 1200-1290 planned 2026-11-04 1140-1230 planned 2026-11-07 600-690 planned]"
	if got := occurrences(after, r.ID); fmt.Sprint(got) != want {
		t.Fatalf("Friday is not planned again: want %s, got %v", want, got)
	}
	reasons := map[string]string{}
	for _, x := range after.Exceptions {
		reasons[string(x.Date)] = x.Reason
	}
	if reasons["2026-11-06"] != "moved" || reasons["2026-11-02"] != "moved" {
		t.Fatalf("moved exceptions: %+v", after.Exceptions)
	}
}

func TestSplit_ThisAndFollowingKeepsEditedAndDoneOccurrences(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	b := getRange(t, e, "2026-11-02", "2026-11-15")
	wed := taskOn(t, b, r.ID, "2026-11-04")
	fri := taskOn(t, b, r.ID, "2026-11-06")
	mon2 := taskOn(t, b, r.ID, "2026-11-09")
	doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", wed.ID), `{"startTime":"19:00","endTime":"20:30"}`)
	doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", fri.ID), `{"status":"done"}`)
	doRequest(e.r, http.MethodDelete, fmt.Sprintf("/tasks/%d/occurrence", mon2.ID), "")

	w := doRequest(e.r, http.MethodPut, fmt.Sprintf("/recurring-rules/%d?scope=following&fromDate=2026-11-04", r.ID), `{"startMin":1110,"endMin":1200}`)
	if w.Code != http.StatusOK {
		t.Fatalf("split: %d %s", w.Code, w.Body.String())
	}
	contracttest.Validate(t, "recurring", "rule_response", w.Body.Bytes())
	var nb struct{ Rule ruleJSON }
	_ = json.Unmarshal(w.Body.Bytes(), &nb)
	next := nb.Rule
	if next.ID == r.ID || next.ValidFrom != "2026-11-04" || next.SplitFromRuleID == nil || *next.SplitFromRuleID != r.ID || next.StartMin != 1110 {
		t.Fatalf("new rule: %+v", next)
	}
	var oldTo string
	e.db.Raw(`SELECT valid_to::text FROM recurring_rules WHERE id = ?`, r.ID).Scan(&oldTo)
	if oldTo != "2026-11-03" {
		t.Fatalf("the old rule ends the day before, got %q", oldTo)
	}
	after := getRange(t, e, "2026-11-02", "2026-11-15")
	if got := occurrences(after, r.ID); fmt.Sprint(got) != "[2026-11-02 1080-1170 planned]" {
		t.Fatalf("before the split the old rule is untouched: %v", got)
	}
	want := "[2026-11-04 1140-1230 planned 2026-11-06 1080-1170 done 2026-11-11 1110-1200 planned 2026-11-13 1110-1200 planned]"
	if got := occurrences(after, next.ID); fmt.Sprint(got) != want {
		t.Fatalf("edited and done kept and re-pointed, untouched re-planned, the cancelled Monday stays cancelled:\nwant %s\ngot  %v", want, got)
	}
}

func TestRuleEdit_ScopeAllNeverRewritesThePast(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	getRange(t, e, "2026-11-02", "2026-11-08")
	setNow(t, mondayTashkent.AddDate(0, 0, 3)) // Thursday
	if w := doRequest(e.r, http.MethodPut, fmt.Sprintf("/recurring-rules/%d?scope=all", r.ID), `{"startMin":1200,"endMin":1290,"name":"Evening gym"}`); w.Code != http.StatusOK {
		t.Fatalf("edit all: %d %s", w.Code, w.Body.String())
	}
	after := getRange(t, e, "2026-11-02", "2026-11-08")
	want := "[2026-11-02 1080-1170 planned 2026-11-04 1080-1170 planned 2026-11-06 1200-1290 planned]"
	if got := occurrences(after, r.ID); fmt.Sprint(got) != want {
		t.Fatalf("want %s, got %v", want, got)
	}
	if taskOn(t, after, r.ID, "2026-11-02").Name != "Gym" || taskOn(t, after, r.ID, "2026-11-06").Name != "Evening gym" {
		t.Fatal("past occurrences keep their name")
	}
}

func TestRuleDelete_FollowingAndAll(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	b := getRange(t, e, "2026-11-02", "2026-11-15")
	doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", taskOn(t, b, r.ID, "2026-11-11").ID), `{"name":"Leg day"}`)
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/recurring-rules/%d?scope=following&fromDate=2026-11-09", r.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete following: %d %s", w.Code, w.Body.String())
	}
	after := getRange(t, e, "2026-11-02", "2026-11-15")
	want := "[2026-11-02 1080-1170 planned 2026-11-04 1080-1170 planned 2026-11-06 1080-1170 planned 2026-11-11 1080-1170 planned]"
	if got := occurrences(after, r.ID); fmt.Sprint(got) != want {
		t.Fatalf("untouched following ones go, the edited one stays as history: want %s, got %v", want, got)
	}
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/recurring-rules/%d", r.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete all: %d", w.Code)
	}
	final := getRange(t, e, "2026-11-02", "2026-11-15")
	var left []string
	for _, x := range final.Tasks {
		left = append(left, string(x.Date))
	}
	if fmt.Sprint(left) != "[2026-11-11]" {
		t.Fatalf("only the edited one stays (as a plain block), got %v", left)
	}
}

func TestRange_TimezoneAndDST(t *testing.T) {
	e := newTaskEnv(t)
	e.tz = "America/New_York"
	// 2026-11-01 03:30 UTC is still Saturday 2026-10-31 23:30 in New York (DST ends that night).
	setNow(t, time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC))
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Run", "[1,2,3,4,5,6,7]", 540, 600, "2026-10-01"))
	b := getRange(t, e, "2026-10-30", "2026-11-02")
	if got := occurrences(b, r.ID); fmt.Sprint(got) != "[2026-10-31 540-600 planned 2026-11-01 540-600 planned 2026-11-02 540-600 planned]" {
		t.Fatalf("'today' is the user's Saturday; 09:00 stays 09:00 across fall-back: %v", got)
	}
	// Spring forward 2027-03-14: 02:30 does not exist that night; the occurrence keeps its minutes.
	setNow(t, time.Date(2027, 3, 13, 15, 0, 0, 0, time.UTC))
	r2 := createRule(t, e, ruleBody(gymCategory(t, e), "Night", "[7]", 150, 180, "2027-03-01"))
	b = getRange(t, e, "2027-03-13", "2027-03-15")
	if got := occurrences(b, r2.ID); fmt.Sprint(got) != "[2027-03-14 150-180 planned]" {
		t.Fatalf("the skipped hour keeps its minutes: %v", got)
	}
}

func TestRange_ConcurrentReadsCreateOneRowPerSlot(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	r := createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doRequest(e.r, http.MethodGet, "/tasks?startDate=2026-11-02&endDate=2026-11-08", "")
		}()
	}
	wg.Wait()
	var n int64
	e.db.Raw(`SELECT count(*) FROM tasks WHERE recurring_rule_id = ?`, r.ID).Scan(&n)
	if n != 3 {
		t.Fatalf("one row per slot, got %d", n)
	}
}

func TestRange_WeekPlannedEventOncePerRequestThatCreatedSomething(t *testing.T) {
	e := newTaskEnv(t)
	setNow(t, mondayTashkent)
	createRule(t, e, ruleBody(gymCategory(t, e), "Gym", "[1,3,5]", 1080, 1170, "2026-11-02"))
	getRange(t, e, "2026-11-02", "2026-11-08")
	getRange(t, e, "2026-11-02", "2026-11-08")
	var props []string
	e.db.Raw(`SELECT props::text FROM product_events WHERE name = 'week_planned'`).Scan(&props)
	if len(props) != 1 || !strings.Contains(props[0], `"created": 3`) || !strings.Contains(props[0], `"days": 7`) {
		t.Fatalf("one week_planned with counts only, got %v", props)
	}
	var created []string
	e.db.Raw(`SELECT props::text FROM product_events WHERE name = 'block_created'`).Scan(&created)
	if len(created) != 3 || !strings.Contains(created[0], `"category_kind": "exercise"`) || strings.Contains(created[0], "Gym") {
		t.Fatalf("block_created per occurrence with the kind and no name, got %v", created)
	}
}
