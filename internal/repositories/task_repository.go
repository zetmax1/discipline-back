package repositories

import (
	"errors"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"discipline-backend/internal/models"
)

// ErrTaskOverlap is returned when a block would overlap another block of the same user on the same date.
var ErrTaskOverlap = errors.New("block overlaps another block on the same date")

// TaskMerge validates a partial update against the locked current row. It returns
// the columns to write, the merged task, and whether date/start/end changed.
type TaskMerge func(current models.Task) (cols map[string]any, merged models.Task, timesMoved bool, err error)

// TaskRepository defines the interface for task data access.
type TaskRepository interface {
	// CreateTasks inserts one user's tasks atomically, rejecting overlaps.
	CreateTasks(userID uint, tasks []*models.Task) error
	FindByID(id, userID uint) (*models.Task, error)
	FindByUser(userID uint) ([]models.Task, error)
	FindByUserAndDate(userID uint, date string) ([]models.Task, error)
	// UpdateTask applies a validated partial update, writing only the changed columns.
	UpdateTask(id, userID uint, merge TaskMerge) (*models.Task, error)
	Delete(id, userID uint) error
}

type taskRepository struct {
	db *gorm.DB
}

// NewTaskRepository creates a new TaskRepository.
func NewTaskRepository(db *gorm.DB) TaskRepository {
	return &taskRepository{db: db}
}

// lockUserTasks serialises task writes for one user for the rest of the transaction,
// so overlap checks cannot race. FOR NO KEY UPDATE does not block FK checks.
func lockUserTasks(tx *gorm.DB, userID uint) error {
	var id uint
	res := tx.Raw("SELECT id FROM users WHERE id = ? FOR NO KEY UPDATE", userID).Scan(&id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// checkOverlap reports ErrTaskOverlap if [start, end) intersects another of the user's
// blocks on date (excluding excludeID). Touching blocks do not overlap.
func checkOverlap(tx *gorm.DB, userID uint, date models.Date, start, end models.ClockTime, excludeID uint) error {
	var ids []uint
	err := tx.Raw(`SELECT id FROM tasks
		WHERE user_id = ? AND date = ? AND id <> ? AND start_time < ? AND end_time > ?
		LIMIT 1`, userID, date, excludeID, end, start).Scan(&ids).Error
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		return ErrTaskOverlap
	}
	return nil
}

// checkBatchOverlaps rejects a batch whose own blocks overlap each other.
func checkBatchOverlaps(tasks []*models.Task) error {
	sorted := append([]*models.Task(nil), tasks...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date < sorted[j].Date
		}
		return sorted[i].StartTime < sorted[j].StartTime
	})
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		if prev.Date == cur.Date && cur.StartTime < prev.EndTime {
			return ErrTaskOverlap
		}
	}
	return nil
}

func (r *taskRepository) CreateTasks(userID uint, tasks []*models.Task) error {
	if err := checkBatchOverlaps(tasks); err != nil {
		return err
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		for _, t := range tasks {
			if err := checkOverlap(tx, userID, t.Date, t.StartTime, t.EndTime, 0); err != nil {
				return err
			}
		}
		return tx.CreateInBatches(tasks, 100).Error
	})
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

func (r *taskRepository) UpdateTask(id, userID uint, merge TaskMerge) (*models.Task, error) {
	var out models.Task
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		var current models.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", id, userID).First(&current).Error; err != nil {
			return err
		}
		cols, merged, timesMoved, err := merge(current)
		if err != nil {
			return err
		}
		if timesMoved {
			if err := checkOverlap(tx, userID, merged.Date, merged.StartTime, merged.EndTime, id); err != nil {
				return err
			}
		}
		if len(cols) > 0 {
			if err := tx.Model(&models.Task{}).Where("id = ? AND user_id = ?", id, userID).Updates(cols).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND user_id = ?", id, userID).First(&out).Error
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *taskRepository) Delete(id, userID uint) error {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&models.Task{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
