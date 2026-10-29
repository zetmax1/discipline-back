package dbmigrate_test

import (
	"context"
	"database/sql"
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
