package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
	"discipline-backend/internal/testdb"
)

// sqlRecorder captures executed SQL so tests can assert which columns an UPDATE writes.
type sqlRecorder struct {
	mu   sync.Mutex
	stmt []string
}

func (r *sqlRecorder) LogMode(logger.LogLevel) logger.Interface    { return r }
func (r *sqlRecorder) Info(context.Context, string, ...interface{}) {}
func (r *sqlRecorder) Warn(context.Context, string, ...interface{}) {}
func (r *sqlRecorder) Error(context.Context, string, ...interface{}) {}
func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	s, _ := fc()
	r.mu.Lock()
	r.stmt = append(r.stmt, s)
	r.mu.Unlock()
}

type taskEnv struct {
	db  *gorm.DB
	rec *sqlRecorder
	r   *gin.Engine
	ids [2]uint
}

func newTaskEnv(t *testing.T) *taskEnv {
	t.Helper()
	gdb, _ := testdb.NewMigrated(t)
	rec := &sqlRecorder{}
	gdb = gdb.Session(&gorm.Session{Logger: rec})
	env := &taskEnv{db: gdb, rec: rec}
	for i := range env.ids {
		u := &models.User{Name: "U", Email: fmt.Sprintf("u%d@example.com", i), Password: "x", Role: "user"}
		if err := gdb.Create(u).Error; err != nil {
			t.Fatal(err)
		}
		env.ids[i] = u.ID
	}
	h := NewTaskHandler(services.NewTaskService(repositories.NewTaskRepository(gdb)))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-User") == "2" {
			c.Set("userID", env.ids[1])
		} else {
			c.Set("userID", env.ids[0])
		}
	})
	r.POST("/tasks", h.CreateTask)
	r.POST("/tasks/bulk", h.BulkCreateTasks)
	r.PUT("/tasks/:id", h.UpdateTask)
	env.r = r
	return env
}

func taskJSON(name, start, end, date string) string {
	return fmt.Sprintf(`{"name":%q,"category":"deep-work","startTime":%q,"endTime":%q,"energy":3,"date":%q}`, name, start, end, date)
}

func (e *taskEnv) create(t *testing.T, body string) (int, uint) {
	t.Helper()
	w := doRequest(e.r, http.MethodPost, "/tasks", body)
	var id uint
	if w.Code == http.StatusCreated {
		var task models.Task
		e.db.Order("id desc").First(&task)
		id = task.ID
	}
	return w.Code, id
}

func TestCreateTask_RejectsInvalidBlocks(t *testing.T) {
	e := newTaskEnv(t)
	for name, body := range map[string]string{
		"end before start": taskJSON("x", "10:00", "09:00", "2026-10-29"),
		"crosses midnight": taskJSON("x", "23:00", "01:00", "2026-10-29"),
		"bad date":         taskJSON("x", "09:00", "10:00", "2026-02-30"),
		"bad time":         taskJSON("x", "9am", "10:00", "2026-10-29"),
		"bad category":     `{"name":"x","category":"nap","startTime":"09:00","endTime":"10:00","energy":3,"date":"2026-10-29"}`,
		"energy 6":         `{"name":"x","category":"study","startTime":"09:00","endTime":"10:00","energy":6,"date":"2026-10-29"}`,
		"long name":        taskJSON(strings.Repeat("a", 121), "09:00", "10:00", "2026-10-29"),
	} {
		if code, _ := e.create(t, body); code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", name, code)
		}
	}
	var n int64
	e.db.Model(&models.Task{}).Count(&n)
	if n != 0 {
		t.Fatalf("no invalid block may be stored, found %d", n)
	}
}

func TestCreateTask_RejectsOverlapWith409(t *testing.T) {
	e := newTaskEnv(t)
	if code, _ := e.create(t, taskJSON("A", "09:00", "10:00", "2026-10-29")); code != http.StatusCreated {
		t.Fatalf("setup: %d", code)
	}
	if code, _ := e.create(t, taskJSON("B", "09:30", "10:30", "2026-10-29")); code != http.StatusConflict {
		t.Fatalf("overlap must be 409, got %d", code)
	}
	if code, _ := e.create(t, taskJSON("C", "08:00", "11:00", "2026-10-29")); code != http.StatusConflict {
		t.Fatalf("enclosing block must be 409, got %d", code)
	}
	if code, _ := e.create(t, taskJSON("adjacent", "10:00", "11:00", "2026-10-29")); code != http.StatusCreated {
		t.Fatalf("touching blocks must be allowed, got %d", code)
	}
	if code, _ := e.create(t, taskJSON("other day", "09:00", "10:00", "2026-10-30")); code != http.StatusCreated {
		t.Fatalf("same times on another date must be allowed, got %d", code)
	}
	w := doRequestWithHeader(e.r, http.MethodPost, "/tasks", taskJSON("other user", "09:00", "10:00", "2026-10-29"), "X-Test-User", "2")
	if w.Code != http.StatusCreated {
		t.Fatalf("another user's blocks must not conflict, got %d", w.Code)
	}
}

func TestCreateTask_ConcurrentOverlapsOnlyOneWins(t *testing.T) {
	e := newTaskEnv(t)
	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := doRequest(e.r, http.MethodPost, "/tasks", taskJSON(fmt.Sprintf("T%d", i), "09:00", "10:00", "2026-10-29"))
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Fatalf("unexpected status %d in %v", c, codes)
		}
	}
	if created != 1 {
		t.Fatalf("exactly one of %d concurrent overlapping creates may succeed, got %d (%v)", n, created, codes)
	}
}

