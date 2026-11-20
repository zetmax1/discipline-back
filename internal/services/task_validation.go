package services

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"discipline-backend/internal/apierr"
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
	// Statuses a PUT may set; "moved" only comes from the move endpoint.
	taskStatuses = map[string]bool{
		models.StatusPlanned: true, models.StatusInProgress: true, models.StatusDone: true,
		models.StatusPartial: true, models.StatusSkipped: true,
	}
	legacyStatuses = map[string]string{
		models.LegacyStatusPending: models.StatusPlanned, models.LegacyStatusCompleted: models.StatusDone,
	}
)

// invalidTask wraps ErrInvalidTask with the code the client translates.
func invalidTask(code apierr.Code, format string, args ...any) error {
	return apierr.Wrap(ErrInvalidTask, code, fmt.Sprintf(format, args...))
}

func validateTaskName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxTaskNameChars {
		return "", invalidTask(apierr.TaskNameLength, "name must be 1-%d characters", maxTaskNameChars)
	}
	return name, nil
}

func validateTaskDate(d string) error {
	parsed, err := time.Parse("2006-01-02", d)
	if err != nil || parsed.Format("2006-01-02") != d {
		return invalidTask(apierr.TaskDateInvalid, "date must be a valid YYYY-MM-DD date")
	}
	return nil
}

// validateTaskTimes enforces HH:MM, end after start, and no midnight crossing (end ≤ 24:00).
func validateTaskTimes(start, end models.ClockTime) error {
	s, okS := start.Minutes()
	e, okE := end.Minutes()
	if !okS || !okE {
		return invalidTask(apierr.TaskTimeInvalid, "startTime and endTime must be HH:MM")
	}
	if e <= s {
		return invalidTask(apierr.TaskEndBeforeStart, "endTime must be after startTime (blocks cannot cross midnight; use 24:00 for end of day)")
	}
	return nil
}

func validateTaskCategory(c string) error {
	if !taskCategories[c] {
		return invalidTask(apierr.TaskCategoryInvalid, "category must be one of deep-work, study, admin, gym, rest")
	}
	return nil
}

func validateTaskEnergy(e int) error {
	if e < 1 || e > 5 {
		return invalidTask(apierr.TaskEnergyInvalid, "energy must be between 1 and 5")
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
	if t.CategoryID == nil { // a categoryId is checked against the user's categories in the repository
		if err := validateTaskCategory(t.Category); err != nil {
			return err
		}
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
	CategoryID                                       *uint // F13; wins over Category
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
	if u.CategoryID != nil {
		cols["category_id"], merged.CategoryID = *u.CategoryID, u.CategoryID
	} else if u.Category != nil {
		if err := validateTaskCategory(*u.Category); err != nil {
			return nil, current, false, err
		}
		cols["category"], merged.Category = *u.Category, *u.Category
	}
	if u.Status != nil {
		status := *u.Status
		if mapped, ok := legacyStatuses[status]; ok {
			status = mapped
		}
		if !taskStatuses[status] {
			return nil, current, false, invalidTask(apierr.TaskStatusInvalid, "status must be one of planned, in_progress, done, partial, skipped")
		}
		cols["status"], merged.Status = status, status
	}
	if u.Energy != nil {
		if err := validateTaskEnergy(*u.Energy); err != nil {
			return nil, current, false, err
		}
		cols["energy"], merged.Energy = *u.Energy, *u.Energy
	}
	if u.Distractions != nil {
		if *u.Distractions < 0 || *u.Distractions > 100 {
			return nil, current, false, invalidTask(apierr.TaskDistractionsInvalid, "distractions must be between 0 and 100")
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

const maxTaskNoteChars = 500

// CheckIn is what happened in a block: done / partial / skipped, plus optional actual times
// (default: the span of the block's completed focus sessions, or the planned times when it has
// none), a focus rating 1-5 and a note.
type CheckIn struct {
	Status                       string
	ActualStartMin, ActualEndMin *int
	FocusRating                  *int
	Note                         *string
}

// actualSpan is a default for a check-in's actual times, in minutes of the block's local day.
type actualSpan struct{ start, end int }

// sessionSpan turns the first start and last end of a block's completed focus sessions into
// minutes of the block's local day in loc, clamped to that day: a session that began before the
// day starts at 0, one that ended after it ends at 1440. ok is false when the result cannot serve
// as actual times (end <= start, e.g. a span inside one minute or outside the day), and the
// planned times stay the default.
func sessionSpan(date models.Date, first, last time.Time, loc *time.Location) (actualSpan, bool) {
	day, err := time.ParseInLocation("2006-01-02", string(date), loc)
	if err != nil {
		return actualSpan{}, false
	}
	next := day.AddDate(0, 0, 1)
	span := actualSpan{start: minuteOfDay(first, day, next, loc), end: minuteOfDay(last, day, next, loc)}
	if span.end <= span.start {
		return actualSpan{}, false
	}
	return span, true
}

// minuteOfDay is t as a wall-clock minute of the local day [day, next), clamped to its ends.
func minuteOfDay(t, day, next time.Time, loc *time.Location) int {
	switch local := t.In(loc); {
	case !local.After(day):
		return 0
	case !local.Before(next):
		return 1440
	default:
		return local.Hour()*60 + local.Minute()
	}
}

// checkInColumns validates a check-in against the current block and returns the columns to write.
// Writing the same check-in twice gives the same row (offline replays are harmless).
// The actual times default to fromSessions (the block's completed focus sessions) when there are
// any, otherwise to the planned times; a time the client sends wins, per field.
func checkInColumns(current models.Task, in CheckIn, fromSessions *actualSpan) (map[string]any, error) {
	switch in.Status {
	case models.StatusSkipped:
		return map[string]any{"status": in.Status, "actual_start_min": nil, "actual_end_min": nil,
			"focus_rating": nil}, nil
	case models.StatusDone, models.StatusPartial:
	default:
		return nil, invalidTask(apierr.TaskStatusInvalid, "check-in status must be done, partial or skipped")
	}
	start, end := current.StartMin, current.EndMin
	if fromSessions != nil {
		start, end = fromSessions.start, fromSessions.end
	}
	if in.ActualStartMin != nil {
		start = *in.ActualStartMin
	}
	if in.ActualEndMin != nil {
		end = *in.ActualEndMin
	}
	if start < 0 || start > 1440 || end < 0 || end > 1440 {
		return nil, invalidTask(apierr.TaskTimeInvalid, "actual times must be minutes 0-1440")
	}
	if end <= start {
		return nil, invalidTask(apierr.TaskEndBeforeStart, "actual end must be after actual start")
	}
	if in.FocusRating != nil && (*in.FocusRating < 1 || *in.FocusRating > 5) {
		return nil, invalidTask(apierr.TaskFocusRatingInvalid, "focusRating must be between 1 and 5")
	}
	cols := map[string]any{"status": in.Status, "actual_start_min": start, "actual_end_min": end, "focus_rating": in.FocusRating}
	if in.Note != nil {
		note := strings.TrimSpace(*in.Note)
		if utf8.RuneCountInString(note) > maxTaskNoteChars {
			return nil, invalidTask(apierr.TaskNoteTooLong, "note must be at most %d characters", maxTaskNoteChars)
		}
		cols["note"] = note
	}
	return cols, nil
}
