package dbmigrate_test

import (
	"context"
	"testing"

	"discipline-backend/internal/dbmigrate"
	"discipline-backend/internal/testdb"
)

// F13 review M2: during the deploy the previous API changes a block's legacy string with an
// UPDATE that leaves category_id as it was; the trigger must move the block to the matching
// category, and must leave alone an update that sets category_id itself (the new API).
func TestF13TriggerFollowsALegacyCategoryChange(t *testing.T) {
	ctx := context.Background()
	db := testdb.NewEmpty(t)
	if err := dbmigrate.UpTo(ctx, db, 15); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO users (id, name, email, password) VALUES (1, 'Ann', 'ann@example.com', 'x')`)
	mustExec(t, db, `INSERT INTO tasks (user_id, name, category, start_time, end_time, date) VALUES (1, 'b', 'gym', '09:00', '10:00', '2026-10-30')`)
	if c := blockCategories(t, db)["b"]; c == nil || c.kind != "exercise" {
		t.Fatalf("insert resolved: %+v", c)
	}
	mustExec(t, db, `UPDATE tasks SET category = 'study' WHERE name = 'b'`) // the previous API
	if c := blockCategories(t, db)["b"]; c == nil || c.kind != "study" {
		t.Fatalf("a legacy change moves the block to Study, got %+v", c)
	}
	// The new API sends the id with the string: the trigger keeps the id it was given.
	mustExec(t, db, `INSERT INTO categories (id, user_id, name, color, icon, kind) VALUES (50, 1, 'Reading', '#8B5CF6', 'book-open', 'study')`)
	mustExec(t, db, `UPDATE tasks SET category = 'study', category_id = 50 WHERE name = 'b'`)
	if c := blockCategories(t, db)["b"]; c == nil || c.name != "Reading" {
		t.Fatalf("an explicit category_id wins, got %+v", c)
	}
	// Down restores 00014's function; the column and the data stay.
	if err := dbmigrate.DownTo(ctx, db, 14); err != nil {
		t.Fatal(err)
	}
	if c := blockCategories(t, db)["b"]; c == nil || c.name != "Reading" {
		t.Fatalf("down keeps the data, got %+v", c)
	}
}
