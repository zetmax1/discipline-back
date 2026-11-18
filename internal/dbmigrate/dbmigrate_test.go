package dbmigrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"discipline-backend/internal/dbmigrate"
	"discipline-backend/internal/testdb"
)

type column struct{ table, name, dataType, nullable, def string }

func columns(t *testing.T, db *sql.DB) []column {
	t.Helper()
	rows, err := db.Query(`
		SELECT table_name, column_name, data_type, is_nullable, coalesce(column_default, '')
		  FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name IN ('users','tasks','vocabulary_words')
		 ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.table, &c.name, &c.dataType, &c.nullable, &c.def); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func indexes(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	// goose_db_version is goose's bookkeeping table, not application schema.
	rows, err := db.Query(`SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename <> 'goose_db_version'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		out[name] = def
	}
	return out
}

func version(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	v, err := dbmigrate.Version(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// loadGormSchema recreates the exact schema GORM AutoMigrate built before migrations existed.
func loadGormSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	ddl, err := os.ReadFile("testdata/gorm_automigrate_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatalf("load GORM schema: %v", err)
	}
}

func TestBaselineMatchesGormAutoMigrateSchema(t *testing.T) {
	ctx := context.Background()
	want := testdb.NewEmpty(t)
	loadGormSchema(t, want)

	got := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, got, 1); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	wc, gc := columns(t, want), columns(t, got)
	if len(wc) != len(gc) {
		t.Fatalf("column count differs: gorm=%d baseline=%d", len(wc), len(gc))
	}
	for i := range wc {
		if wc[i] != gc[i] {
			t.Errorf("column mismatch:\n gorm     %+v\n baseline %+v", wc[i], gc[i])
		}
	}
	wi, gi := indexes(t, want), indexes(t, got)
	for name, def := range wi {
		if gi[name] != def {
			t.Errorf("index %s: gorm %q, baseline %q", name, def, gi[name])
		}
	}
	if len(wi) != len(gi) {
		t.Errorf("index sets differ: gorm %v, baseline %v", wi, gi)
	}
}

func TestBaselineIsNoOpOnExistingGormDatabase(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	loadGormSchema(t, db)
	if _, err := db.Exec(`INSERT INTO users (name, email, password) VALUES ('Ann', 'ann@example.com', 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := dbmigrate.UpTo(ctx, db, 1); err != nil {
		t.Fatalf("baseline must succeed on an existing GORM schema: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("baseline must not touch existing rows, users=%d err=%v", n, err)
	}
}

func TestUpDownUp(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)

	if err := dbmigrate.Up(ctx, db); err != nil {
		t.Fatalf("up: %v", err)
	}
	latest, err := dbmigrate.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, db); v != latest {
		t.Fatalf("expected version %d after up, got %d", latest, v)
	}
	if err := dbmigrate.RequireLatest(ctx, db); err != nil {
		t.Fatalf("RequireLatest after up: %v", err)
	}

	if err := dbmigrate.DownTo(ctx, db, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if v := version(t, db); v != 0 {
		t.Fatalf("expected version 0 after full down, got %d", v)
	}
	if cols := columns(t, db); len(cols) != 0 {
		t.Fatalf("full down must drop all app tables, still have %d columns", len(cols))
	}
	if err := dbmigrate.RequireLatest(ctx, db); err == nil {
		t.Fatal("RequireLatest must fail on an unmigrated database")
	}

	if err := dbmigrate.Up(ctx, db); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if v := version(t, db); v != latest {
		t.Fatalf("expected version %d after second up, got %d", latest, v)
	}
}

func TestEveryMigrationStepsDownAndUp(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	latest, _ := dbmigrate.Latest()
	for v := int64(1); v <= latest; v++ {
		if err := dbmigrate.UpTo(ctx, db, v); err != nil {
			t.Fatalf("up to %d: %v", v, err)
		}
		if err := dbmigrate.DownTo(ctx, db, v-1); err != nil {
			t.Fatalf("down from %d: %v", v, err)
		}
		if err := dbmigrate.UpTo(ctx, db, v); err != nil {
			t.Fatalf("re-up to %d: %v", v, err)
		}
	}
}

func TestTypedTaskColumnsConvertAndRevert(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 1); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date)
	                 VALUES (1, 'Late', 'study', '9:30', '24:00', '2026-10-29')`)

	if err := dbmigrate.UpTo(ctx, db, 2); err != nil {
		t.Fatalf("typed columns migration: %v", err)
	}
	var date, start, end, dateType string
	mustScan(t, db, `SELECT date::text, start_time::text, end_time::text, pg_typeof(date)::text FROM tasks`, &date, &start, &end, &dateType)
	if date != "2026-10-29" || start != "09:30:00" || end != "24:00:00" || dateType != "date" {
		t.Fatalf("unexpected converted row: %s %s %s (%s)", date, start, end, dateType)
	}

	if err := dbmigrate.DownTo(ctx, db, 1); err != nil {
		t.Fatalf("down: %v", err)
	}
	mustScan(t, db, `SELECT date, start_time, end_time, pg_typeof(date)::text FROM tasks`, &date, &start, &end, &dateType)
	if date != "2026-10-29" || start != "09:30" || end != "24:00" || dateType != "text" {
		t.Fatalf("down must restore text columns, got %s %s %s (%s)", date, start, end, dateType)
	}
}

