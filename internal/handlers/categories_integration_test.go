package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/models"
)

type categoryJSON struct {
	ID         uint    `json:"id"`
	Name       string  `json:"name"`
	Color      string  `json:"color"`
	Icon       string  `json:"icon"`
	Kind       string  `json:"kind"`
	SystemKey  *string `json:"systemKey"`
	NameCustom bool    `json:"nameCustom"`
}

func listCategories(t *testing.T, e *taskEnv, user string) []categoryJSON {
	t.Helper()
	w := doRequestAs(e, http.MethodGet, "/categories", "", user)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /categories: %d %s", w.Code, w.Body.String())
	}
	contracttest.Validate(t, "categories", "category_list", w.Body.Bytes())
	var body struct{ Categories []categoryJSON }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Categories
}

func categoryByKey(cats []categoryJSON, key string) *categoryJSON {
	for i := range cats {
		if cats[i].SystemKey != nil && *cats[i].SystemKey == key {
			return &cats[i]
		}
	}
	return nil
}

func TestCategories_NewUserGetsSixDefaultsLazilyAndOnce(t *testing.T) {
	e := newTaskEnv(t)
	first := listCategories(t, e, "")
	keys := []string{}
	for _, c := range first {
		if c.SystemKey == nil || c.NameCustom {
			t.Fatalf("a default has a system key and no custom name: %+v", c)
		}
		keys = append(keys, *c.SystemKey)
	}
	if strings.Join(keys, ",") != "deep_work,study,admin,exercise,rest,personal" {
		t.Fatalf("defaults in order, got %v", keys)
	}
	if again := listCategories(t, e, ""); len(again) != 6 || again[0].ID != first[0].ID {
		t.Fatalf("a second read changes nothing, got %+v", again)
	}
}

func TestCategories_EnsureDefaultsAdoptsAndSkipsInsteadOfFailing(t *testing.T) {
	e := newTaskEnv(t)
	u := e.ids[0]
	// Made by the backfill: a "Study" of kind study without a key (adopted), an "Admin" of kind
	// personal (the admin default is skipped: the user owns that name).
	mustExecDB(t, e, `INSERT INTO categories (id, user_id, name, color, icon, kind) VALUES
		(500, ?, 'study', '#8B5CF6', 'book-open', 'study'), (501, ?, 'Admin', '#EC4899', 'user', 'personal')`, u, u)
	mustExecDB(t, e, `INSERT INTO tasks (user_id, name, category, category_id, start_time, end_time, date) VALUES (?, 'x', 'study', 500, '09:00', '10:00', '2026-10-29')`, u)
	for range 2 {
		cats := listCategories(t, e, "")
		if s := categoryByKey(cats, "study"); s == nil || s.ID != 500 {
			t.Fatalf("the existing Study is adopted, keeping its id: %+v", cats)
		}
		if categoryByKey(cats, "admin") != nil || len(cats) != 6 {
			t.Fatalf("five defaults (Study adopted) plus the user's Admin, got %+v", cats)
		}
	}
}