func TestUpdateTask_RejectsInvalidAndOverlapping(t *testing.T) {
	e := newTaskEnv(t)
	_, a := e.create(t, taskJSON("A", "09:00", "10:00", "2026-10-29"))
	_, b := e.create(t, taskJSON("B", "11:00", "12:00", "2026-10-29"))

	put := func(id uint, body string) int {
		return doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", id), body).Code
	}
	if c := put(b, `{"startTime":"09:30"}`); c != http.StatusConflict {
		t.Fatalf("moving B onto A must be 409, got %d", c)
	}
	if c := put(b, `{"endTime":"10:30"}`); c != http.StatusBadRequest {
		t.Fatalf("end before start on update must be 400, got %d", c)
	}
	for _, body := range []string{`{"status":"done"}`, `{"energy":0}`, `{"distractions":101}`, `{"category":"nap"}`, `{"date":"2026-02-30"}`, `{"name":""}`} {
		if c := put(a, body); c != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", body, c)
		}
	}
	if c := put(a, `{"startTime":"09:15","endTime":"09:45"}`); c != http.StatusOK {
		t.Fatalf("shrinking a block inside its own slot must not conflict with itself, got %d", c)
	}
	if c := put(b, `{"startTime":"10:00","endTime":"11:00"}`); c != http.StatusOK {
		t.Fatalf("moving B into free time must succeed, got %d", c)
	}
	var stored models.Task
	e.db.First(&stored, b)
	if stored.StartTime != "10:00" || stored.EndTime != "11:00" {
		t.Fatalf("update not stored: %+v", stored)
	}
}

func TestUpdateTask_WritesOnlyProvidedFields(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:00", "2026-10-29"))
	// A concurrent writer bumps distractions after our handler would have loaded the row.
	e.db.Exec("UPDATE tasks SET distractions = 7 WHERE id = ?", id)

	e.rec.mu.Lock()
	e.rec.stmt = nil
	e.rec.mu.Unlock()
	if c := doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", id), `{"status":"completed"}`).Code; c != http.StatusOK {
		t.Fatalf("expected 200, got %d", c)
	}
	var update string
	for _, s := range e.rec.stmt {
		if strings.HasPrefix(s, `UPDATE "tasks"`) {
			update = s
		}
	}
	if update == "" {
		t.Fatal("no UPDATE recorded")
	}
	for _, col := range []string{`"distractions"`, `"name"`, `"start_time"`, `"energy"`, `"date"`} {
		if strings.Contains(update, col) {
			t.Fatalf("status-only update must not write %s: %s", col, update)
		}
	}
	var stored models.Task
	e.db.First(&stored, id)
	if stored.Distractions != 7 || stored.Status != "completed" {
		t.Fatalf("expected distractions 7 and completed, got %+v", stored)
	}
}

func TestUpdateTask_LegacyInvalidRowCanBeCompleted(t *testing.T) {
	e := newTaskEnv(t)
	e.db.Exec(`INSERT INTO tasks (user_id, name, category, start_time, end_time, date, created_at, updated_at)
	           VALUES (?, 'legacy', 'study', '10:00', '09:00', '2026-10-29', now(), now())`, e.ids[0])
	var legacy models.Task
	e.db.Where("name = 'legacy'").First(&legacy)
	if c := doRequest(e.r, http.MethodPut, fmt.Sprintf("/tasks/%d", legacy.ID), `{"status":"completed"}`).Code; c != http.StatusOK {
		t.Fatalf("status-only update of a legacy row must succeed, got %d", c)
	}
}

func TestBulkCreate_ValidatesEveryItemAndOverlaps(t *testing.T) {
	e := newTaskEnv(t)
	bulk := func(items ...string) int {
		return doRequest(e.r, http.MethodPost, "/tasks/bulk", `{"tasks":[`+strings.Join(items, ",")+`]}`).Code
	}
	if c := bulk(taskJSON("a", "09:00", "10:00", "2026-10-29"), taskJSON("b", "09:30", "10:30", "2026-10-29")); c != http.StatusConflict {
		t.Fatalf("overlap inside the batch must be 409, got %d", c)
	}
	if c := bulk(taskJSON("a", "09:00", "10:00", "2026-10-29"), taskJSON("b", "11:00", "10:30", "2026-10-29")); c != http.StatusBadRequest {
		t.Fatalf("an invalid item must be 400, got %d", c)
	}
	var n int64
	e.db.Model(&models.Task{}).Count(&n)
	if n != 0 {
		t.Fatalf("a rejected batch must insert nothing, found %d", n)
	}
	e.create(t, taskJSON("existing", "14:00", "15:00", "2026-10-29"))
	if c := bulk(taskJSON("a", "09:00", "10:00", "2026-10-29"), taskJSON("b", "14:30", "15:30", "2026-10-29")); c != http.StatusConflict {
		t.Fatalf("overlap with an existing block must be 409, got %d", c)
	}
	e.db.Model(&models.Task{}).Count(&n)
	if n != 1 {
		t.Fatalf("a rejected batch must insert nothing, found %d", n)
	}
}
