package dbmigrate_test

import (
	"context"
	"testing"

	"discipline-backend/internal/dbmigrate"
	"discipline-backend/internal/testdb"
)

// F9a migration 00016: commitments, weekly targets, calendar sources. Additive only.
func TestF9aMigrationTablesAndChecks(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 16); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO categories (id, user_id, name, color, icon, kind) VALUES (10, 1, 'Deep', '#3B82F6', 'target', 'focus')`)
	ok := `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source)
		VALUES (1, 'Sleep', 'rest', '{1,2,3,4,5,6,7}', 1380, 420, '2026-11-01', 'manual')`
	mustExec(t, db, ok)
	bad := map[string]string{
		"empty weekdays":      `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source) VALUES (1, 'x', 'rest', '{}', 60, 120, '2026-11-01', 'manual')`,
		"weekday 0":           `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source) VALUES (1, 'x', 'rest', '{0}', 60, 120, '2026-11-01', 'manual')`,
		"weekday 8":           `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source) VALUES (1, 'x', 'rest', '{8}', 60, 120, '2026-11-01', 'manual')`,
		"start = end":         `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source) VALUES (1, 'x', 'rest', '{1}', 60, 60, '2026-11-01', 'manual')`,
		"valid_to before":     `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, valid_to, source) VALUES (1, 'x', 'rest', '{1}', 60, 120, '2026-11-01', '2026-10-01', 'manual')`,
		"unknown kind":        `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source) VALUES (1, 'x', 'sleep', '{1}', 60, 120, '2026-11-01', 'manual')`,
		"empty name":          `INSERT INTO commitments (user_id, name, kind, weekdays, start_min, end_min, valid_from, source) VALUES (1, '', 'rest', '{1}', 60, 120, '2026-11-01', 'manual')`,
		"target not 15-step":  `INSERT INTO category_targets (user_id, category_id, minutes_per_week) VALUES (1, 10, 50)`,
		"target over 80 h":    `INSERT INTO category_targets (user_id, category_id, minutes_per_week) VALUES (1, 10, 4815)`,
		"max focus too small": `INSERT INTO weekly_targets (user_id, max_focus_min_per_day) VALUES (1, 45)`,
		"rest day 8":          `INSERT INTO weekly_targets (user_id, rest_day) VALUES (1, 8)`,
	}
	for name, q := range bad {
		if _, err := db.Exec(q); err == nil {
			t.Errorf("%s: expected the CHECK to reject it", name)
		}
	}
	mustExec(t, db, `INSERT INTO category_targets (user_id, category_id, minutes_per_week) VALUES (1, 10, 900)`)
	mustExec(t, db, `INSERT INTO weekly_targets (user_id, max_focus_min_per_day, rest_day) VALUES (1, 240, 7)`)
	mustExec(t, db, `INSERT INTO calendar_sources (user_id, url_ciphertext, url_fingerprint, url_host) VALUES (1, '\x00', '\x01', 'calendar.google.com')`)
	if _, err := db.Exec(`INSERT INTO calendar_sources (user_id, url_ciphertext, url_fingerprint, url_host) VALUES (1, '\x02', '\x01', 'calendar.google.com')`); err == nil {
		t.Error("the same URL twice for one user must be rejected (fingerprint)")
	}
	// Deleting a category drops its target (F13 answer 4: CASCADE).
	mustExec(t, db, `DELETE FROM categories WHERE id = 10`)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM category_targets`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("category target should go with its category, got %d (%v)", n, err)
	}
	// Deleting the account removes everything (export/delete principle).
	mustExec(t, db, `DELETE FROM users WHERE id = 1`)
	for _, table := range []string{"commitments", "weekly_targets", "calendar_sources"} {
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s should be empty after the user is deleted, got %d (%v)", table, n, err)
		}
	}
}

func TestF9aDownDropsOnlyTheNewTables(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 16); err != nil {
		t.Fatal(err)
	}
	if err := dbmigrate.DownTo(ctx, db, 15); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('commitments','weekly_targets','category_targets','calendar_sources')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("down must drop the four tables, %d left (%v)", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('tasks','categories','recurring_rules')`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("down must keep the earlier tables, found %d (%v)", n, err)
	}
}
