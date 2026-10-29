package handlers

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"discipline-backend/internal/models"
	"discipline-backend/internal/services"
)

type fakeTaskService struct {
	services.TaskService
	bulkCalls int
	bulkCount int
}

func (f *fakeTaskService) BulkCreateTasks(tasks []*models.Task) error {
	f.bulkCalls++
	f.bulkCount = len(tasks)
	return nil
}

func taskRouter(svc services.TaskService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", uint(1)) })
	r.POST("/tasks/bulk", NewTaskHandler(svc).BulkCreateTasks)
	return r
}

func bulkBody(n int) string {
	one := `{"name":"Deep Work","category":"deep-work","startTime":"09:00","endTime":"10:00","energy":3,"date":"2026-10-29"}`
	items := make([]string, n)
	for i := range items {
		items[i] = one
	}
	return `{"tasks":[` + strings.Join(items, ",") + `]}`
}

func TestBulkCreate_RejectsMoreThan200(t *testing.T) {
	svc := &fakeTaskService{}
	w := doRequest(taskRouter(svc), http.MethodPost, "/tasks/bulk", bulkBody(201))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for 201 tasks, got %d", w.Code)
	}
	if svc.bulkCalls != 0 {
		t.Fatal("service must not be called")
	}
}

func TestBulkCreate_Accepts200(t *testing.T) {
	svc := &fakeTaskService{}
	w := doRequest(taskRouter(svc), http.MethodPost, "/tasks/bulk", bulkBody(200))
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if svc.bulkCount != 200 {
		t.Fatalf("expected 200 tasks passed to service, got %d", svc.bulkCount)
	}
}

func TestBulkCreate_RejectsEmptyList(t *testing.T) {
	svc := &fakeTaskService{}
	w := doRequest(taskRouter(svc), http.MethodPost, "/tasks/bulk", `{"tasks":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an empty list, got %d", w.Code)
	}
}

type errTaskService struct {
	services.TaskService
	err error
}

func (f *errTaskService) DeleteTask(id, userID uint) error { return f.err }
func (f *errTaskService) UpdateTask(id, userID uint, updates map[string]interface{}) (*models.Task, error) {
	return nil, f.err
}

func TestTaskHandlers_MapDBErrorsTo500AndMissingTo404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err  error
		want int
	}{
		{gorm.ErrRecordNotFound, http.StatusNotFound},
		{errors.New("connection refused"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		h := NewTaskHandler(&errTaskService{err: tc.err})
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("userID", uint(1)) })
		r.DELETE("/tasks/:id", h.DeleteTask)
		r.PUT("/tasks/:id", h.UpdateTask)
		if w := doRequest(r, http.MethodDelete, "/tasks/7", ""); w.Code != tc.want {
			t.Errorf("DELETE with %v: expected %d, got %d", tc.err, tc.want, w.Code)
		}
		if w := doRequest(r, http.MethodPut, "/tasks/7", `{"name":"x"}`); w.Code != tc.want {
			t.Errorf("PUT with %v: expected %d, got %d", tc.err, tc.want, w.Code)
		}
	}
}
