package services

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// DashboardStats holds computed stats for the dashboard.
type DashboardStats struct {
	TotalTasks     int     `json:"totalTasks"`
	CompletedTasks int     `json:"completedTasks"`
	CompletionRate int     `json:"completionRate"` // percentage 0-100
	FocusScore     int     `json:"focusScore"`     // 0-100
	TotalHours     float64 `json:"totalHours"`
	DeepWorkHours  float64 `json:"deepWorkHours"`
}

// TaskService defines task business logic.
type TaskService interface {
	CreateTask(task *models.Task) error
	BulkCreateTasks(tasks []*models.Task) error
	GetTask(id, userID uint) (*models.Task, error)
	GetTasks(userID uint, date string) ([]models.Task, error)
	UpdateTask(id, userID uint, upd TaskUpdate) (*models.Task, error)
	AdjustDistractions(id, userID uint, delta int) (*models.Task, error)
	DeleteTask(id, userID uint) error
	GetDashboardStats(userID uint, date string) (*DashboardStats, error)
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

// GetDashboardStats computes dashboard metrics — mirrors the frontend useMemo logic.
func (s *taskService) GetDashboardStats(userID uint, date string) (*DashboardStats, error) {
	tasks, err := s.taskRepo.FindByUserAndDate(userID, date)
	if err != nil {
		return nil, err
	}

	total := len(tasks)
	completed := 0
	totalHours := 0.0
	deepWorkHours := 0.0
	totalDistractions := 0

	for _, t := range tasks {
		if t.Status == models.StatusCompleted {
			completed++
		}
		hours := timeDiffHours(string(t.StartTime), string(t.EndTime))
		totalHours += hours
		if t.Category == models.CategoryDeepWork {
			deepWorkHours += hours
		}
		totalDistractions += t.Distractions
	}

	completionRate := 0
	if total > 0 {
		completionRate = int(math.Round(float64(completed) / float64(total) * 100))
	}

	deepRatio := 0.0
	if totalHours > 0 {
		deepRatio = deepWorkHours / totalHours
	}
	distractionPenalty := math.Min(float64(totalDistractions)*5, 40)
	focusScore := int(math.Max(0, math.Round(deepRatio*100-distractionPenalty)))

	return &DashboardStats{
		TotalTasks:     total,
		CompletedTasks: completed,
		CompletionRate: completionRate,
		FocusScore:     focusScore,
		TotalHours:     math.Round(totalHours*10) / 10,
		DeepWorkHours:  math.Round(deepWorkHours*10) / 10,
	}, nil
}

// timeDiffHours calculates hours between two "HH:MM" strings.
func timeDiffHours(start, end string) float64 {
	sp := strings.Split(start, ":")
	ep := strings.Split(end, ":")
	if len(sp) != 2 || len(ep) != 2 {
		return 0
	}
	sh, _ := strconv.Atoi(sp[0])
	sm, _ := strconv.Atoi(sp[1])
	eh, _ := strconv.Atoi(ep[0])
	em, _ := strconv.Atoi(ep[1])
	return float64(eh*60+em-sh*60-sm) / 60
}
