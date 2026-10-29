package services

import (
	"fmt"

	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// TaskService defines task business logic.
type TaskService interface {
	CreateTask(task *models.Task) error
	BulkCreateTasks(tasks []*models.Task) error
	GetTask(id, userID uint) (*models.Task, error)
	GetTasks(userID uint, date string) ([]models.Task, error)
	UpdateTask(id, userID uint, upd TaskUpdate) (*models.Task, error)
	AdjustDistractions(id, userID uint, delta int) (*models.Task, error)
	DeleteTask(id, userID uint) error
}

type taskService struct {
	taskRepo repositories.TaskRepository
}

// NewTaskService creates a new TaskService.
func NewTaskService(taskRepo repositories.TaskRepository) TaskService {
	return &taskService{taskRepo: taskRepo}
}

// CreateTask validates the block and inserts it, rejecting overlaps (ErrTaskOverlap).
func (s *taskService) CreateTask(task *models.Task) error {
	if err := validateNewTask(task); err != nil {
		return err
	}
	return s.taskRepo.CreateTasks(task.UserID, []*models.Task{task})
}

// BulkCreateTasks validates every block, then inserts all or none.
func (s *taskService) BulkCreateTasks(tasks []*models.Task) error {
	if len(tasks) == 0 {
		return nil
	}
	for i, t := range tasks {
		if err := validateNewTask(t); err != nil {
			return fmt.Errorf("tasks[%d]: %w", i, err)
		}
	}
	return s.taskRepo.CreateTasks(tasks[0].UserID, tasks)
}

func (s *taskService) GetTask(id, userID uint) (*models.Task, error) {
	return s.taskRepo.FindByID(id, userID)
}

func (s *taskService) GetTasks(userID uint, date string) ([]models.Task, error) {
	if date != "" {
		return s.taskRepo.FindByUserAndDate(userID, date)
	}
	return s.taskRepo.FindByUser(userID)
}

// UpdateTask applies a validated partial update; only provided fields are written.
func (s *taskService) UpdateTask(id, userID uint, upd TaskUpdate) (*models.Task, error) {
	return s.taskRepo.UpdateTask(id, userID, func(current models.Task) (map[string]any, models.Task, bool, error) {
		return mergeTaskUpdate(current, upd)
	})
}

// AdjustDistractions adds +1 or -1 to a block's distraction count in one SQL statement.
func (s *taskService) AdjustDistractions(id, userID uint, delta int) (*models.Task, error) {
	if delta != 1 && delta != -1 {
		return nil, invalidTask("delta must be 1 or -1")
	}
	return s.taskRepo.AdjustDistractions(id, userID, delta)
}

func (s *taskService) DeleteTask(id, userID uint) error {
	return s.taskRepo.Delete(id, userID)
}