func TestTypedTaskColumnsMigrationAbortsAndListsBadRows(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 1); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO tasks (id, user_id, name, category, start_time, end_time, date) VALUES
		(10, 1, 'ok',        'study', '09:00', '10:00', '2026-10-29'),
		(11, 1, 'bad date',  'study', '09:00', '10:00', '2026-02-30'),
		(12, 1, 'us date',   'study', '09:00', '10:00', '10/29/2026'),
		(13, 1, 'bad start', 'study', '9am',   '10:00', '2026-10-29'),
		(14, 1, 'bad end',   'study', '09:00', '25:00', '2026-10-29'),
		(15, 999, 'orphan',  'study', '09:00', '10:00', '2026-10-29')`)
	mustExec(t, db, `INSERT INTO vocabulary_words (id, user_id, word, language, definition, date)
	                 VALUES (20, 888, 'x', 'english', 'd', '2026-10-29')`)

	err := dbmigrate.UpTo(ctx, db, 2)
	if err == nil {
		t.Fatal("migration must abort when rows cannot be converted")
	}
	msg := err.Error()
	for _, want := range []string{
		"tasks.id=11", "2026-02-30",
		"tasks.id=12", "10/29/2026",
		"tasks.id=13", "9am",
		"tasks.id=14", "25:00",
		"tasks.id=15", "user_id=999",
		"vocabulary_words.id=20", "user_id=888",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must list %q; got:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "tasks.id=10 ") {
		t.Errorf("valid row 10 must not be listed:\n%s", msg)
	}

	if v := version(t, db); v != 1 {
		t.Fatalf("aborted migration must leave version at 1, got %d", v)
	}
	var n int
	var dateType string
	mustScan(t, db, `SELECT count(*), min(pg_typeof(date)::text) FROM tasks`, &n, &dateType)
	if n != 6 || dateType != "text" {
		t.Fatalf("aborted migration must not change data: rows=%d type=%s", n, dateType)
	}
}

func TestIndexesAndCascadingForeignKeys(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	idx := indexes(t, db)
	if !strings.Contains(idx["idx_tasks_user_date"], "(user_id, date)") {
		t.Errorf("missing composite index on tasks(user_id, date): %v", idx)
	}

	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (1, 'a', 'study', '09:00', '10:00', '2026-10-29')`)
	mustExec(t, db, `INSERT INTO vocabulary_words (user_id, word, language, definition, date) VALUES (1, 'w', 'english', 'd', '2026-10-29')`)
	if _, err := db.Exec(`INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (42, 'a', 'study', '09:00', '10:00', '2026-10-29')`); err == nil {
		t.Error("tasks.user_id must reference users")
	}
	mustExec(t, db, `DELETE FROM users WHERE id = 1`)
	var tasks, words int
	mustScan(t, db, `SELECT (SELECT count(*) FROM tasks), (SELECT count(*) FROM vocabulary_words)`, &tasks, &words)
	if tasks != 0 || words != 0 {
		t.Fatalf("deleting a user must cascade, left tasks=%d words=%d", tasks, words)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func mustScan(t *testing.T, db *sql.DB, q string, dest ...any) {
	t.Helper()
	if err := db.QueryRow(q).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestUserTimezoneColumnBackfillsExistingUsers(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 2); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	if err := dbmigrate.UpTo(ctx, db, 3); err != nil {
		t.Fatalf("timezone migration: %v", err)
	}
	var tz, nullable string
	mustScan(t, db, `SELECT timezone, (SELECT is_nullable FROM information_schema.columns
	                  WHERE table_name='users' AND column_name='timezone') FROM users WHERE id = 1`, &tz, &nullable)
	if tz != "Asia/Tashkent" || nullable != "NO" {
		t.Fatalf("existing users must get Asia/Tashkent and the column must be NOT NULL; got %q nullable=%s", tz, nullable)
	}
}

func TestVocabularyDedupeKeepsOldestAndMergesFlags(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 3); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'a@example.com', 'x'), (2, 'Bob', 'b@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO vocabulary_words (id, user_id, word, language, definition, date, learned, saved) VALUES
		(1, 1, 'first',  'english', 'd', '2026-10-29', false, true),
		(2, 1, 'second', 'english', 'd', '2026-10-29', true,  false),
		(3, 1, 'third',  'english', 'd', '2026-10-29', false, false),
		(4, 1, 'слово',  'russian', 'd', '2026-10-29', false, false),
		(5, 2, 'first',  'english', 'd', '2026-10-29', false, false),
		(6, 1, 'other',  'english', 'd', '2026-10-30', false, false)`)

	if err := dbmigrate.UpTo(ctx, db, 4); err != nil {
		t.Fatalf("dedupe migration: %v", err)
	}
	var ids string
	mustScan(t, db, `SELECT string_agg(id::text, ',' ORDER BY id) FROM vocabulary_words`, &ids)
	if ids != "1,4,5,6" {
		t.Fatalf("expected the oldest duplicate (1) and all unique rows to remain, got %s", ids)
	}
	var learned, saved bool
	var word string
	mustScan(t, db, `SELECT word, learned, saved FROM vocabulary_words WHERE id = 1`, &word, &learned, &saved)
	if word != "first" || !learned || !saved {
		t.Fatalf("kept row must keep its word and merge learned/saved from duplicates; got %s learned=%v saved=%v", word, learned, saved)
	}
	if _, err := db.Exec(`INSERT INTO vocabulary_words (user_id, word, language, definition, date) VALUES (1, 'dup', 'english', 'd', '2026-10-29')`); err == nil {
		t.Fatal("a duplicate (user_id, date, language) must be rejected by the unique index")
	}
}

func TestTokenVersionColumn(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 4); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	if err := dbmigrate.UpTo(ctx, db, 5); err != nil {
		t.Fatalf("token_version migration: %v", err)
	}
	var v int64
	var nullable string
	mustScan(t, db, `SELECT token_version, (SELECT is_nullable FROM information_schema.columns
	                  WHERE table_name='users' AND column_name='token_version') FROM users WHERE id = 1`, &v, &nullable)
	if v != 0 || nullable != "NO" {
		t.Fatalf("existing users must start at token_version 0 (NOT NULL), got %d nullable=%s", v, nullable)
	}
}

func TestTaskSourceAndPlanColumns(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 5); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (1, 'old', 'study', '09:00', '10:00', '2026-10-29')`)
	if err := dbmigrate.UpTo(ctx, db, 6); err != nil {
		t.Fatalf("source/plan_id migration: %v", err)
	}
	var source string
	var planNull bool
	mustScan(t, db, `SELECT source, plan_id IS NULL FROM tasks`, &source, &planNull)
	if source != "manual" || !planNull {
		t.Fatalf("existing tasks must become manual with no plan, got %q plan_null=%v", source, planNull)
	}
	if _, err := db.Exec(`UPDATE tasks SET source = 'imported'`); err == nil {
		t.Fatal("source must be limited to manual / smart-schedule")
	}
}

