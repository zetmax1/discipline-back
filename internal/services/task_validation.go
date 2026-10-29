package services

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"discipline-backend/internal/models"
)

// ErrInvalidTask is wrapped by every task validation failure.
var ErrInvalidTask = errors.New("invalid task")

const maxTaskNameChars = 120

var (
	taskCategories = map[string]bool{
		models.CategoryDeepWork: true, models.CategoryStudy: true, models.CategoryAdmin: true,
		models.CategoryGym: true, models.CategoryRest: true,
	}
	taskStatuses = map[string]bool{
		models.StatusPending: true, models.StatusCompleted: true, models.StatusSkipped: true,
	}
)

func invalidTask(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidTask, fmt.Sprintf(format, args...))
}

func validateTaskName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxTaskNameChars {
		return "", invalidTask("name must be 1-%d characters", maxTaskNameChars)
	}
	return name, nil
}

func validateTaskDate(d string) error {
	parsed, err := time.Parse("2006-01-02", d)
	if err != nil || parsed.Format("2006-01-02") != d {
		return invalidTask("date must be a valid YYYY-MM-DD date")
	}
	return nil
}

// validateTaskTimes enforces HH:MM, end after start, and no midnight crossing (end ≤ 24:00).
func validateTaskTimes(start, end models.ClockTime) error {
	s, okS := start.Minutes()
	e, okE := end.Minutes()
	if !okS || !okE {
		return invalidTask("startTime and endTime must be HH:MM")
	}
	if e <= s {
		return invalidTask("endTime must be after startTime (blocks cannot cross midnight; use 24:00 for end of day)")
	}
	return nil
}

func validateTaskCategory(c string) error {
	if !taskCategories[c] {
		return invalidTask("category must be one of deep-work, study, admin, gym, rest")
	}
	return nil
}

func validateTaskEnergy(e int) error {
	if e < 1 || e > 5 {
		return invalidTask("energy must be between 1 and 5")
	}
	return nil
}

// validateNewTask checks every field of a block being created; it trims the name in place.
func validateNewTask(t *models.Task) error {
	name, err := validateTaskName(t.Name)
	if err != nil {
		return err
	}
	t.Name = name
	if err := validateTaskCategory(t.Category); err != nil {
		return err
	}
	if err := validateTaskDate(string(t.Date)); err != nil {
		return err
	}
	if err := validateTaskTimes(t.StartTime, t.EndTime); err != nil {
		return err
	}
	return validateTaskEnergy(t.Energy)
}

// TaskUpdate holds the fields a PUT /tasks/:id request provided (nil = not provided).
type TaskUpdate struct {
	Name, Category, StartTime, EndTime, Status, Date *string
	Distractions, Energy                             *int
}

// mergeTaskUpdate validates the provided fields against the current row. It returns
// only the columns to write, the merged task, and whether date/start/end changed
// (in which case the time order and overlaps must be re-checked by the caller).
// Untouched fields are not re-validated, so legacy rows stay editable.
func mergeTaskUpdate(current models.Task, u TaskUpdate) (map[string]any, models.Task, bool, error) {
	cols := map[string]any{}
	merged := current
	if u.Name != nil {
		name, err := validateTaskName(*u.Name)
		if err != nil {
			return nil, current, false, err
		}
		cols["name"], merged.Name = name, name
	}
	if u.Category != nil {
		if err := validateTaskCategory(*u.Category); err != nil {
			return nil, current, false, err
		}
		cols["category"], merged.Category = *u.Category, *u.Category
	}
	if u.Status != nil {
		if !taskStatuses[*u.Status] {
			return nil, current, false, invalidTask("status must be one of pending, completed, skipped")
		}
		cols["status"], merged.Status = *u.Status, *u.Status
	}
	if u.Energy != nil {
		if err := validateTaskEnergy(*u.Energy); err != nil {
			return nil, current, false, err
		}
		cols["energy"], merged.Energy = *u.Energy, *u.Energy
	}
	if u.Distractions != nil {
		if *u.Distractions < 0 || *u.Distractions > 100 {
			return nil, current, false, invalidTask("distractions must be between 0 and 100")
		}
		cols["distractions"], merged.Distractions = *u.Distractions, *u.Distractions
	}

	timesMoved := false
	if u.Date != nil {
		if err := validateTaskDate(*u.Date); err != nil {
			return nil, current, false, err
		}
		cols["date"], merged.Date = models.Date(*u.Date), models.Date(*u.Date)
		timesMoved = true
	}
	if u.StartTime != nil {
		cols["start_time"], merged.StartTime = models.ClockTime(*u.StartTime), models.ClockTime(*u.StartTime)
		timesMoved = true
	}
	if u.EndTime != nil {
		cols["end_time"], merged.EndTime = models.ClockTime(*u.EndTime), models.ClockTime(*u.EndTime)
		timesMoved = true
	}
	if u.StartTime != nil || u.EndTime != nil {
		if err := validateTaskTimes(merged.StartTime, merged.EndTime); err != nil {
			return nil, current, false, err
		}
	}
	return cols, merged, timesMoved, nil
}
