package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/events"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// newCommitmentEnv is a router with the F9a commitment routes (the server adds them with FEATURE_F9A).
func newCommitmentEnv(t *testing.T) *taskEnv {
	t.Helper()
	e := newTaskEnv(t)
	h := NewCommitmentHandler(services.NewCommitmentService(repositories.NewCommitmentRepository(e.db), events.NewRecorder(e.db)))
	RegisterCommitmentRoutes(e.r, h)
	return e
}

const sleep = `{"name":"Sleep","kind":"rest","weekdays":[1,2,3,4,5,6,7],"startMin":1380,"endMin":420,"validFrom":"2026-01-01","validTo":null}`

func createCommitment(t *testing.T, e *taskEnv, body string) map[string]any {
	t.Helper()
	w := doRequestAs(e, http.MethodPost, "/commitments", body, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	contracttest.Validate(t, "commitments", "commitment", w.Body.Bytes())
	var c map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	return c
}

func TestCommitments_CRUDAndContract(t *testing.T) {
	e := newCommitmentEnv(t)
	c := createCommitment(t, e, sleep)
	if c["overnight"] != true || c["source"] != "manual" || c["kind"] != "rest" {
		t.Fatalf("unexpected commitment %v", c)
	}
	id := int(c["id"].(float64))
	w := doRequestAs(e, http.MethodGet, "/commitments", "", "")
	contracttest.Validate(t, "commitments", "commitment_list", w.Body.Bytes())
	if !strings.Contains(w.Body.String(), `"Sleep"`) {
		t.Fatalf("list: %s", w.Body)
	}
	upd := strings.Replace(sleep, `"Sleep"`, `"Night sleep"`, 1)
	w = doRequestAs(e, http.MethodPut, fmt.Sprintf("/commitments/%d", id), upd, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Night sleep") {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	contracttest.Validate(t, "commitments", "commitment", w.Body.Bytes())
	// Another user's id is not theirs to see: 404, never 403.
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		if w := doRequestAs(e, m, fmt.Sprintf("/commitments/%d", id), upd, "2"); w.Code != http.StatusNotFound || codeOf(w.Body.Bytes()) != "commitment_not_found" {
			t.Fatalf("%s as another user: %d %s", m, w.Code, w.Body)
		}
	}
	if w := doRequestAs(e, http.MethodGet, "/commitments", "", "2"); strings.Contains(w.Body.String(), "Night sleep") {
		t.Fatal("another user must not see the commitment")
	}
	if w := doRequestAs(e, http.MethodDelete, fmt.Sprintf("/commitments/%d", id), "", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := doRequestAs(e, http.MethodDelete, fmt.Sprintf("/commitments/%d", id), "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", w.Code)
	}
}

func TestCommitments_Validation(t *testing.T) {
	e := newCommitmentEnv(t)
	cases := map[string][2]string{
		"empty weekdays": {strings.Replace(sleep, `[1,2,3,4,5,6,7]`, `[]`, 1), "commitment_weekdays_invalid"},
		"weekday 8":      {strings.Replace(sleep, `[1,2,3,4,5,6,7]`, `[1,8]`, 1), "commitment_weekdays_invalid"},
		"weekday 0":      {strings.Replace(sleep, `[1,2,3,4,5,6,7]`, `[0]`, 1), "commitment_weekdays_invalid"},
		"start = end":    {strings.Replace(sleep, `"endMin":420`, `"endMin":1380`, 1), "commitment_times_invalid"},
		"start 1440":     {strings.Replace(sleep, `"startMin":1380`, `"startMin":1440`, 1), "commitment_times_invalid"},
		"end 0":          {strings.Replace(sleep, `"endMin":420`, `"endMin":0`, 1), "commitment_times_invalid"},
		"to before from": {strings.Replace(sleep, `"validTo":null`, `"validTo":"2025-12-31"`, 1), "commitment_dates_invalid"},
		"bad date":       {strings.Replace(sleep, `"2026-01-01"`, `"2026-02-30"`, 1), "commitment_dates_invalid"},
		"unknown kind":   {strings.Replace(sleep, `"rest"`, `"sleep"`, 1), "commitment_kind_invalid"},
		"empty name":     {strings.Replace(sleep, `"Sleep"`, `"  "`, 1), "validation_failed"},
		"long name":      {strings.Replace(sleep, `"Sleep"`, `"`+strings.Repeat("x", 101)+`"`, 1), "validation_failed"},
	}
	for name, tc := range cases {
		w := doRequestAs(e, http.MethodPost, "/commitments", tc[0], "")
		if w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != tc[1] {
			t.Errorf("%s: got %d %s, want 400 %s", name, w.Code, w.Body, tc[1])
		}
	}
	// Weekdays are sorted and de-duplicated; an end of 00:00 arrives as 1440 and is not overnight.
	c := createCommitment(t, e, `{"name":"Gym","kind":"exercise","weekdays":[5,1,3,1],"startMin":1200,"endMin":1440,"validFrom":"2026-01-01","validTo":null}`)
	if fmt.Sprint(c["weekdays"]) != "[1 3 5]" || c["overnight"] != false {
		t.Fatalf("got %v", c)
	}
}

func TestCommitments_LimitAndIdempotentReplay(t *testing.T) {
	e := newCommitmentEnv(t)
	mustExecDB(t, e, `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source)
		SELECT ?, 'c' || g, 'personal', '{1}', 60, 120, '2026-01-01', 'manual' FROM generate_series(1, 99) g`, e.ids[0])
	post := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/commitments", strings.NewReader(sleep))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		e.r.ServeHTTP(w, req)
		return w
	}
	first := post("replay-key-0001")
	if first.Code != http.StatusCreated {
		t.Fatalf("100th: %d %s", first.Code, first.Body)
	}
	again := post("replay-key-0001") // a lost answer replayed: same row, no limit error, no second event
	if again.Code != http.StatusCreated || again.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %s vs %s", again.Code, again.Body, first.Body)
	}
	if w := post("another-key-0002"); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != "commitment_limit_reached" {
		t.Fatalf("101st: %d %s", w.Code, w.Body)
	}
	if n := countRows(t, e, `SELECT count(*) FROM product_events WHERE name = 'commitment_created'`); n != 1 {
		t.Fatalf("commitment_created recorded %d times", n)
	}
	var props string
	e.db.Raw(`SELECT props::text FROM product_events WHERE name = 'commitment_created'`).Scan(&props)
	for _, want := range []string{`"source": "manual"`, `"kind": "rest"`, `"overnight": true`, `"weekday_count": 7`} {
		if !strings.Contains(props, want) {
			t.Fatalf("props %s missing %s", props, want)
		}
	}
	if strings.Contains(props, "Sleep") {
		t.Fatal("event props must not carry the name")
	}
}

