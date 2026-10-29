package services

import (
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
	UpdateTask(id, userID uint, updates map[string]interface{}) (*models.Task, error)
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

func (s *taskService) CreateTask(task *models.Task) error {
	return s.taskRepo.Create(task)
}

func (s *taskService) BulkCreateTasks(tasks []*models.Task) error {
	return s.taskRepo.CreateBatch(tasks)
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

func (s *taskService) UpdateTask(id, userID uint, updates map[string]interface{}) (*models.Task, error) {
	task, err := s.taskRepo.FindByID(id, userID)
	if err != nil {
		return nil, err
	}

	// Apply updates
	if v, ok := updates["name"]; ok {
		task.Name = v.(string)
	}
	if v, ok := updates["category"]; ok {
		task.Category = v.(string)
	}
	if v, ok := updates["startTime"]; ok {
		task.StartTime = models.ClockTime(v.(string))
	}
	if v, ok := updates["endTime"]; ok {
		task.EndTime = models.ClockTime(v.(string))
	}
	if v, ok := updates["status"]; ok {
		task.Status = v.(string)
	}
	if v, ok := updates["distractions"]; ok {
		task.Distractions = int(v.(float64))
	}
	if v, ok := updates["energy"]; ok {
		task.Energy = int(v.(float64))
	}
	if v, ok := updates["date"]; ok {
		task.Date = models.Date(v.(string))
	}

	if err := s.taskRepo.Update(task); err != nil {
		return nil, err
	}
	return task, nil
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
