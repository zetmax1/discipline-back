package repositories

import (
	"errors"

	"gorm.io/gorm"
)

// ErrTargetCategory: a target names a category that is not the caller's.
var ErrTargetCategory = errors.New("target category is not one of yours")

// CategoryMinutes is one category's weekly target.
type CategoryMinutes struct {
	CategoryID     uint `json:"categoryId"`
	MinutesPerWeek int  `json:"minutesPerWeek"`
}

// Targets is a user's saved weekly targets; Saved is false when nothing was ever saved.
type Targets struct {
	Saved             bool
	Categories        []CategoryMinutes
	MaxFocusMinPerDay *int
	RestDay           *int
}

// TargetRepository stores weekly targets (F9a S3), scoped by the caller's user id.
type TargetRepository interface {
	Get(userID uint) (Targets, error)
	// Save replaces every category row and the per-day settings in one transaction.
	Save(userID uint, t Targets) error
	// SystemCategories returns the user's default categories by system key (creating them if needed).
	SystemCategories(userID uint) (map[string]uint, error)
	// ApplyIfNone saves t only when nothing is saved yet (local import); it reports whether it did.
	ApplyIfNone(userID uint, t Targets) (bool, error)
	// CategoryKinds maps the user's category ids to their kind.
	CategoryKinds(userID uint) (map[uint]string, error)
}

type targetRepository struct{ db *gorm.DB }

func NewTargetRepository(db *gorm.DB) TargetRepository { return &targetRepository{db: db} }

func (r *targetRepository) Get(userID uint) (Targets, error) {
	var rows []struct {
		MaxFocusMinPerDay *int
		RestDay           *int
	}
	if err := r.db.Raw(`SELECT max_focus_min_per_day, rest_day FROM weekly_targets WHERE user_id = ?`, userID).Scan(&rows).Error; err != nil {
		return Targets{}, err
	}
	t := Targets{Categories: []CategoryMinutes{}}
	if len(rows) == 1 {
		t.Saved, t.MaxFocusMinPerDay, t.RestDay = true, rows[0].MaxFocusMinPerDay, rows[0].RestDay
	}
	err := r.db.Raw(`SELECT category_id, minutes_per_week FROM category_targets WHERE user_id = ? ORDER BY category_id`, userID).
		Scan(&t.Categories).Error
	return t, err
}

func save(tx *gorm.DB, userID uint, t Targets) error {
	ids := make([]uint, 0, len(t.Categories))
	for _, c := range t.Categories {
		ids = append(ids, c.CategoryID)
	}
	if len(ids) > 0 {
		var mine int64
		if err := tx.Raw(`SELECT count(*) FROM categories WHERE user_id = ? AND id IN ?`, userID, ids).Scan(&mine).Error; err != nil {
			return err
		}
		if int(mine) != len(ids) {
			return ErrTargetCategory
		}
	}
	if err := tx.Exec(`INSERT INTO weekly_targets (user_id, max_focus_min_per_day, rest_day) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET max_focus_min_per_day = excluded.max_focus_min_per_day,
		rest_day = excluded.rest_day, updated_at = now()`, userID, t.MaxFocusMinPerDay, t.RestDay).Error; err != nil {
		return err
	}
	if err := tx.Exec(`DELETE FROM category_targets WHERE user_id = ?`, userID).Error; err != nil {
		return err
	}
	for _, c := range t.Categories {
		if err := tx.Exec(`INSERT INTO category_targets (user_id, category_id, minutes_per_week) VALUES (?, ?, ?)`,
			userID, c.CategoryID, c.MinutesPerWeek).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *targetRepository) Save(userID uint, t Targets) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockCommitments(tx, userID); err != nil {
			return err
		}
		return save(tx, userID, t)
	})
}

func (r *targetRepository) ApplyIfNone(userID uint, t Targets) (bool, error) {
	applied := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockCommitments(tx, userID); err != nil {
			return err
		}
		var n int64
		if err := tx.Raw(`SELECT (SELECT count(*) FROM weekly_targets WHERE user_id = ?) + (SELECT count(*) FROM category_targets WHERE user_id = ?)`,
			userID, userID).Scan(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		if err := save(tx, userID, t); err != nil {
			return err
		}
		applied = true
		return tx.Exec(`UPDATE weekly_targets SET local_import_at = now() WHERE user_id = ?`, userID).Error
	})
	return applied, err
}

func (r *targetRepository) SystemCategories(userID uint) (map[string]uint, error) {
	out := map[string]uint{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := EnsureDefaultCategories(tx, userID); err != nil {
			return err
		}
		var rows []struct {
			SystemKey string
			ID        uint
		}
		if err := tx.Raw(`SELECT system_key, id FROM categories WHERE user_id = ? AND system_key IS NOT NULL`, userID).Scan(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			out[r.SystemKey] = r.ID
		}
		return nil
	})
	return out, err
}

func (r *targetRepository) CategoryKinds(userID uint) (map[uint]string, error) {
	var rows []struct {
		ID   uint
		Kind string
	}
	if err := r.db.Raw(`SELECT id, kind FROM categories WHERE user_id = ?`, userID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Kind
	}
	return out, nil
}
