package repositories

import (
	"discipline-backend/internal/models"

	"gorm.io/gorm"
)

// TaskRepository defines the interface for task data access.
type TaskRepository interface {
	Create(task *models.Task) error
	FindByID(id, userID uint) (*models.Task, error)
	FindByUser(userID uint) ([]models.Task, error)
	FindByUserAndDate(userID uint, date string) ([]models.Task, error)
	Update(task *models.Task) error
	Delete(id, userID uint) error
}

type taskRepository struct {
	db *gorm.DB
}

// NewTaskRepository creates a new TaskRepository.
func NewTaskRepository(db *gorm.DB) TaskRepository {
	return &taskRepository{db: db}
}

func (r *taskRepository) Create(task *models.Task) error {
	return r.db.Create(task).Error
}

func (r *taskRepository) FindByID(id, userID uint) (*models.Task, error) {
	var task models.Task
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *taskRepository) FindByUser(userID uint) ([]models.Task, error) {
	var tasks []models.Task
	if err := r.db.Where("user_id = ?", userID).Order("date DESC, start_time ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *taskRepository) FindByUserAndDate(userID uint, date string) ([]models.Task, error) {
	var tasks []models.Task
	if err := r.db.Where("user_id = ? AND date = ?", userID, date).Order("start_time ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *taskRepository) Update(task *models.Task) error {
	return r.db.Save(task).Error
}

func (r *taskRepository) Delete(id, userID uint) error {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&models.Task{})
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}
