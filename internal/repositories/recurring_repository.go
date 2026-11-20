package repositories

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"discipline-backend/internal/models"
)

var (
	// ErrRuleNotFound: no such rule for this user.
	ErrRuleNotFound = errors.New("recurring rule not found")
	// ErrOccurrenceNotFound: the block is not an occurrence of a rule (or there is no conflict to resolve).
	ErrOccurrenceNotFound = errors.New("occurrence not found")
	// ErrRuleDates: a split date outside the rule's validity.
	ErrRuleDates = errors.New("date is outside the rule's validity")
)

// RuleEdit is a validated partial change to a rule (nil = unchanged). ClearValidTo removes the end.
type RuleEdit struct {
	CategoryID       *uint
	Name             *string
	Weekdays         models.Weekdays
	StartMin, EndMin *int
	ValidFrom        *models.Date
	ValidTo          *models.Date
	ClearValidTo     bool
}

// Materialized is what one range read created.
type Materialized struct {
	Created   []models.Task
	Conflicts int
}

// RecurringRepository stores recurring rules, their exceptions and materializes occurrences.
// Every method filters by userID; every multi-row write runs in one transaction.
type RecurringRepository interface {
	ListRules(userID uint) ([]models.RecurringRule, error)
	CreateRule(rule *models.RecurringRule) error
	// UpdateRule edits the whole rule (fromDate nil) or splits it at fromDate ("this and following").
	// Untouched planned occurrences from max(fromDate, today) are deleted so they are planned again.
	UpdateRule(id, userID uint, edit RuleEdit, fromDate *models.Date, today models.Date) (*models.RecurringRule, error)
	// DeleteRule removes the rule (fromDate nil) or ends it the day before fromDate.
	DeleteRule(id, userID uint, fromDate *models.Date, today models.Date) error
	// Materialize creates the occurrences missing in [from, to] (both already clamped).
	Materialize(userID uint, from, to models.Date) (Materialized, error)
	// Range returns the blocks and exceptions dated start..end.
	Range(userID uint, start, end models.Date) ([]models.Task, []models.RecurringException, error)
	// CancelOccurrence deletes an untouched occurrence (keeps a done/partial/in-progress one) and
	// records a `cancelled` exception for the date it was planned on.
	CancelOccurrence(taskID, userID uint) error
	// PlaceAnyway creates the occurrence a conflict left out, overlapping on purpose.
	PlaceAnyway(ruleID, userID uint, date models.Date) (*models.Task, error)
	// SkipConflict turns a conflict into a cancellation ("skip this week").
	SkipConflict(ruleID, userID uint, date models.Date) error
}

type recurringRepository struct{ db *gorm.DB }

// NewRecurringRepository creates a RecurringRepository.
func NewRecurringRepository(db *gorm.DB) RecurringRepository { return &recurringRepository{db: db} }

const dateLayout = "2006-01-02"

func parseDate(d models.Date) time.Time {
	t, _ := time.Parse(dateLayout, string(d))
	return t
}

func addDays(d models.Date, n int) models.Date {
	return models.Date(parseDate(d).AddDate(0, 0, n).Format(dateLayout))
}

func maxDate(a, b models.Date) models.Date {
	if a > b {
		return a
	}
	return b
}

func (r *recurringRepository) ListRules(userID uint) ([]models.RecurringRule, error) {
	var rules []models.RecurringRule
	err := r.db.Where("user_id = ?", userID).Order("valid_from, start_min, id").Find(&rules).Error
	return rules, err
}

func (r *recurringRepository) CreateRule(rule *models.RecurringRule) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if _, err := categoryKind(tx, rule.CategoryID, rule.UserID); err != nil {
			return err
		}
		return tx.Create(rule).Error
	})
}

func lockRule(tx *gorm.DB, id, userID uint) (*models.RecurringRule, error) {
	var rule models.RecurringRule
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).First(&rule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRuleNotFound
	}
	return &rule, err
}

// dropUntouched deletes the rule's occurrences from `from` on that nobody touched, and its
// conflict exceptions from then on (they are re-evaluated when the range is read again).
func dropUntouched(tx *gorm.DB, userID, ruleID uint, from models.Date) error {
	if err := tx.Exec(`DELETE FROM tasks WHERE user_id = ? AND recurring_rule_id = ? AND date >= ?
		AND status = ? AND NOT recurring_edited AND original_date IS NULL`,
		userID, ruleID, from, models.StatusPlanned).Error; err != nil {
		return err
	}
	return tx.Exec(`DELETE FROM recurring_exceptions WHERE user_id = ? AND rule_id = ? AND date >= ? AND reason = ?`,
		userID, ruleID, from, ExceptionConflict).Error
}

