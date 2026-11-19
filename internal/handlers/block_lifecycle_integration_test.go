package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/models"
)

func doRequestWithKey(e *taskEnv, method, target, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func blockFrom(t *testing.T, w *httptest.ResponseRecorder) models.Task {
	t.Helper()
	var body struct{ Task models.Task }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: %v", w.Body.String(), err)
	}
	return body.Task
}

func TestCheckIn_DoneDefaultsActualTimesToPlannedAndIsRepeatable(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:30", "2026-10-29"))
	path := fmt.Sprintf("/tasks/%d/check-in", id)
	for range 2 { // a replayed (offline) check-in gives the same result
		w := doRequest(e.r, http.MethodPost, path, `{"status":"done","focusRating":4}`)
		if w.Code != http.StatusOK {
			t.Fatalf("check-in: %d %s", w.Code, w.Body.String())
		}
		contracttest.Validate(t, "blocks", "block_response", w.Body.Bytes())
		b := blockFrom(t, w)
		if b.Status != models.StatusDone || *b.ActualStartMin != 540 || *b.ActualEndMin != 630 || *b.FocusRating != 4 {
			t.Fatalf("unexpected block %+v", b)
		}
	}
}

func TestCheckIn_PartialKeepsGivenActualTimesAndSkipClearsThem(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "11:00", "2026-10-29"))
	path := fmt.Sprintf("/tasks/%d/check-in", id)
	b := blockFrom(t, doRequest(e.r, http.MethodPost, path, `{"status":"partial","actualStartMin":560,"actualEndMin":620,"note":"phone call"}`))
	if b.Status != models.StatusPartial || *b.ActualStartMin != 560 || *b.ActualEndMin != 620 || *b.Note != "phone call" {
		t.Fatalf("unexpected %+v", b)
	}
	b = blockFrom(t, doRequest(e.r, http.MethodPost, path, `{"status":"skipped"}`))
	if b.Status != models.StatusSkipped || b.ActualStartMin != nil || b.ActualEndMin != nil || b.FocusRating != nil {
		t.Fatalf("skipping clears what happened, got %+v", b)
	}
}

