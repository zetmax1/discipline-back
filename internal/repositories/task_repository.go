package repositories

import (
	"errors"
	"sort"
	"time"

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
	// ReplaceWeekPlan deletes the user's generated blocks dated weekStart..weekEnd and
	// inserts tasks, all in one transaction; returns how many blocks were replaced.
	ReplaceWeekPlan(userID uint, weekStart, weekEnd models.Date, tasks []*models.Task) (int64, error)
	// MoveTask turns a block into "moved" and inserts its replacement (built from the locked
	// current row) in one transaction. With an idempotency key, a replay returns the block the
	// first call created.
	MoveTask(id, userID uint, key string, build func(current models.Task) (*models.Task, error)) (*models.Task, error)
	// AdjustDistractions atomically adds delta to the counter, clamped to 0-100.
	AdjustDistractions(id, userID uint, delta int) (*models.Task, error)
	// CompletedSessionSpan returns the first start and the last end of the block's completed
	// focus sessions; ok is false when it has none. A session still running or abandoned is not
	// time worked and is left out.
	CompletedSessionSpan(userID, blockID uint) (first, last time.Time, ok bool, err error)
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
// blocks on date (excluding excludeID). Touching blocks do not overlap, and skipped or
// moved blocks do not occupy time.
func checkOverlap(tx *gorm.DB, userID uint, date models.Date, start, end models.ClockTime, excludeID uint) error {
	s, _ := start.Minutes()
	e, _ := end.Minutes()
	var ids []uint
	err := tx.Raw(`SELECT id FROM tasks
		WHERE user_id = ? AND date = ? AND id <> ? AND status NOT IN (?, ?) AND start_min < ? AND end_min > ?
		LIMIT 1`, userID, date, excludeID, models.StatusSkipped, models.StatusMoved, e, s).Scan(&ids).Error
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
			if err := resolveCategory(tx, userID, t); err != nil {
				return err
			}
			if err := checkOverlap(tx, userID, t.Date, t.StartTime, t.EndTime, 0); err != nil {
				return err
			}
		}
		if err := tx.CreateInBatches(tasks, 100).Error; err != nil {
			return err
		}
		return attachKinds(tx, tasks)
	})
}

// resolveCategory checks a block's categoryId belongs to the user and writes the matching legacy
// string next to it. A block sent with only the legacy string is resolved by the trigger.
func resolveCategory(tx *gorm.DB, userID uint, t *models.Task) error {
	if t.CategoryID == nil {
		return nil
	}
	kind, err := categoryKind(tx, *t.CategoryID, userID)
	if err != nil {
		return err
	}
	t.Category = models.LegacyCategory(kind)
	return nil
}

func (r *taskRepository) ReplaceWeekPlan(userID uint, weekStart, weekEnd models.Date, tasks []*models.Task) (int64, error) {
	if err := checkBatchOverlaps(tasks); err != nil {
		return 0, err
	}
	var replaced int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		res := tx.Where("user_id = ? AND source = ? AND date BETWEEN ? AND ?",
			userID, models.SourceSmartSchedule, weekStart, weekEnd).Delete(&models.Task{})
		if res.Error != nil {
			return res.Error
		}
		replaced = res.RowsAffected
		for _, t := range tasks {
			if err := resolveCategory(tx, userID, t); err != nil {
				return err
			}
			if err := checkOverlap(tx, userID, t.Date, t.StartTime, t.EndTime, 0); err != nil {
				return err
			}
		}
		if len(tasks) == 0 {
			return nil
		}
		return tx.CreateInBatches(tasks, 100).Error
	})
	if err != nil {
		return 0, err
	}
	return replaced, nil
}

func (r *taskRepository) FindByID(id, userID uint) (*models.Task, error) {
	var task models.Task
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, attachKinds(r.db, []*models.Task{&task})
}