func applyEdit(rule *models.RecurringRule, e RuleEdit) {
	if e.CategoryID != nil {
		rule.CategoryID = *e.CategoryID
	}
	if e.Name != nil {
		rule.Name = *e.Name
	}
	if e.Weekdays != nil {
		rule.Weekdays = e.Weekdays
	}
	if e.StartMin != nil {
		rule.StartMin = *e.StartMin
	}
	if e.EndMin != nil {
		rule.EndMin = *e.EndMin
	}
	if e.ValidFrom != nil {
		rule.ValidFrom = *e.ValidFrom
	}
	if e.ClearValidTo {
		rule.ValidTo = nil
	} else if e.ValidTo != nil {
		rule.ValidTo = e.ValidTo
	}
}

// validRule re-checks what the CHECK constraints would reject, after merging an edit.
func validRule(rule *models.RecurringRule) error {
	if rule.StartMin >= rule.EndMin {
		return ErrRuleTimes
	}
	if rule.ValidTo != nil && *rule.ValidTo < rule.ValidFrom {
		return ErrRuleDates
	}
	return nil
}

// ErrRuleTimes: after an edit the rule would end before it starts.
var ErrRuleTimes = errors.New("endMin must be after startMin")

func (r *recurringRepository) UpdateRule(id, userID uint, edit RuleEdit, fromDate *models.Date, today models.Date) (*models.RecurringRule, error) {
	var out models.RecurringRule
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		old, err := lockRule(tx, id, userID)
		if err != nil {
			return err
		}
		if edit.CategoryID != nil {
			if _, err := categoryKind(tx, *edit.CategoryID, userID); err != nil {
				return err
			}
		}
		// Splitting on or before the first day is the same as editing the whole rule.
		if fromDate != nil && *fromDate <= old.ValidFrom {
			fromDate = nil
		}
		if fromDate == nil {
			out = *old
			applyEdit(&out, edit)
			if err := validRule(&out); err != nil {
				return err
			}
			if err := tx.Model(&models.RecurringRule{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]any{
				"category_id": out.CategoryID, "name": out.Name, "weekdays": out.Weekdays, "start_min": out.StartMin,
				"end_min": out.EndMin, "valid_from": out.ValidFrom, "valid_to": out.ValidTo, "updated_at": gorm.Expr("now()"),
			}).Error; err != nil {
				return err
			}
			// The past is never rewritten: only future untouched occurrences are planned again.
			return dropUntouched(tx, userID, id, today)
		}
		d := *fromDate
		if old.ValidTo != nil && d > *old.ValidTo {
			return ErrRuleDates
		}
		next := *old
		next.ID, next.SplitFromRuleID, next.CreatedAt, next.UpdatedAt = 0, &old.ID, time.Time{}, time.Time{}
		edit.ValidFrom = nil
		applyEdit(&next, edit)
		next.ValidFrom = d
		if err := validRule(&next); err != nil {
			return err
		}
		// 1. the old rule ends the day before; 2. the new one starts on d.
		if err := tx.Model(&models.RecurringRule{}).Where("id = ? AND user_id = ?", id, userID).
			Updates(map[string]any{"valid_to": addDays(d, -1), "updated_at": gorm.Expr("now()")}).Error; err != nil {
			return err
		}
		if err := tx.Create(&next).Error; err != nil {
			return err
		}
		// 3. untouched future occurrences go (the new rule plans them again).
		if err := dropUntouched(tx, userID, id, maxDate(d, today)); err != nil {
			return err
		}
		// 4. edited, done, partial, in-progress and moved ones stay, re-pointed at the new rule.
		if err := tx.Exec(`UPDATE tasks SET recurring_rule_id = ? WHERE user_id = ? AND recurring_rule_id = ?
			AND coalesce(original_date, date) >= ?`, next.ID, userID, id, d).Error; err != nil {
			return err
		}
		// 5. so do the exceptions: a cancelled Wednesday stays cancelled across the split.
		if err := tx.Exec(`UPDATE recurring_exceptions SET rule_id = ? WHERE user_id = ? AND rule_id = ? AND date >= ?`,
			next.ID, userID, id, d).Error; err != nil {
			return err
		}
		out = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *recurringRepository) DeleteRule(id, userID uint, fromDate *models.Date, today models.Date) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		rule, err := lockRule(tx, id, userID)
		if err != nil {
			return err
		}
		if fromDate == nil || *fromDate <= rule.ValidFrom {
			if err := dropUntouched(tx, userID, id, today); err != nil {
				return err
			}
			// Kept occurrences become plain blocks (ON DELETE SET NULL); exceptions go with the rule.
			return tx.Where("id = ? AND user_id = ?", id, userID).Delete(&models.RecurringRule{}).Error
		}
		d := *fromDate
		if err := tx.Model(&models.RecurringRule{}).Where("id = ? AND user_id = ?", id, userID).
			Updates(map[string]any{"valid_to": addDays(d, -1), "updated_at": gorm.Expr("now()")}).Error; err != nil {
			return err
		}
		if err := dropUntouched(tx, userID, id, maxDate(d, today)); err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM recurring_exceptions WHERE user_id = ? AND rule_id = ? AND date >= ?`, userID, id, d).Error
	})
}

type slot struct {
	rule uint
	date models.Date
}

func (r *recurringRepository) Materialize(userID uint, from, to models.Date) (Materialized, error) {
	var out Materialized
	if to < from {
		return out, nil
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		var rules []struct {
			models.RecurringRule
			Kind string
		}
		if err := tx.Raw(`SELECT r.*, c.kind FROM recurring_rules r JOIN categories c ON c.id = r.category_id
			WHERE r.user_id = ? AND r.valid_from <= ? AND (r.valid_to IS NULL OR r.valid_to >= ?) ORDER BY r.id`,
			userID, to, from).Scan(&rules).Error; err != nil {
			return err
		}
		if len(rules) == 0 {
			return nil
		}
		taken := map[slot]bool{}
		var rows []struct {
			Rule uint
			Date models.Date
		}
		// Present: a row planned for that date (original_date NULL), or one moved away from it.
		if err := tx.Raw(`SELECT recurring_rule_id AS rule, date FROM tasks WHERE user_id = ? AND recurring_rule_id IS NOT NULL
				AND original_date IS NULL AND date BETWEEN ? AND ?
			UNION SELECT recurring_rule_id, original_date FROM tasks WHERE user_id = ? AND recurring_rule_id IS NOT NULL
				AND original_date BETWEEN ? AND ?
			UNION SELECT rule_id, date FROM recurring_exceptions WHERE user_id = ? AND date BETWEEN ? AND ?`,
			userID, from, to, userID, from, to, userID, from, to).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			taken[slot{row.Rule, row.Date}] = true
		}
		for d := from; d <= to; d = addDays(d, 1) {
			iso := models.ISOWeekday(parseDate(d))
			for i := range rules {
				rule := &rules[i]
				if d < rule.ValidFrom || (rule.ValidTo != nil && d > *rule.ValidTo) || !rule.Weekdays.Has(iso) || taken[slot{rule.ID, d}] {
					continue
				}
				t := occurrence(userID, &rule.RecurringRule, rule.Kind, d)
				// Materialization never touches an existing block: a clash with an active one (F1's
				// overlap rule, which ignores skipped and moved blocks) creates nothing and is
				// recorded as a conflict the user can resolve.
				switch err := checkOverlap(tx, userID, d, t.StartTime, t.EndTime, 0); {
				case errors.Is(err, ErrTaskOverlap):
					if err := addException(tx, userID, rule.ID, d, ExceptionConflict); err != nil {
						return err
					}
					out.Conflicts++
					continue
				case err != nil:
					return err
				}
				// Two devices racing: the slot index lets exactly one insert through.
				res := tx.Clauses(clause.OnConflict{
					Columns:     []clause.Column{{Name: "user_id"}, {Name: "recurring_rule_id"}, {Name: "date"}},
					TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "recurring_rule_id IS NOT NULL AND original_date IS NULL"}}},
					DoNothing:   true,
				}).Create(&t)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 1 {
					out.Created = append(out.Created, t)
				}
			}
		}
		return nil
	})
	return out, err
}

// occurrence is the block a rule plans on date d.
func occurrence(userID uint, rule *models.RecurringRule, kind string, d models.Date) models.Task {
	ruleID, catID := rule.ID, rule.CategoryID
	return models.Task{
		UserID: userID, Name: rule.Name, Category: models.LegacyCategory(kind), CategoryID: &catID, CategoryKind: &kind,
		StartTime: models.ClockFromMinutes(rule.StartMin), EndTime: models.ClockFromMinutes(rule.EndMin),
		StartMin: rule.StartMin, EndMin: rule.EndMin, Status: models.StatusPlanned, Energy: 3, Date: d,
		Source: models.SourceRecurring, RecurringRuleID: &ruleID,
	}
}

// conflictRule locks the user's rule and checks it has a conflict exception on date.
func conflictRule(tx *gorm.DB, ruleID, userID uint, date models.Date) (*models.RecurringRule, string, error) {
	if err := lockUserTasks(tx, userID); err != nil {
		return nil, "", err
	}
	var rows []struct {
		models.RecurringRule
		Kind string
	}
	if err := tx.Raw(`SELECT r.*, c.kind FROM recurring_rules r JOIN categories c ON c.id = r.category_id
		WHERE r.id = ? AND r.user_id = ?`, ruleID, userID).Scan(&rows).Error; err != nil {
		return nil, "", err
	}
	if len(rows) == 0 {
		return nil, "", ErrRuleNotFound
	}
	var n int64
	if err := tx.Raw(`SELECT count(*) FROM recurring_exceptions WHERE user_id = ? AND rule_id = ? AND date = ? AND reason = ?`,
		userID, ruleID, date, ExceptionConflict).Scan(&n).Error; err != nil {
		return nil, "", err
	}
	if n == 0 {
		return nil, "", ErrOccurrenceNotFound
	}
	return &rows[0].RecurringRule, rows[0].Kind, nil
}

func (r *recurringRepository) PlaceAnyway(ruleID, userID uint, date models.Date) (*models.Task, error) {
	var out models.Task
	err := r.db.Transaction(func(tx *gorm.DB) error {
		rule, kind, err := conflictRule(tx, ruleID, userID, date)
		if err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM recurring_exceptions WHERE user_id = ? AND rule_id = ? AND date = ?`,
			userID, ruleID, date).Error; err != nil {
			return err
		}
		out = occurrence(userID, rule, kind, date) // overlapping on purpose: no overlap check
		return tx.Create(&out).Error
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *recurringRepository) SkipConflict(ruleID, userID uint, date models.Date) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if _, _, err := conflictRule(tx, ruleID, userID, date); err != nil {
			return err
		}
		return addException(tx, userID, ruleID, date, ExceptionCancelled)
	})
}