func TestCommitments_OccurrencesSplitOvernightAndReadTheDayBefore(t *testing.T) {
	e := newCommitmentEnv(t)
	createCommitment(t, e, `{"name":"Sleep","kind":"rest","weekdays":[1,2,3,4,5],"startMin":1380,"endMin":420,"validFrom":"2026-01-01","validTo":null}`)
	createCommitment(t, e, `{"name":"Late","kind":"personal","weekdays":[7],"startMin":1320,"endMin":360,"validFrom":"2026-01-01","validTo":null}`)
	w := doRequestAs(e, http.MethodGet, "/commitments/occurrences?startDate=2026-11-16&endDate=2026-11-17", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	contracttest.Validate(t, "commitments", "occurrence_list", w.Body.Bytes())
	var got struct {
		Segments []struct {
			Date     string
			StartMin int
			EndMin   int
			Part     string
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	want := []string{"2026-11-16 0-360 morning", "2026-11-16 1380-1440 evening", "2026-11-17 0-420 morning", "2026-11-17 1380-1440 evening"}
	var have []string
	for _, s := range got.Segments {
		have = append(have, fmt.Sprintf("%s %d-%d %s", s.Date, s.StartMin, s.EndMin, s.Part))
	}
	if fmt.Sprint(have) != fmt.Sprint(want) {
		t.Fatalf("got %v\nwant %v", have, want)
	}
	for q, code := range map[string]string{
		"startDate=2026-11-01&endDate=2026-12-31": "range_too_long",
		"startDate=2026-11-17&endDate=2026-11-16": "date_invalid",
		"startDate=nope&endDate=2026-11-16":       "date_invalid",
	} {
		if w := doRequestAs(e, http.MethodGet, "/commitments/occurrences?"+q, "", ""); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != code {
			t.Errorf("%s: %d %s", q, w.Code, w.Body)
		}
	}
}
