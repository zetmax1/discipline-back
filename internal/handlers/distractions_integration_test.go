package handlers

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"discipline-backend/internal/models"
)

func (e *taskEnv) distractions(t *testing.T, id uint) int {
	t.Helper()
	var task models.Task
	if err := e.db.First(&task, id).Error; err != nil {
		t.Fatal(err)
	}
	return task.Distractions
}

func TestDistractions_ConcurrentIncrementsAreNotLost(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:00", "2026-10-29"))

	const n = 20
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = doRequest(e.r, http.MethodPost, fmt.Sprintf("/tasks/%d/distractions", id), `{"delta":1}`).Code
		}(i)
	}
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("unexpected status in %v", codes)
		}
	}
	if got := e.distractions(t, id); got != n {
		t.Fatalf("%d concurrent increments must give %d, got %d", n, n, got)
	}
}

func TestDistractions_ClampedAndValidated(t *testing.T) {
	e := newTaskEnv(t)
	_, id := e.create(t, taskJSON("A", "09:00", "10:00", "2026-10-29"))
	path := fmt.Sprintf("/tasks/%d/distractions", id)

	if c := doRequest(e.r, http.MethodPost, path, `{"delta":-1}`).Code; c != http.StatusOK {
		t.Fatalf("expected 200, got %d", c)
	}
	if got := e.distractions(t, id); got != 0 {
		t.Fatalf("must not go below 0, got %d", got)
	}
	e.db.Exec("UPDATE tasks SET distractions = 100 WHERE id = ?", id)
	doRequest(e.r, http.MethodPost, path, `{"delta":1}`)
	if got := e.distractions(t, id); got != 100 {
		t.Fatalf("must not go above 100, got %d", got)
	}
	for _, body := range []string{`{"delta":2}`, `{"delta":0}`, `{}`, `{"delta":"1"}`} {
		if c := doRequest(e.r, http.MethodPost, path, body).Code; c != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", body, c)
		}
	}
	if c := doRequestWithHeader(e.r, http.MethodPost, path, `{"delta":1}`, "X-Test-User", "2").Code; c != http.StatusNotFound {
		t.Fatalf("another user's task must be 404, got %d", c)
	}
	if c := doRequest(e.r, http.MethodPost, "/tasks/999999/distractions", `{"delta":1}`).Code; c != http.StatusNotFound {
		t.Fatalf("missing task must be 404, got %d", c)
	}
}