func (r *recurringRepository) Range(userID uint, start, end models.Date) ([]models.Task, []models.RecurringException, error) {
	var tasks []models.Task
	if err := r.db.Where("user_id = ? AND date BETWEEN ? AND ?", userID, start, end).
		Order("date, start_min, id").Find(&tasks).Error; err != nil {
		return nil, nil, err
	}
	if err := attachKindsList(r.db, tasks); err != nil {
		return nil, nil, err
	}
	exceptions := []models.RecurringException{}
	if err := r.db.Raw(`SELECT rule_id, date, reason FROM recurring_exceptions WHERE user_id = ? AND date BETWEEN ? AND ?
		ORDER BY date, rule_id`, userID, start, end).Scan(&exceptions).Error; err != nil {
		return nil, nil, err
	}
	return tasks, exceptions, nil
}

func (r *recurringRepository) CancelOccurrence(taskID, userID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		var t models.Task
		if err := tx.Where("id = ? AND user_id = ?", taskID, userID).First(&t).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOccurrenceNotFound
			}
			return err
		}
		if t.RecurringRuleID == nil {
			return ErrOccurrenceNotFound
		}
		day := t.Date
		if t.OriginalDate != nil {
			day = *t.OriginalDate
		}
		switch t.Status {
		case models.StatusDone, models.StatusPartial, models.StatusInProgress, models.StatusMoved:
			// What happened stays; only the plan is cancelled.
		default:
			if err := tx.Where("id = ? AND user_id = ?", taskID, userID).Delete(&models.Task{}).Error; err != nil {
				return err
			}
		}
		return addException(tx, userID, *t.RecurringRuleID, day, ExceptionCancelled)
	})
}