func TestCheckIn_RejectsBadInput(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:00", "2026-10-29"))
	path := fmt.Sprintf("/tasks/%d/check-in", id)
	for body, code := range map[string]string{
		`{"status":"moved"}`:                                          "task_status_invalid",
		`{"status":"done","focusRating":6}`:                           "task_focus_rating_invalid",
		`{"status":"done","actualStartMin":600,"actualEndMin":590}`:   "task_end_before_start",
		`{"status":"done","actualStartMin":-1,"actualEndMin":30}`:     "task_time_invalid",
		`{"status":"done","note":"` + strings.Repeat("x", 501) + `"}`: "task_note_too_long",
	} {
		w := doRequest(e.r, http.MethodPost, path, body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
			t.Errorf("%s: want 400 %s, got %d %s", body[:min(len(body), 50)], code, w.Code, w.Body.String())
		}
	}
	if w := doRequest(e.r, http.MethodPost, "/tasks/999999/check-in", `{"status":"done"}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown block: want 404, got %d", w.Code)
	}
}

func TestMove_CreatesTheNewBlockOnceAndFreesTheOldSlot(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("Essay", "20:00", "21:00", "2026-10-29"))
	path := fmt.Sprintf("/tasks/%d/move", id)
	body := `{"date":"2026-10-30","startMin":540,"endMin":600}`

	if w := doRequestWithKey(e, http.MethodPost, path, body, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("a move needs an Idempotency-Key, got %d", w.Code)
	}
	first := doRequestWithKey(e, http.MethodPost, path, body, "move-essay-1")
	if first.Code != http.StatusCreated {
		t.Fatalf("move: %d %s", first.Code, first.Body.String())
	}
	contracttest.Validate(t, "blocks", "block_response", first.Body.Bytes())
	again := doRequestWithKey(e, http.MethodPost, path, body, "move-essay-1")
	moved := blockFrom(t, first)
	if again.Code != http.StatusCreated || blockFrom(t, again).ID != moved.ID {
		t.Fatalf("a replayed move must return the first block, got %d %s", again.Code, again.Body.String())
	}
	if moved.Date != "2026-10-30" || moved.StartMin != 540 || moved.EndMin != 600 || moved.Status != models.StatusPlanned ||
		moved.MovedFromID == nil || *moved.MovedFromID != id || moved.Name != "Essay" {
		t.Fatalf("unexpected new block %+v", moved)
	}
	var count int64
	e.db.Model(&models.Task{}).Where("moved_from_id = ?", id).Count(&count)
	if count != 1 {
		t.Fatalf("exactly one new block, got %d", count)
	}
	var old models.Task
	e.db.First(&old, id)
	if old.Status != models.StatusMoved {
		t.Fatalf("the old block becomes moved, got %s", old.Status)
	}
	// The moved block no longer occupies 20:00-21:00.
	if code, _ := e.create(t, taskJSON("Reading", "20:00", "21:00", "2026-10-29")); code != http.StatusCreated {
		t.Fatalf("a moved block must not block its old slot, got %d", code)
	}
	// Moving again is refused: the block was already moved.
	if w := doRequestWithKey(e, http.MethodPost, path, body, "move-essay-2"); w.Code != http.StatusConflict {
		t.Fatalf("moving a moved block: want 409, got %d", w.Code)
	}
}

func TestBlockEvents(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:00", "2026-10-29"))
	doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", id), `{"status":"in_progress"}`)
	doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", id), `{"name":"renamed"}`) // not a status change
	doRequest(e.r, http.MethodPost, fmt.Sprintf("/tasks/%d/check-in", id), `{"status":"done","focusRating":4}`)
	_, b := e.create(t, taskJSON("B", "11:00", "12:00", "2026-10-29"))
	doRequestWithKey(e, http.MethodPost, fmt.Sprintf("/tasks/%d/move", b), `{"date":"2026-10-30","startMin":540,"endMin":600}`, "move-b-0001")
	doRequestWithKey(e, http.MethodPost, fmt.Sprintf("/tasks/%d/move", b), `{"date":"2026-10-30","startMin":540,"endMin":600}`, "move-b-0001")
	var names []string
	e.db.Raw(`SELECT name FROM product_events ORDER BY id`).Scan(&names)
	want := "block_created,block_status_changed,block_checked_in,block_created,block_moved"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

// sessionSeq numbers the session ids this file makes (clients make UUIDs; the value is opaque).
var sessionSeq int

// session stores a focus session of user ids[0] on the block, at wall-clock times in the user's zone.
func (e *taskEnv) session(t *testing.T, blockID uint, day, start, end, outcome string) {
	t.Helper()
	loc, err := time.LoadLocation(models.DefaultTimezone)
	if err != nil {
		t.Fatal(err)
	}
	wall := func(hhmm string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hhmm, loc)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	sessionSeq++
	s := &models.FocusSession{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", sessionSeq), UserID: e.ids[0], BlockID: &blockID,
		StartedAt: wall(start), PlannedMinutes: 30, BreakMinutes: 5}
	if end != "" {
		ended, out := wall(end), outcome
		s.EndedAt, s.Outcome = &ended, &out
	}
	if err := e.db.Create(s).Error; err != nil {
		t.Fatal(err)
	}
}

func TestCheckIn_ActualTimesComeFromTheCompletedFocusSessions(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:30", "2026-10-29"))
	_, other := e.create(t, taskJSON("B", "14:00", "15:00", "2026-10-29"))
	e.session(t, id, "2026-10-29", "09:05", "09:35", models.SessionCompleted)
	e.session(t, id, "2026-10-29", "09:50", "10:40", models.SessionCompleted) // ran past the block
	e.session(t, id, "2026-10-29", "07:00", "08:00", models.SessionAbandoned) // not time worked
	e.session(t, other, "2026-10-29", "14:00", "15:30", models.SessionCompleted)
	e.session(t, id, "2026-10-29", "11:00", "", "") // still running

	path := fmt.Sprintf("/tasks/%d/check-in", id)
	for range 2 { // and a replay gives the same row
		b := blockFrom(t, doRequest(e.r, http.MethodPost, path, `{"status":"done","focusRating":5}`))
		if *b.ActualStartMin != 545 || *b.ActualEndMin != 640 {
			t.Fatalf("want the completed sessions' span 545-640, got %d-%d", *b.ActualStartMin, *b.ActualEndMin)
		}
	}
	// Another block's sessions stay with that block.
	b := blockFrom(t, doRequest(e.r, http.MethodPost, fmt.Sprintf("/tasks/%d/check-in", other), `{"status":"done"}`))
	if *b.ActualStartMin != 840 || *b.ActualEndMin != 930 {
		t.Fatalf("want 840-930 for the other block, got %d-%d", *b.ActualStartMin, *b.ActualEndMin)
	}
	// A time the user edits still wins, per field.
	b = blockFrom(t, doRequest(e.r, http.MethodPost, path, `{"status":"partial","actualEndMin":615}`))
	if *b.ActualStartMin != 545 || *b.ActualEndMin != 615 {
		t.Fatalf("want 545-615, got %d-%d", *b.ActualStartMin, *b.ActualEndMin)
	}
}

func TestCheckIn_WithoutACompletedSessionTheActualTimesStayPlanned(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:30", "2026-10-29"))
	e.session(t, id, "2026-10-29", "09:10", "09:40", models.SessionAbandoned)
	b := blockFrom(t, doRequest(e.r, http.MethodPost, fmt.Sprintf("/tasks/%d/check-in", id), `{"status":"done"}`))
	if *b.ActualStartMin != 540 || *b.ActualEndMin != 630 {
		t.Fatalf("want the planned times 540-630, got %d-%d", *b.ActualStartMin, *b.ActualEndMin)
	}
}

// A session that crossed midnight is clamped to the block's own day.
func TestCheckIn_SessionAcrossMidnightIsClampedToTheBlocksDay(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("Night", "23:00", "24:00", "2026-10-29"))
	e.session(t, id, "2026-10-29", "23:30", "", "")
	e.db.Exec(`UPDATE focus_sessions SET ended_at = started_at + interval '50 minutes', outcome = 'completed'`)
	b := blockFrom(t, doRequest(e.r, http.MethodPost, fmt.Sprintf("/tasks/%d/check-in", id), `{"status":"done"}`))
	if *b.ActualStartMin != 1410 || *b.ActualEndMin != 1440 {
		t.Fatalf("want 1410-1440, got %d-%d", *b.ActualStartMin, *b.ActualEndMin)
	}
}