func TestVocabularyDateColumnConvertsAndReverts(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 6); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO vocabulary_words (user_id, word, language, definition, date) VALUES (1, 'w', 'english', 'd', '2026-10-29')`)

	if err := dbmigrate.UpTo(ctx, db, 7); err != nil {
		t.Fatalf("vocabulary date migration: %v", err)
	}
	var date, dateType string
	mustScan(t, db, `SELECT date::text, pg_typeof(date)::text FROM vocabulary_words`, &date, &dateType)
	if date != "2026-10-29" || dateType != "date" {
		t.Fatalf("expected DATE 2026-10-29, got %s (%s)", date, dateType)
	}
	if _, err := db.Exec(`INSERT INTO vocabulary_words (user_id, word, language, definition, date) VALUES (1, 'dup', 'english', 'd', '2026-10-29')`); err == nil {
		t.Fatal("the (user_id, date, language) unique index must survive the type change")
	}

	if err := dbmigrate.DownTo(ctx, db, 6); err != nil {
		t.Fatalf("down: %v", err)
	}
	mustScan(t, db, `SELECT date, pg_typeof(date)::text FROM vocabulary_words`, &date, &dateType)
	if date != "2026-10-29" || dateType != "text" {
		t.Fatalf("down must restore the text column, got %s (%s)", date, dateType)
	}
}

func TestVocabularyDateMigrationAbortsAndListsBadRows(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 6); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO vocabulary_words (id, user_id, word, language, definition, date) VALUES
		(10, 1, 'ok',  'english', 'd', '2026-10-29'),
		(11, 1, 'bad', 'english', 'd', '2026-02-30'),
		(12, 1, 'us',  'russian', 'd', '10/29/2026')`)

	err := dbmigrate.UpTo(ctx, db, 7)
	if err == nil {
		t.Fatal("migration must abort when a date cannot be converted")
	}
	msg := err.Error()
	for _, want := range []string{"vocabulary_words.id=11", "2026-02-30", "vocabulary_words.id=12", "10/29/2026"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error must list %q; got:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "vocabulary_words.id=10 ") {
		t.Errorf("valid row 10 must not be listed:\n%s", msg)
	}
	if v := version(t, db); v != 6 {
		t.Fatalf("aborted migration must leave version at 6, got %d", v)
	}
	var n int
	var dateType string
	mustScan(t, db, `SELECT count(*), min(pg_typeof(date)::text) FROM vocabulary_words`, &n, &dateType)
	if n != 3 || dateType != "text" {
		t.Fatalf("aborted migration must not change data: rows=%d type=%s", n, dateType)
	}
}

