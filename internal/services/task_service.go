package services

import (
	"crypto/rand"
	"fmt"
	"time"

	"discipline-backend/internal/apierr"
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
	// CheckIn records what happened in a block (done / partial / skipped).
	CheckIn(id, userID uint, in CheckIn) (*models.Task, error)
	// MoveTask marks the block moved and creates its replacement; key makes replays safe.
	MoveTask(id, userID uint, key string, date string, startMin, endMin int) (*models.Task, error)
	SaveWeekPlan(userID uint, weekStart string, tasks []*models.Task) (planID string, replaced int64, err error)
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

// SaveWeekPlan stores a generated weekly plan for the Monday-Sunday week starting at
// weekStart, replacing blocks generated for that week before (manual blocks are kept).
func (s *taskService) SaveWeekPlan(userID uint, weekStart string, tasks []*models.Task) (string, int64, error) {
	start, err := time.Parse("2006-01-02", weekStart)
	if err != nil || start.Format("2006-01-02") != weekStart || start.Weekday() != time.Monday {
		return "", 0, invalidTask(apierr.PlanWeekInvalid, "weekStart must be a Monday (YYYY-MM-DD)")
	}
	end := start.AddDate(0, 0, 6).Format("2006-01-02")
	planID, err := newUUID()
	if err != nil {
		return "", 0, err
	}
	for i, t := range tasks {
		if err := validateNewTask(t); err != nil {
			return "", 0, fmt.Errorf("tasks[%d]: %w", i, err)
		}
		if string(t.Date) < weekStart || string(t.Date) > end {
			return "", 0, invalidTask(apierr.TaskDateInvalid, "tasks[%d]: date must be within %s..%s", i, weekStart, end)
		}
		t.UserID = userID
		t.Source = models.SourceSmartSchedule
		t.PlanID = &planID
	}
	replaced, err := s.taskRepo.ReplaceWeekPlan(userID, models.Date(weekStart), models.Date(end), tasks)
	if err != nil {
		return "", 0, err
	}
	return planID, replaced, nil
}

// newUUID returns a random (version 4) UUID.
func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// AdjustDistractions adds +1 or -1 to a block's distraction count in one SQL statement.
func (s *taskService) AdjustDistractions(id, userID uint, delta int) (*models.Task, error) {
	if delta != 1 && delta != -1 {
		return nil, invalidTask(apierr.TaskDistractionsInvalid, "delta must be 1 or -1")
	}
	return s.taskRepo.AdjustDistractions(id, userID, delta)
}

func (s *taskService) DeleteTask(id, userID uint) error {
	return s.taskRepo.Delete(id, userID)
}

func (s *taskService) CheckIn(id, userID uint, in CheckIn) (*models.Task, error) {
	return s.taskRepo.UpdateTask(id, userID, func(current models.Task) (map[string]any, models.Task, bool, error) {
		cols, err := checkInColumns(current, in)
		if err != nil {
			return nil, current, false, err
		}
		merged := current
		merged.Status = in.Status
		return cols, merged, false, nil
	})
}

func (s *taskService) MoveTask(id, userID uint, key string, date string, startMin, endMin int) (*models.Task, error) {
	if err := validateTaskDate(date); err != nil {
		return nil, err
	}
	start, end := models.ClockFromMinutes(startMin), models.ClockFromMinutes(endMin)
	if err := validateTaskTimes(start, end); err != nil {
		return nil, err
	}
	return s.taskRepo.MoveTask(id, userID, key, func(current models.Task) (*models.Task, error) {
		movedFrom := current.ID
		return &models.Task{
			UserID: userID, Name: current.Name, Category: current.Category, Energy: current.Energy,
			StartTime: start, EndTime: end, Date: models.Date(date), Status: models.StatusPlanned,
			Source: models.SourceManual, MovedFromID: &movedFrom,
		}, nil
	})
}