func (r *taskRepository) FindByUser(userID uint) ([]models.Task, error) {
	var tasks []models.Task
	if err := r.db.Where("user_id = ?", userID).Order("date DESC, start_time ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, attachKindsList(r.db, tasks)
}

func (r *taskRepository) FindByUserAndDate(userID uint, date string) ([]models.Task, error) {
	var tasks []models.Task
	if err := r.db.Where("user_id = ? AND date = ?", userID, date).Order("start_time ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, attachKindsList(r.db, tasks)
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
		if err := categoryColumns(tx, userID, current, cols); err != nil {
			return err
		}
		if err := occurrenceColumns(tx, userID, current, cols); err != nil {
			return err
		}
		// A skipped or moved block occupies no time; reviving one claims its slot again.
		revived := !models.OccupiesTime(current.Status) && models.OccupiesTime(merged.Status)
		if models.OccupiesTime(merged.Status) && (timesMoved || revived) {
			if err := checkOverlap(tx, userID, merged.Date, merged.StartTime, merged.EndTime, id); err != nil {
				return err
			}
		}
		if len(cols) > 0 {
			if err := tx.Model(&models.Task{}).Where("id = ? AND user_id = ?", id, userID).Updates(cols).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&out).Error; err != nil {
			return err
		}
		return attachKinds(tx, []*models.Task{&out})
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// categoryColumns completes an update's category columns: a categoryId must be the user's and
// brings its legacy string; a legacy string alone clears category_id so the trigger resolves it;
// a block that still has no category (an empty legacy string) gets Personal now that it is touched.
func categoryColumns(tx *gorm.DB, userID uint, current models.Task, cols map[string]any) error {
	if id, ok := cols["category_id"].(uint); ok {
		kind, err := categoryKind(tx, id, userID)
		if err != nil {
			return err
		}
		cols["category"] = models.LegacyCategory(kind)
		return nil
	}
	if _, ok := cols["category"]; ok {
		cols["category_id"] = nil
		return nil
	}
	if current.CategoryID == nil {
		id, err := personalCategoryID(tx, userID)
		if err != nil {
			return err
		}
		if id != 0 {
			cols["category_id"] = id
		}
	}
	return nil
}

// occurrenceColumns marks an occurrence of a recurring rule as edited by hand ("this one") when
// what it plans changes; moving it to another date records where it came from and writes a
// `moved` exception, so materialization does not plan the original date again.
func occurrenceColumns(tx *gorm.DB, userID uint, current models.Task, cols map[string]any) error {
	if current.RecurringRuleID == nil {
		return nil
	}
	for _, c := range []string{"name", "category", "category_id", "start_time", "end_time", "date"} {
		if _, ok := cols[c]; ok {
			cols["recurring_edited"] = true
			break
		}
	}
	newDate, ok := cols["date"].(models.Date)
	if !ok || newDate == current.Date {
		return nil
	}
	original := current.Date
	if current.OriginalDate != nil {
		original = *current.OriginalDate
	}
	cols["original_date"] = original
	return addException(tx, userID, *current.RecurringRuleID, original, ExceptionMoved)
}

// Reasons a recurring rule has no block on a date (never a miss).
const (
	ExceptionCancelled = "cancelled"
	ExceptionConflict  = "conflict"
	ExceptionMoved     = "moved"
)

// addException records that rule has no block on date; an existing exception keeps its reason
// unless the new one is a cancellation, which wins.
func addException(tx *gorm.DB, userID, ruleID uint, date models.Date, reason string) error {
	return tx.Exec(`INSERT INTO recurring_exceptions (user_id, rule_id, date, reason) VALUES (?, ?, ?, ?)
		ON CONFLICT (rule_id, date) DO UPDATE SET reason = CASE WHEN EXCLUDED.reason = 'cancelled'
			THEN 'cancelled' ELSE recurring_exceptions.reason END`, userID, ruleID, date, reason).Error
}

func (r *taskRepository) AdjustDistractions(id, userID uint, delta int) (*models.Task, error) {
	var task models.Task
	res := r.db.Model(&task).Clauses(clause.Returning{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("distractions", gorm.Expr("LEAST(100, GREATEST(0, distractions + ?))", delta))
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &task, nil
}

func (r *taskRepository) CompletedSessionSpan(userID, blockID uint) (time.Time, time.Time, bool, error) {
	var row struct {
		First *time.Time
		Last  *time.Time
	}
	res := r.db.Raw(`SELECT min(started_at) AS first, max(ended_at) AS last FROM focus_sessions
		WHERE user_id = ? AND block_id = ? AND outcome = ? AND ended_at IS NOT NULL`,
		userID, blockID, models.SessionCompleted).Scan(&row)
	if res.Error != nil {
		return time.Time{}, time.Time{}, false, res.Error
	}
	if row.First == nil || row.Last == nil {
		return time.Time{}, time.Time{}, false, nil
	}
	return *row.First, *row.Last, true, nil
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

// ErrTaskNotMovable is returned when moving a block that is already done, partial or moved.
var ErrTaskNotMovable = errors.New("block can no longer be moved")

func (r *taskRepository) MoveTask(id, userID uint, key string, build func(current models.Task) (*models.Task, error)) (*models.Task, error) {
	var out models.Task
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		var earlier []uint
		if err := tx.Raw(`SELECT resource_id FROM idempotency_keys WHERE user_id = ? AND key = ?`, userID, key).
			Scan(&earlier).Error; err != nil {
			return err
		}
		if len(earlier) > 0 {
			if err := tx.Where("id = ? AND user_id = ?", earlier[0], userID).First(&out).Error; err != nil {
				return err
			}
			return attachKinds(tx, []*models.Task{&out})
		}
		var current models.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", id, userID).First(&current).Error; err != nil {
			return err
		}
		switch current.Status {
		case models.StatusDone, models.StatusPartial, models.StatusMoved:
			return ErrTaskNotMovable
		}
		next, err := build(current)
		if err != nil {
			return err
		}
		// The old block stops occupying its slot first, so a move within the same day can overlap it.
		if err := tx.Model(&models.Task{}).Where("id = ? AND user_id = ?", id, userID).
			Update("status", models.StatusMoved).Error; err != nil {
			return err
		}
		if err := checkOverlap(tx, userID, next.Date, next.StartTime, next.EndTime, 0); err != nil {
			return err
		}
		if next.CategoryID == nil {
			if id, err := personalCategoryID(tx, userID); err != nil {
				return err
			} else if id != 0 {
				next.CategoryID = &id
			}
		}
		if err := tx.Create(next).Error; err != nil {
			return err
		}
		if next.RecurringRuleID != nil && next.OriginalDate != nil {
			if err := addException(tx, userID, *next.RecurringRuleID, *next.OriginalDate, ExceptionMoved); err != nil {
				return err
			}
		}
		if err := tx.Exec(`DELETE FROM idempotency_keys WHERE created_at < now() - interval '1 day'`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO idempotency_keys (user_id, key, resource_id) VALUES (?, ?, ?)`,
			userID, key, next.ID).Error; err != nil {
			return err
		}
		out = *next
		return attachKinds(tx, []*models.Task{&out})
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
