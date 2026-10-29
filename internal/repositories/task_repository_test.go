package repositories

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"discipline-backend/internal/models"
	"discipline-backend/internal/testdb"
)

func seedUser(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	u := &models.User{Name: "Ann", Email: "ann@example.com", Password: "x", Role: "user"}
	if err := db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestTaskRepository_TypedColumnsRoundTrip(t *testing.T) {
	db, _ := testdb.NewMigrated(t)
	uid := seedUser(t, db)
	repo := NewTaskRepository(db)

	task := &models.Task{UserID: uid, Name: "Late", Category: "study", StartTime: "21:30", EndTime: "24:00", Date: "2026-10-29", Status: "pending", Energy: 3}
	if err := repo.CreateTasks(uid, []*models.Task{task}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.FindByUserAndDate(uid, "2026-10-29")
	if err != nil || len(got) != 1 {
		t.Fatalf("find: %v %v", got, err)
	}
	if got[0].Date != "2026-10-29" || got[0].StartTime != "21:30" || got[0].EndTime != "24:00" {
		t.Fatalf("round trip changed values: %+v", got[0])
	}
}

func TestTaskRepository_DeleteReportsDBErrors(t *testing.T) {
	db, sqlDB := testdb.NewMigrated(t)
	repo := NewTaskRepository(db)

	if err := repo.Delete(12345, 1); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing row must be ErrRecordNotFound, got %v", err)
	}
	_ = sqlDB.Close()
	err := repo.Delete(12345, 1)
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("a DB failure must surface as a real error, not not-found; got %v", err)
	}
}
