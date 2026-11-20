package dbmigrate_test

import (
	"context"
	"database/sql"
	"testing"

	"discipline-backend/internal/dbmigrate"
	"discipline-backend/internal/testdb"
)

// f13Fixture is production-shaped legacy data at version 13: two users, the F1 strings, a
// custom one, "fixed", "Personal", a padded "rest " and mixed case.
func f13Fixture(t *testing.T, db *sql.DB, order string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x'), (2, 'Bo', 'bo@example.com', 'x')`)
	rows := []string{
		`(1, 'a', 'deep-work')`, `(1, 'b', 'study')`, `(1, 'c', 'admin')`, `(1, 'd', 'gym')`,
		`(1, 'e', 'rest ')`, `(1, 'f', 'fixed')`, `(1, 'g', 'Personal')`, `(1, 'h', 'Reading')`,
		`(1, 'i', 'reading')`, `(1, 'j', 'Exercise')`, `(1, 'k', '')`,
		`(2, 'l', 'Deep-Work')`, `(2, 'm', 'Reading')`,
	}
	if order == "reverse" {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	for _, r := range rows {
		mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date)
			SELECT v.u, v.n, v.c, '09:00', '10:00', '2026-10-29' FROM (VALUES `+r+`) AS v(u, n, c)`)
	}
}

type catOf struct {
	name, kind string
	systemKey  sql.NullString
}

// blockCategories maps each block name to the category it points at.
func blockCategories(t *testing.T, db *sql.DB) map[string]*catOf {
	t.Helper()
	rows, err := db.Query(`SELECT t.name, c.name, c.kind, c.system_key FROM tasks t LEFT JOIN categories c ON c.id = t.category_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]*catOf{}
	for rows.Next() {
		var block string
		var name, kind, key sql.NullString
		if err := rows.Scan(&block, &name, &kind, &key); err != nil {
			t.Fatal(err)
		}
		if name.Valid {
			out[block] = &catOf{name: name.String, kind: kind.String, systemKey: key}
		} else {
			out[block] = nil
		}
	}
	return out
}