func TestCategories_CreateValidatesAndNamesAreUniqueCaseInsensitively(t *testing.T) {
	e := newTaskEnv(t)
	w := doRequest(e.r, http.MethodPost, "/categories", `{"name":" Reading ","color":"#ab12cd","icon":"book-open","kind":"study"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	contracttest.Validate(t, "categories", "category_response", w.Body.Bytes())
	var created struct{ Category categoryJSON }
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Category.Name != "Reading" || created.Category.Color != "#AB12CD" || created.Category.SystemKey != nil {
		t.Fatalf("trimmed name, upper-case colour, no system key: %+v", created.Category)
	}
	for body, want := range map[string]string{
		`{"name":"gym","color":"#10B981","icon":"dumbbell","kind":"exercise"}`:     "409 category_name_taken",
		`{"name":"READING","color":"#10B981","icon":"dumbbell","kind":"exercise"}`: "409 category_name_taken",
		`{"name":"X","color":"red","icon":"dumbbell","kind":"exercise"}`:           "400 category_color_invalid",
		`{"name":"X","color":"#10B981","icon":"rocket","kind":"exercise"}`:         "400 category_icon_invalid",
		`{"name":"X","color":"#10B981","icon":"dumbbell","kind":"sleep"}`:          "400 category_kind_invalid",
		`{"name":"  ","color":"#10B981","icon":"dumbbell","kind":"exercise"}`:      "400 category_invalid",
	} {
		w := doRequest(e.r, http.MethodPost, "/categories", body)
		if got := fmt.Sprintf("%d %s", w.Code, codeOf(w.Body.Bytes())); got != want {
			t.Errorf("%s: want %s, got %s", body, want, got)
		}
	}
}

func TestCategories_UpdateRenamesKeepsKindAndChecksOwnership(t *testing.T) {
	e := newTaskEnv(t)
	gym := categoryByKey(listCategories(t, e, ""), "exercise")
	w := doRequest(e.r, http.MethodPut, fmt.Sprintf("/categories/%d", gym.ID), `{"name":"Workout","color":"#000000"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	contracttest.Validate(t, "categories", "category_response", w.Body.Bytes())
	var got struct{ Category categoryJSON }
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Category.Name != "Workout" || !got.Category.NameCustom || got.Category.SystemKey == nil || *got.Category.SystemKey != "exercise" {
		t.Fatalf("a renamed default keeps its key and becomes custom: %+v", got.Category)
	}
	if w := doRequest(e.r, http.MethodPut, fmt.Sprintf("/categories/%d", gym.ID), `{"kind":"rest"}`); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != "category_kind_invalid" {
		t.Errorf("kind is immutable: %d %s", w.Code, w.Body.String())
	}
	if w := doRequest(e.r, http.MethodPut, fmt.Sprintf("/categories/%d", gym.ID), `{"color":"#00000G"}`); codeOf(w.Body.Bytes()) != "category_color_invalid" {
		t.Errorf("colour validated on update: %s", w.Body.String())
	}
	if w := doRequestAs(e, http.MethodPut, fmt.Sprintf("/categories/%d", gym.ID), `{"name":"Mine"}`, "2"); w.Code != http.StatusNotFound || codeOf(w.Body.Bytes()) != "category_not_found" {
		t.Errorf("another user's category: %d %s", w.Code, w.Body.String())
	}
	if w := doRequest(e.r, http.MethodPut, "/categories/abc", `{"name":"x"}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", w.Code)
	}
}

func TestCategories_DeleteRefusesInUseAndReassignsInOneTransaction(t *testing.T) {
	e := newTaskEnv(t)
	cats := listCategories(t, e, "")
	w := doRequest(e.r, http.MethodPost, "/categories", `{"name":"Reading","color":"#AB12CD","icon":"book-open","kind":"study"}`)
	var reading struct{ Category categoryJSON }
	_ = json.Unmarshal(w.Body.Bytes(), &reading)
	id := reading.Category.ID
	study := categoryByKey(cats, "study")
	if code, _ := e.create(t, fmt.Sprintf(`{"name":"Novel","categoryId":%d,"startTime":"20:00","endTime":"21:00","energy":3,"date":"2026-10-29"}`, id)); code != http.StatusCreated {
		t.Fatalf("create with categoryId: %d", code)
	}
	mustExecDB(t, e, `INSERT INTO recurring_rules (user_id, category_id, name, weekdays, start_min, end_min, valid_from) VALUES (?, ?, 'Read', '{1}', 60, 120, '2026-11-02')`, e.ids[0], id)

	path := fmt.Sprintf("/categories/%d", id)
	if w := doRequest(e.r, http.MethodDelete, path, ""); w.Code != http.StatusConflict || codeOf(w.Body.Bytes()) != "category_in_use" {
		t.Fatalf("in use: %d %s", w.Code, w.Body.String())
	}
	other := categoryByKey(listCategories(t, e, "2"), "study")
	for _, target := range []uint{other.ID, id, 999999} {
		if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("%s?reassignTo=%d", path, target), ""); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != "category_invalid" {
			t.Errorf("reassignTo %d: %d %s", target, w.Code, w.Body.String())
		}
	}
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("%s?reassignTo=%d", path, study.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("reassign: %d %s", w.Code, w.Body.String())
	}
	var blocks, rules int64
	e.db.Raw(`SELECT count(*) FROM tasks WHERE category_id = ?`, study.ID).Scan(&blocks)
	e.db.Raw(`SELECT count(*) FROM recurring_rules WHERE category_id = ?`, study.ID).Scan(&rules)
	if blocks != 1 || rules != 1 {
		t.Fatalf("blocks and rules move to the target, got %d %d", blocks, rules)
	}
	if w := doRequest(e.r, http.MethodDelete, fmt.Sprintf("/categories/%d", study.ID), ""); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != "category_invalid" {
		t.Errorf("a default can be renamed but not deleted: %d %s", w.Code, w.Body.String())
	}
	if w := doRequest(e.r, http.MethodDelete, path, ""); w.Code != http.StatusNotFound {
		t.Errorf("deleted: %d", w.Code)
	}
}

func TestBlocks_CategoryIDIsCheckedAndCarriesTheKind(t *testing.T) {
	e := newTaskEnv(t)
	gym := categoryByKey(listCategories(t, e, ""), "exercise")
	otherGym := categoryByKey(listCategories(t, e, "2"), "exercise")
	body := `{"name":"Lift","categoryId":%d,"startTime":"18:00","endTime":"19:00","energy":3,"date":"2026-10-29"}`
	if w := doRequest(e.r, http.MethodPost, "/tasks", fmt.Sprintf(body, otherGym.ID)); w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != "category_invalid" {
		t.Fatalf("another user's category: %d %s", w.Code, w.Body.String())
	}
	if w := doRequest(e.r, http.MethodPost, "/tasks", `{"name":"Lift","startTime":"18:00","endTime":"19:00","energy":3,"date":"2026-10-29"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a block needs categoryId or category: %d", w.Code)
	}
	_, id := e.create(t, fmt.Sprintf(body, gym.ID))
	w := doRequest(e.r, http.MethodGet, "/tasks?date=2026-10-29", "")
	contracttest.Validate(t, "blocks", "block_list", w.Body.Bytes())
	var list struct{ Tasks []models.Task }
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	b := list.Tasks[0]
	if b.CategoryID == nil || *b.CategoryID != gym.ID || b.CategoryKind == nil || *b.CategoryKind != "exercise" || b.Category != "gym" {
		t.Fatalf("categoryId, kind and the legacy string: %+v", b)
	}
	// The legacy string still works (the previous web version): the trigger finds the category.
	_, legacy := e.create(t, taskJSON("Old", "07:00", "08:00", "2026-10-29"))
	var cat uint
	e.db.Raw(`SELECT category_id FROM tasks WHERE id = ?`, legacy).Scan(&cat)
	if dw := categoryByKey(listCategories(t, e, ""), "deep_work"); cat != dw.ID {
		t.Fatalf("legacy deep-work resolves to the Deep Work default, got %d", cat)
	}
	// Changing the category of a block by id.
	study := categoryByKey(listCategories(t, e, ""), "study")
	w = doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", id), fmt.Sprintf(`{"categoryId":%d}`, study.ID))
	if b := blockFrom(t, w); b.CategoryID == nil || *b.CategoryID != study.ID || *b.CategoryKind != "study" || b.Category != "study" {
		t.Fatalf("update categoryId: %d %+v", w.Code, b)
	}
	if w := doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", id), fmt.Sprintf(`{"categoryId":%d}`, otherGym.ID)); codeOf(w.Body.Bytes()) != "category_invalid" {
		t.Fatalf("update with another user's category: %s", w.Body.String())
	}
}

func TestBlocks_ALegacyBlockWithoutCategoryGetsPersonalWhenTouched(t *testing.T) {
	e := newTaskEnv(t)
	mustExecDB(t, e, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (?, 'blank', '', '09:00', '10:00', '2026-10-29')`, e.ids[0])
	var id uint
	e.db.Raw(`SELECT id FROM tasks WHERE name = 'blank'`).Scan(&id)
	b := blockFrom(t, doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", id), `{"name":"renamed"}`))
	personal := categoryByKey(listCategories(t, e, ""), "personal")
	if b.CategoryID == nil || *b.CategoryID != personal.ID {
		t.Fatalf("a block without a category gets Personal, got %+v", b)
	}
}
