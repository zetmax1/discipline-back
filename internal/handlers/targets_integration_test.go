package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/events"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

func newTargetsEnv(t *testing.T) *taskEnv {
	t.Helper()
	e := newCommitmentEnv(t)
	RegisterTargetRoutes(e.r, NewTargetHandler(services.NewTargetService(repositories.NewTargetRepository(e.db))))
	RegisterLocalImportRoute(e.r, NewLocalImportHandler(services.NewLocalImportService(repositories.NewCommitmentRepository(e.db), repositories.NewTargetRepository(e.db), events.NewRecorder(e.db))))
	return e
}

func systemCategory(t *testing.T, e *taskEnv, key, user string) int {
	t.Helper()
	w := doRequestAs(e, http.MethodGet, "/categories", "", user)
	var got struct {
		Categories []struct {
			ID        int
			SystemKey *string
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	for _, c := range got.Categories {
		if c.SystemKey != nil && *c.SystemKey == key {
			return c.ID
		}
	}
	t.Fatalf("no %s category", key)
	return 0
}

type targets struct {
	Categories        []struct{ CategoryID, MinutesPerWeek int }
	MaxFocusMinPerDay *int
	RestDay           *int
	IsDefault         bool
}

func getTargets(t *testing.T, e *taskEnv, user string) targets {
	t.Helper()
	w := doRequestAs(e, http.MethodGet, "/weekly-targets", "", user)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	contracttest.Validate(t, "commitments", "weekly_targets", w.Body.Bytes())
	var out targets
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func minutesOf(tg targets, id int) int {
	for _, c := range tg.Categories {
		if c.CategoryID == id {
			return c.MinutesPerWeek
		}
	}
	return -1
}

func TestTargets_DefaultsUntilSaved(t *testing.T) {
	e := newTargetsEnv(t)
	tg := getTargets(t, e, "")
	if !tg.IsDefault || tg.MaxFocusMinPerDay != nil || tg.RestDay != nil {
		t.Fatalf("defaults: %+v", tg)
	}
	if minutesOf(tg, systemCategory(t, e, "deep_work", "")) != 900 || minutesOf(tg, systemCategory(t, e, "study", "")) != 600 ||
		minutesOf(tg, systemCategory(t, e, "rest", "")) != 420 {
		t.Fatalf("Deep Work 15 h, Study 10 h, Rest 7 h expected: %+v", tg)
	}
	if n := countRows(t, e, `SELECT count(*) FROM weekly_targets`); n != 0 {
		t.Fatal("reading the defaults must not write anything")
	}
}

func TestTargets_SaveReplaceAndValidate(t *testing.T) {
	e := newTargetsEnv(t)
	deep, gym := systemCategory(t, e, "deep_work", ""), systemCategory(t, e, "exercise", "")
	body := fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":600},{"categoryId":%d,"minutesPerWeek":180}],"maxFocusMinPerDay":240,"restDay":7}`, deep, gym)
	w := doRequestAs(e, http.MethodPut, "/weekly-targets", body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	contracttest.Validate(t, "commitments", "weekly_targets", w.Body.Bytes())
	tg := getTargets(t, e, "")
	if tg.IsDefault || minutesOf(tg, deep) != 600 || minutesOf(tg, gym) != 180 || *tg.MaxFocusMinPerDay != 240 || *tg.RestDay != 7 || len(tg.Categories) != 2 {
		t.Fatalf("saved: %+v", tg)
	}
	// Replaces every row: the gym target goes when it is left out.
	doRequestAs(e, http.MethodPut, "/weekly-targets", fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":300}],"maxFocusMinPerDay":null,"restDay":null}`, deep), "")
	if tg := getTargets(t, e, ""); len(tg.Categories) != 1 || tg.RestDay != nil || tg.MaxFocusMinPerDay != nil {
		t.Fatalf("replace: %+v", tg)
	}
	other := systemCategory(t, e, "study", "2")
	for name, tc := range map[string][2]string{
		"another user's category": {fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":60}],"maxFocusMinPerDay":null,"restDay":null}`, other), "target_category_invalid"},
		"not 15-minute steps":     {fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":50}],"maxFocusMinPerDay":null,"restDay":null}`, deep), "targets_invalid"},
		"over 80 h":               {fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":4815}],"maxFocusMinPerDay":null,"restDay":null}`, deep), "targets_invalid"},
		"max focus 45":            {`{"categories":[],"maxFocusMinPerDay":45,"restDay":null}`, "targets_invalid"},
		"rest day 8":              {`{"categories":[],"maxFocusMinPerDay":null,"restDay":8}`, "targets_invalid"},
		"one category twice":      {fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":60},{"categoryId":%d,"minutesPerWeek":90}],"maxFocusMinPerDay":null,"restDay":null}`, deep, deep), "targets_invalid"},
	} {
		if w := doRequestAs(e, http.MethodPut, "/weekly-targets", tc[0], ""); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != tc[1] {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestTargets_CategoryDeleteDropsOrReassigns(t *testing.T) {
	e := newTargetsEnv(t)
	deep := systemCategory(t, e, "deep_work", "")
	w := doRequestAs(e, http.MethodPost, "/categories", `{"name":"Reading","color":"#3B82F6","icon":"book-open","kind":"personal"}`, "")
	var c struct{ Category struct{ ID int } }
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	reading := c.Category.ID
	doRequestAs(e, http.MethodPut, "/weekly-targets", fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":4500},{"categoryId":%d,"minutesPerWeek":600}],"maxFocusMinPerDay":null,"restDay":null}`, deep, reading), "")
	if w := doRequestAs(e, http.MethodDelete, fmt.Sprintf("/categories/%d?reassignTo=%d", reading, deep), "", ""); w.Code != http.StatusNoContent {
		t.Fatalf("reassign: %d %s", w.Code, w.Body)
	}
	tg := getTargets(t, e, "")
	if minutesOf(tg, deep) != 4800 || minutesOf(tg, reading) != -1 { // 4500 + 600, capped at 80 h
		t.Fatalf("minutes move to the target category (capped at 4800): %+v", tg)
	}
}

func TestLocalImport_MapsOnceDedupesAndAppliesGoalsOnlyWithoutTargets(t *testing.T) {
	e := newTargetsEnv(t)
	body := `{"commitments":[
		{"id":"1","name":"Uyqu","startTime":"23:00","endTime":"07:00","days":["Monday","Tuesday","Wednesday","Thursday","Friday","Saturday","Sunday"]},
		{"id":"2","name":"Университет","startTime":"10:00","endTime":"13:00","days":["Monday","Wednesday","Friday"]},
		{"id":"3","name":"Gym","startTime":"17:00","endTime":"00:00","days":["Tuesday"]},
		{"id":"4","name":"Choir","startTime":"18:00","endTime":"24:00","days":["Thursday"]},
		{"id":"5","name":"Broken","startTime":"25:00","endTime":"26:00","days":["Monday"]},
		{"id":"6","name":"No days","startTime":"09:00","endTime":"10:00","days":[]}
	],"goals":{"deepWorkHours":12,"studyHours":8,"restHours":5}}`
	w := doRequestAs(e, http.MethodPost, "/commitments/local-import", body, "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	contracttest.Validate(t, "commitments", "local_import_result", w.Body.Bytes())
	if got := w.Body.String(); !strings.Contains(got, `"imported":4`) || !strings.Contains(got, `"invalid":2`) || !strings.Contains(got, `"targetsApplied":true`) {
		t.Fatalf("result: %s", got)
	}
	list := doRequestAs(e, http.MethodGet, "/commitments", "", "").Body.String()
	for _, want := range []string{`"name":"Uyqu","kind":"rest"`, `"name":"Университет","kind":"study"`, `"name":"Gym","kind":"exercise"`, `"name":"Choir","kind":"personal"`,
		`"startMin":1020,"endMin":1440`, `"startMin":1080,"endMin":1440`, `"source":"local_import"`} {
		if !strings.Contains(list, want) {
			t.Errorf("list misses %s: %s", want, list)
		}
	}
	if tg := getTargets(t, e, ""); tg.IsDefault || minutesOf(tg, systemCategory(t, e, "deep_work", "")) != 720 {
		t.Fatalf("goals become targets when none are saved: %+v", tg)
	}
	// A second device sends the same data: duplicates skipped, saved targets untouched.
	w = doRequestAs(e, http.MethodPost, "/commitments/local-import", strings.Replace(body, `"deepWorkHours":12`, `"deepWorkHours":1`, 1), "")
	if got := w.Body.String(); !strings.Contains(got, `"imported":0`) || !strings.Contains(got, `"duplicates":4`) || !strings.Contains(got, `"targetsApplied":false`) {
		t.Fatalf("second import: %s", got)
	}
	if tg := getTargets(t, e, ""); minutesOf(tg, systemCategory(t, e, "deep_work", "")) != 720 {
		t.Fatal("saved targets must not be overwritten")
	}
	if n := countRows(t, e, `SELECT count(*) FROM product_events WHERE name = 'commitment_created' AND props->>'source' = 'local_import'`); n != 4 {
		t.Fatalf("commitment_created per imported commitment, got %d", n)
	}
}