func TestF13BackfillKeepsEveryCategoryAndIsOrderIndependent(t *testing.T) {
	for _, order := range []string{"forward", "reverse"} {
		t.Run(order, func(t *testing.T) {
			ctx := context.Background()
			db := testdb.NewEmpty(t)
			if err := dbmigrate.UpTo(ctx, db, 13); err != nil {
				t.Fatal(err)
			}
			f13Fixture(t, db, order)
			if err := dbmigrate.UpTo(ctx, db, 14); err != nil {
				t.Fatalf("00014: %v", err)
			}
			got := blockCategories(t, db)
			want := map[string]struct{ name, kind, key string }{
				"a": {"Deep Work", "focus", "deep_work"}, "b": {"Study", "study", "study"}, "c": {"Admin", "admin", "admin"},
				"d": {"Gym", "exercise", "exercise"}, "e": {"Rest", "rest", "rest"}, "f": {"Fixed", "personal", ""},
				"g": {"Personal", "personal", "personal"}, "h": {"Reading", "personal", ""}, "i": {"Reading", "personal", ""},
				"j": {"Gym", "exercise", "exercise"}, "l": {"Deep Work", "focus", "deep_work"}, "m": {"Reading", "personal", ""},
			}
			for block, w := range want {
				c := got[block]
				if c == nil || c.name != w.name || c.kind != w.kind || c.systemKey.String != w.key {
					t.Errorf("block %s: want %+v, got %+v", block, w, c)
				}
			}
			if got["k"] != nil {
				t.Errorf("an empty legacy string stays NULL until the API touches it, got %+v", got["k"])
			}
			// "Reading" and "reading" share one category; gym and Exercise share Gym.
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM categories WHERE user_id = 1`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 8 { // Deep Work, Study, Admin, Gym, Rest, Fixed, Personal, Reading
				t.Errorf("user 1 categories: want 8, got %d", n)
			}
			var legacy string
			if err := db.QueryRow(`SELECT category FROM tasks WHERE name = 'e'`).Scan(&legacy); err != nil || legacy != "rest " {
				t.Errorf("tasks.category is never rewritten, got %q (%v)", legacy, err)
			}
		})
	}
}

func TestF13TriggerFillsCategoryForTheOldAPI(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 13); err != nil {
		t.Fatal(err)
	}
	f13Fixture(t, db, "forward")
	if err := dbmigrate.UpTo(ctx, db, 14); err != nil {
		t.Fatal(err)
	}
	// The user renamed Gym in the new UI; the old API still writes "gym".
	mustExec(t, db, `UPDATE categories SET name = 'Workout', name_custom = true WHERE user_id = 1 AND system_key = 'exercise'`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (1, 'old-gym', 'gym', '11:00', '12:00', '2026-10-30')`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (1, 'old-read', 'READING', '12:00', '13:00', '2026-10-30')`)
	// A user created after 00014 has no categories yet.
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (3, 'Cy', 'cy@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (3, 'new-user', 'study', '09:00', '10:00', '2026-10-30')`)

	got := blockCategories(t, db)
	if c := got["old-gym"]; c == nil || c.name != "Workout" {
		t.Errorf("a renamed default is found by system_key, got %+v", c)
	}
	if c := got["old-read"]; c == nil || c.name != "Reading" {
		t.Errorf("a custom category is found case-insensitively, got %+v", c)
	}
	if c := got["new-user"]; c == nil || c.kind != "study" || c.systemKey.String != "study" {
		t.Errorf("a new user gets a category made for them, got %+v", c)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM categories WHERE user_id = 1`).Scan(&n); err != nil || n != 8 {
		t.Errorf("no duplicate category, want 8, got %d (%v)", n, err)
	}
	var legacy string
	if err := db.QueryRow(`SELECT category FROM tasks WHERE name = 'old-gym'`).Scan(&legacy); err != nil || legacy != "gym" {
		t.Errorf("the trigger never writes tasks.category, got %q", legacy)
	}
}

func TestF13ConstraintsAndUserDeletion(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 14); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO categories (id, user_id, name, color, icon, kind) VALUES (10, 1, 'Gym', '#10B981', 'dumbbell', 'exercise')`)
	for _, bad := range []string{
		`INSERT INTO categories (user_id, name, color, icon, kind) VALUES (1, 'gym', '#10B981', 'dumbbell', 'exercise')`, // name taken (case)
		`INSERT INTO categories (user_id, name, color, icon, kind) VALUES (1, 'X', '#10b981', 'dumbbell', 'exercise')`,   // lower-case hex
		`INSERT INTO categories (user_id, name, color, icon, kind) VALUES (1, 'X', '#10B981', 'rocket', 'exercise')`,     // icon
		`INSERT INTO categories (user_id, name, color, icon, kind) VALUES (1, 'X', '#10B981', 'dumbbell', 'sleep')`,      // kind
		`INSERT INTO recurring_rules (user_id, category_id, name, weekdays, start_min, end_min, valid_from) VALUES (1, 10, 'r', '{}', 60, 120, '2026-11-02')`,
		`INSERT INTO recurring_rules (user_id, category_id, name, weekdays, start_min, end_min, valid_from) VALUES (1, 10, 'r', '{0}', 60, 120, '2026-11-02')`,
		`INSERT INTO recurring_rules (user_id, category_id, name, weekdays, start_min, end_min, valid_from) VALUES (1, 10, 'r', '{8}', 60, 120, '2026-11-02')`,
		`INSERT INTO recurring_rules (user_id, category_id, name, weekdays, start_min, end_min, valid_from) VALUES (1, 10, 'r', '{1,NULL}', 60, 120, '2026-11-02')`,
		`INSERT INTO recurring_rules (user_id, category_id, name, weekdays, start_min, end_min, valid_from) VALUES (1, 10, 'r', '{1}', 120, 120, '2026-11-02')`,
		`INSERT INTO recurring_rules (user_id, category_id, name, weekdays, start_min, end_min, valid_from, valid_to) VALUES (1, 10, 'r', '{1}', 60, 120, '2026-11-02', '2026-11-01')`,
	} {
		if _, err := db.Exec(bad); err == nil {
			t.Errorf("must be rejected: %s", bad)
		}
	}
	mustExec(t, db, `INSERT INTO recurring_rules (id, user_id, category_id, name, weekdays, start_min, end_min, valid_from) VALUES (5, 1, 10, 'Gym', '{1,3,5}', 1080, 1170, '2026-11-02')`)
	mustExec(t, db, `INSERT INTO recurring_exceptions (user_id, rule_id, date, reason) VALUES (1, 5, '2026-11-04', 'cancelled')`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, category_id, recurring_rule_id, source, start_time, end_time, date) VALUES (1, 'Gym', 'gym', 10, 5, 'recurring', '18:00', '19:30', '2026-11-02')`)
	if _, err := db.Exec(`DELETE FROM categories WHERE id = 10`); err == nil {
		t.Error("a category in use cannot be deleted on its own")
	}
	if _, err := db.Exec(`INSERT INTO recurring_exceptions (user_id, rule_id, date, reason) VALUES (1, 5, '2026-11-06', 'skipped')`); err == nil {
		t.Error("an exception is never 'skipped'")
	}
	// NO ACTION, not RESTRICT: deleting the user removes everything in one statement.
	mustExec(t, db, `DELETE FROM users WHERE id = 1`)
	for _, table := range []string{"categories", "recurring_rules", "recurring_exceptions", "tasks"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s left after deleting the user: %d (%v)", table, n, err)
		}
	}
}

func TestF13DownRestoresThePreviousSchema(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 13); err != nil {
		t.Fatal(err)
	}
	f13Fixture(t, db, "forward")
	before := columns(t, db)
	if err := dbmigrate.UpTo(ctx, db, 14); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, source, start_time, end_time, date) VALUES (1, 'occ', 'gym', 'recurring', '18:00', '19:00', '2026-11-02')`)
	if err := dbmigrate.DownTo(ctx, db, 13); err != nil {
		t.Fatalf("down: %v", err)
	}
	after := columns(t, db)
	if len(after) != len(before) {
		t.Fatalf("columns differ after down: %d vs %d", len(after), len(before))
	}
	var source string
	if err := db.QueryRow(`SELECT source FROM tasks WHERE name = 'occ'`).Scan(&source); err != nil || source != "manual" {
		t.Errorf("recurring blocks fall back to manual, got %q (%v)", source, err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (user_id, name, category, source, start_time, end_time, date) VALUES (1, 'x', 'gym', 'recurring', '18:00', '19:00', '2026-11-02')`); err == nil {
		t.Error("'recurring' is not a source before 00014")
	}
}