// A production-shaped copy: every legacy status, a 24:00 end, a 09:30 start and an inverted legacy row.
func TestBlockLifecycleMigrationKeepsEveryBlock(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 7); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES
		(1, 'Ann', 'ann@example.com', 'x'),
		(2, 'Bob', 'bob@example.com', 'x')`)
	// Production-shaped rows: both users, manual and plan-generated blocks (source and plan_id
	// come from 00006; the CHECK there spells the plan source 'smart-schedule'), 00:00 starts,
	// a 24:00 end and a legacy inverted row.
	mustExec(t, db, `INSERT INTO tasks (id, user_id, name, category, start_time, end_time, date, status, distractions, energy) VALUES
		(1, 1, 'Late study', 'study', '09:30', '24:00', '2026-10-29', 'pending', 0, 3),
		(2, 1, 'Deep', 'deep-work', '08:00', '09:00', '2026-10-28', 'completed', 2, 5),
		(3, 1, 'Gym', 'gym', '17:00', '18:30', '2026-10-28', 'skipped', 0, 3),
		(4, 1, 'Legacy', 'admin', '10:00', '09:00', '2026-10-27', 'pending', 0, 3)`)
	mustExec(t, db, `INSERT INTO tasks (id, user_id, name, category, start_time, end_time, date, status, source, plan_id) VALUES
		(10, 1, 'Night shift', 'study', '00:00', '01:00', '2026-10-30', 'pending', 'smart-schedule', '11111111-1111-1111-1111-111111111111'),
		(11, 2, 'Whole day', 'deep-work', '00:00', '24:00', '2026-10-30', 'pending', 'smart-schedule', '11111111-1111-1111-1111-111111111111'),
		(12, 2, 'Early run', 'gym', '06:00', '07:00', '2026-10-29', 'completed', 'manual', NULL),
		(13, 2, 'Late plan', 'admin', '22:00', '24:00', '2026-10-28', 'skipped', 'smart-schedule', '22222222-2222-2222-2222-222222222222')`)

	if err := dbmigrate.UpTo(ctx, db, 8); err != nil {
		t.Fatalf("block lifecycle migration: %v", err)
	}
	want := map[int]string{
		1:  "u1 2026-10-29 570 1440 planned study manual -",
		2:  "u1 2026-10-28 480 540 done deep-work manual -",
		3:  "u1 2026-10-28 1020 1110 skipped gym manual -",
		4:  "u1 2026-10-27 600 540 planned admin manual -",
		10: "u1 2026-10-30 0 60 planned study smart-schedule 11111111-1111-1111-1111-111111111111",
		11: "u2 2026-10-30 0 1440 planned deep-work smart-schedule 11111111-1111-1111-1111-111111111111",
		12: "u2 2026-10-29 360 420 done gym manual -",
		13: "u2 2026-10-28 1320 1440 skipped admin smart-schedule 22222222-2222-2222-2222-222222222222",
	}
	seen := 0
	rows, err := db.Query(`SELECT id, user_id, date::text, start_min, end_min, status, category, source, coalesce(plan_id::text, '-') FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, user, s, e int
		var date, status, category, source, planID string
		if err := rows.Scan(&id, &user, &date, &s, &e, &status, &category, &source, &planID); err != nil {
			t.Fatal(err)
		}
		seen++
		if got := fmt.Sprintf("u%d %s %d %d %s %s %s %s", user, date, s, e, status, category, source, planID); got != want[id] {
			t.Errorf("task %d: want %q, got %q", id, want[id], got)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != len(want) {
		t.Errorf("the migration must keep every block: want %d rows, got %d", len(want), seen)
	}
	if _, err := db.Exec(`UPDATE tasks SET status = 'in-progress' WHERE id = 1`); err == nil {
		t.Error("only the new statuses are allowed after the migration")
	}
	if _, err := db.Exec(`UPDATE tasks SET focus_rating = 6 WHERE id = 2`); err == nil {
		t.Error("focus rating must be 1-5")
	}
	if _, err := db.Exec(`UPDATE tasks SET end_min = 1441 WHERE id = 2`); err == nil {
		t.Error("minutes must stay within 0-1440")
	}
	mustExec(t, db, `UPDATE tasks SET status = 'partial', actual_start_min = 480, actual_end_min = 510 WHERE id = 2`)
	mustExec(t, db, `INSERT INTO tasks (id, user_id, name, category, start_min, end_min, date, status, moved_from_id)
		VALUES (5, 1, 'Moved', 'study', 600, 1440, '2026-10-30', 'planned', 1)`)
	var endTime string
	mustScan(t, db, `SELECT end_time::text FROM tasks WHERE id = 5`, &endTime)
	if endTime != "24:00:00" {
		t.Errorf("minutes-only writes must fill the TIME columns too, got %s", endTime)
	}
	// The previous API version (still running during a deploy) writes TIME columns and old statuses.
	mustExec(t, db, `INSERT INTO tasks (id, user_id, name, category, start_time, end_time, date, status)
		VALUES (6, 1, 'Old API', 'study', '07:15', '08:00', '2026-10-30', 'pending')`)
	mustExec(t, db, `UPDATE tasks SET status = 'completed', start_time = '07:30' WHERE id = 6`)
	var oldStart, oldEnd int
	var oldStatus string
	mustScan(t, db, `SELECT start_min, end_min, status FROM tasks WHERE id = 6`, &oldStart, &oldEnd, &oldStatus)
	if oldStart != 450 || oldEnd != 480 || oldStatus != "done" {
		t.Errorf("old API writes must be translated, got %d %d %s", oldStart, oldEnd, oldStatus)
	}
	mustExec(t, db, `UPDATE tasks SET status = 'moved' WHERE id = 1`)

	if err := dbmigrate.DownTo(ctx, db, 7); err != nil {
		t.Fatalf("down: %v", err)
	}
	var statuses string
	mustScan(t, db, `SELECT string_agg(status, ',' ORDER BY id) FROM tasks`, &statuses)
	if statuses != "skipped,completed,skipped,pending,pending,completed,pending,pending,completed,skipped" {
		t.Fatalf("down must map statuses back, got %s", statuses)
	}
	// Neither direction may touch which plan a block came from, or the other user's rows.
	var plans string
	mustScan(t, db, `SELECT string_agg(source || ':' || coalesce(plan_id::text, '-'), ',' ORDER BY id) FROM tasks WHERE user_id = 2`, &plans)
	const wantPlans = "smart-schedule:11111111-1111-1111-1111-111111111111,manual:-," +
		"smart-schedule:22222222-2222-2222-2222-222222222222"
	if plans != wantPlans {
		t.Errorf("plan source must survive up and down: want %q, got %q", wantPlans, plans)
	}
}

func TestBlockLifecycleMigrationAbortsOnUnknownStatus(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 7); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO tasks (id, user_id, name, category, start_time, end_time, date, status)
		VALUES (9, 1, 'Odd', 'study', '09:00', '10:00', '2026-10-29', 'in-progress')`)
	err := dbmigrate.UpTo(ctx, db, 8)
	if err == nil || !strings.Contains(err.Error(), "tasks.id=9") {
		t.Fatalf("want the migration to abort and name the row, got %v", err)
	}
	var status string
	mustScan(t, db, `SELECT status FROM tasks WHERE id = 9`, &status)
	if status != "in-progress" {
		t.Fatalf("nothing may change when the migration aborts, got %q", status)
	}
}
