package services

import (
	"errors"
	"slices"
	"time"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/events"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrInvalidRule is wrapped by every recurring-rule and range validation failure.
var ErrInvalidRule = errors.New("invalid recurring rule")

const (
	// MaxRangeDays is the longest range one read may ask for (a month view needs 42).
	MaxRangeDays = 42
	// PlanAheadDays: nothing is materialized more than 8 weeks after today.
	PlanAheadDays = 56
)

// RuleInput is a create (Name, CategoryID, Weekdays, StartMin, EndMin, ValidFrom required) or an
// update (nil = unchanged; ClearValidTo removes the end date).
type RuleInput struct {
	CategoryID         *uint
	Name               *string
	Weekdays           []int
	StartMin, EndMin   *int
	ValidFrom, ValidTo *string
	ClearValidTo       bool
}

// RangeResult is GET /tasks?startDate=&endDate=.
type RangeResult struct {
	Tasks      []models.Task               `json:"tasks"`
	Exceptions []models.RecurringException `json:"exceptions"`
	Truncated  bool                        `json:"truncated"`
}

// RecurringService validates recurring rules and materializes their occurrences.
type RecurringService interface {
	ListRules(userID uint) ([]models.RecurringRule, error)
	CreateRule(userID uint, in RuleInput) (*models.RecurringRule, error)
	// UpdateRule with fromDate == "" edits the whole rule; otherwise "this and following".
	UpdateRule(id, userID uint, in RuleInput, fromDate string, today string) (*models.RecurringRule, error)
	DeleteRule(id, userID uint, fromDate string, today string) error
	// Range materializes [max(start, today), min(end, today+56)] and returns start..end.
	Range(userID uint, start, end, today string) (*RangeResult, error)
	CancelOccurrence(taskID, userID uint) error
}

type recurringService struct {
	repo   repositories.RecurringRepository
	events events.Recorder
}

// NewRecurringService creates a RecurringService that records product events with rec.
func NewRecurringService(repo repositories.RecurringRepository, rec events.Recorder) RecurringService {
	return &recurringService{repo: repo, events: rec}
}

func invalidRule(code apierr.Code, msg string) error { return apierr.Wrap(ErrInvalidRule, code, msg) }

func ruleDate(s string) (models.Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil || t.Format("2006-01-02") != s {
		return "", invalidRule(apierr.RecurringDatesInvalid, "dates must be valid YYYY-MM-DD dates")
	}
	return models.Date(s), nil
}

// normalWeekdays sorts and de-duplicates ISO weekdays; empty or out of 1-7 is rejected.
func normalWeekdays(in []int) (models.Weekdays, error) {
	out := models.Weekdays{}
	for _, d := range in {
		if d < 1 || d > 7 {
			return nil, invalidRule(apierr.RecurringWeekdaysInvalid, "weekdays must be ISO weekdays 1 (Mon) to 7 (Sun)")
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, invalidRule(apierr.RecurringWeekdaysInvalid, "at least one weekday is required")
	}
	slices.Sort(out)
	return out, nil
}

func ruleMinute(m int) error {
	if m < 0 || m > 1440 {
		return invalidRule(apierr.TaskTimeInvalid, "times must be minutes 0-1440")
	}
	return nil
}

// toEdit validates the provided fields of in.
func toEdit(in RuleInput) (repositories.RuleEdit, error) {
	var e repositories.RuleEdit
	e.CategoryID = in.CategoryID
	if in.Name != nil {
		name, err := validateTaskName(*in.Name)
		if err != nil {
			return e, err
		}
		e.Name = &name
	}
	if in.Weekdays != nil {
		w, err := normalWeekdays(in.Weekdays)
		if err != nil {
			return e, err
		}
		e.Weekdays = w
	}
	for _, m := range []*int{in.StartMin, in.EndMin} {
		if m != nil {
			if err := ruleMinute(*m); err != nil {
				return e, err
			}
		}
	}
	e.StartMin, e.EndMin = in.StartMin, in.EndMin
	if in.ValidFrom != nil {
		d, err := ruleDate(*in.ValidFrom)
		if err != nil {
			return e, err
		}
		e.ValidFrom = &d
	}
	if in.ValidTo != nil {
		d, err := ruleDate(*in.ValidTo)
		if err != nil {
			return e, err
		}
		e.ValidTo = &d
	}
	e.ClearValidTo = in.ClearValidTo
	return e, nil
}

func (s *recurringService) ListRules(userID uint) ([]models.RecurringRule, error) {
	return s.repo.ListRules(userID)
}

func (s *recurringService) CreateRule(userID uint, in RuleInput) (*models.RecurringRule, error) {
	if in.Name == nil {
		empty := ""
		in.Name = &empty
	}
	if in.Weekdays == nil {
		in.Weekdays = []int{}
	}
	if in.CategoryID == nil || in.StartMin == nil || in.EndMin == nil || in.ValidFrom == nil {
		return nil, invalidRule(apierr.ValidationFailed, "categoryId, name, weekdays, startMin, endMin and validFrom are required")
	}
	e, err := toEdit(in)
	if err != nil {
		return nil, err
	}
	rule := &models.RecurringRule{UserID: userID, CategoryID: *e.CategoryID, Name: *e.Name, Weekdays: e.Weekdays,
		StartMin: *e.StartMin, EndMin: *e.EndMin, ValidFrom: *e.ValidFrom, ValidTo: e.ValidTo}
	if rule.StartMin >= rule.EndMin {
		return nil, invalidRule(apierr.TaskEndBeforeStart, "endMin must be after startMin (blocks cannot cross midnight)")
	}
	if rule.ValidTo != nil && *rule.ValidTo < rule.ValidFrom {
		return nil, invalidRule(apierr.RecurringDatesInvalid, "validTo must not be before validFrom")
	}
	if err := s.repo.CreateRule(rule); err != nil {
		return nil, err
	}
	return rule, nil
}

func optionalDate(s string) (*models.Date, error) {
	if s == "" {
		return nil, nil
	}
	d, err := ruleDate(s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *recurringService) UpdateRule(id, userID uint, in RuleInput, fromDate, today string) (*models.RecurringRule, error) {
	e, err := toEdit(in)
	if err != nil {
		return nil, err
	}
	from, err := optionalDate(fromDate)
	if err != nil {
		return nil, err
	}
	return s.repo.UpdateRule(id, userID, e, from, models.Date(today))
}

func (s *recurringService) DeleteRule(id, userID uint, fromDate, today string) error {
	from, err := optionalDate(fromDate)
	if err != nil {
		return err
	}
	return s.repo.DeleteRule(id, userID, from, models.Date(today))
}

func (s *recurringService) Range(userID uint, start, end, today string) (*RangeResult, error) {
	ts, err1 := time.Parse("2006-01-02", start)
	te, err2 := time.Parse("2006-01-02", end)
	if err1 != nil || err2 != nil || ts.Format("2006-01-02") != start || te.Format("2006-01-02") != end || te.Before(ts) {
		return nil, invalidRule(apierr.DateInvalid, "startDate and endDate must be YYYY-MM-DD dates, startDate first")
	}
	if days := int(te.Sub(ts).Hours()/24) + 1; days > MaxRangeDays {
		return nil, invalidRule(apierr.RangeTooLong, "a range covers at most 42 days")
	}
	tt, _ := time.Parse("2006-01-02", today)
	limit := tt.AddDate(0, 0, PlanAheadDays).Format("2006-01-02")
	from, to := max(start, today), min(end, limit)
	m, err := s.repo.Materialize(userID, models.Date(from), models.Date(to))
	if err != nil {
		return nil, err
	}
	for _, t := range m.Created {
		props := events.Props{"source": t.Source, "minutes": t.EndMin - t.StartMin, "moved": false}
		if t.CategoryKind != nil {
			props["category_kind"] = *t.CategoryKind
		}
		s.events.Record(userID, events.BlockCreated, props)
	}
	if len(m.Created) > 0 {
		tf, _ := time.Parse("2006-01-02", from)
		tl, _ := time.Parse("2006-01-02", to)
		s.events.Record(userID, events.WeekPlanned, events.Props{
			"created": len(m.Created), "conflicts": m.Conflicts, "days": int(tl.Sub(tf).Hours()/24) + 1})
	}
	tasks, exceptions, err := s.repo.Range(userID, models.Date(start), models.Date(end))
	if err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []models.Task{}
	}
	return &RangeResult{Tasks: tasks, Exceptions: exceptions, Truncated: end > limit}, nil
}

func (s *recurringService) CancelOccurrence(taskID, userID uint) error {
	return s.repo.CancelOccurrence(taskID, userID)
}
