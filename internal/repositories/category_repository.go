package repositories

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"discipline-backend/internal/models"
)

var (
	// ErrCategoryNameTaken: another category of the user has this name (case-insensitively).
	ErrCategoryNameTaken = errors.New("a category with this name already exists")
	// ErrCategoryInUse: blocks or recurring rules still point at the category.
	ErrCategoryInUse = errors.New("category is still used by blocks or recurring rules")
	// ErrCategoryInvalid: a category id in a request that is not one of the user's (or not allowed).
	ErrCategoryInvalid = errors.New("category is not valid here")
)

// CategoryRepository stores a user's categories. Every method filters by userID.
type CategoryRepository interface {
	// List ensures the six defaults exist, then returns every category: defaults first, in
	// their fixed order, then the user's own by name.
	List(userID uint) ([]models.Category, error)
	FindByID(id, userID uint) (*models.Category, error)
	// Create inserts a category. With an idempotency key, a replay fills c with the category the
	// first call created.
	Create(c *models.Category, key string) error
	// Update writes the given columns; nameCustom is set by the caller.
	Update(id, userID uint, cols map[string]any) (*models.Category, error)
	// Delete removes an unused category, or with reassignTo moves its blocks and rules there
	// first, in one transaction.
	Delete(id, userID uint, reassignTo *uint) error
}

type categoryRepository struct{ db *gorm.DB }

// NewCategoryRepository creates a CategoryRepository.
func NewCategoryRepository(db *gorm.DB) CategoryRepository { return &categoryRepository{db: db} }

// EnsureDefaultCategories gives the user the six defaults (design §2.4), in tx. For each default:
// 1. adopt an existing category of the same kind and name that has no system key yet (made by the
// backfill), keeping its id; 2. insert what is still missing; 3. skip a default whose name the
// user already owns with another kind. Running it again changes nothing.
func EnsureDefaultCategories(tx *gorm.DB, userID uint) error {
	var have int64
	if err := tx.Raw(`SELECT count(*) FROM categories WHERE user_id = ? AND system_key IS NOT NULL`, userID).
		Scan(&have).Error; err != nil {
		return err
	}
	if have >= 6 {
		return nil
	}
	if err := tx.Exec(`UPDATE categories c SET system_key = d.system_key, updated_at = now()
		  FROM category_defaults d
		 WHERE c.user_id = ? AND c.system_key IS NULL AND c.kind = d.kind AND lower(c.name) = lower(d.name)
		   AND NOT EXISTS (SELECT 1 FROM categories o WHERE o.user_id = c.user_id AND o.system_key = d.system_key)`,
		userID).Error; err != nil {
		return err
	}
	return tx.Exec(`INSERT INTO categories (user_id, name, color, icon, kind, system_key)
		SELECT ?, d.name, d.color, d.icon, d.kind, d.system_key FROM category_defaults d ORDER BY d.position
		ON CONFLICT DO NOTHING`, userID).Error
}

// personalCategoryID returns the user's Personal default (creating the defaults if needed), or 0
// when the user owns the name "Personal" with another kind and the default was skipped.
func personalCategoryID(tx *gorm.DB, userID uint) (uint, error) {
	if err := EnsureDefaultCategories(tx, userID); err != nil {
		return 0, err
	}
	var ids []uint
	err := tx.Raw(`SELECT id FROM categories WHERE user_id = ? AND system_key = ?`, userID, models.SystemKeyPersonal).
		Scan(&ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// legacyCategoryID finds (or creates) the user's category for a legacy tasks.category string,
// in the trigger's order (migration 00014): 1. the mapped system key, 2. legacy_name, 3. the name,
// case-insensitively, 4. a new category of the mapped kind (personal when unknown). An empty
// string means Personal. The API resolves this itself so its writes do not depend on the trigger,
// which migration 00015 drops (F13 review M3).
func legacyCategoryID(tx *gorm.DB, userID uint, legacy string) (uint, error) {
	if strings.TrimSpace(legacy) == "" {
		return personalCategoryID(tx, userID)
	}
	if err := EnsureDefaultCategories(tx, userID); err != nil {
		return 0, err
	}
	var ids []uint
	// The trigger's lookup: want = the mapped name/kind, else the trimmed string and personal.
	const want = `WITH k AS (SELECT left(lower(btrim(?::text)), 64) AS legacy, left(btrim(?::text), 64) AS raw),
		w AS (SELECT k.legacy, coalesce(m.name, k.raw) AS name, coalesce(m.kind, 'personal') AS kind, m.system_key
		        FROM k LEFT JOIN category_legacy_map m ON m.legacy = k.legacy)`
	if err := tx.Raw(want+`
		SELECT c.id FROM categories c, w
		 WHERE c.user_id = ? AND (c.system_key = w.system_key OR c.legacy_name = w.legacy OR lower(c.name) = lower(w.name))
		 ORDER BY (c.system_key IS DISTINCT FROM w.system_key), (c.legacy_name IS DISTINCT FROM w.legacy)
		 LIMIT 1`, legacy, legacy, userID).Scan(&ids).Error; err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		return ids[0], nil
	}
	if err := tx.Raw(want+`
		INSERT INTO categories (user_id, name, color, icon, kind, system_key, legacy_name, name_custom)
		SELECT ?, w.name, d.color, d.icon, w.kind, w.system_key, w.legacy, false
		  FROM w JOIN category_defaults d ON d.kind = w.kind
		ON CONFLICT DO NOTHING RETURNING id`, legacy, legacy, userID).Scan(&ids).Error; err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		return ids[0], nil
	}
	// Lost a race (or the name exists under another kind): use the category with that name.
	if err := tx.Raw(want+`
		SELECT c.id FROM categories c, w WHERE c.user_id = ? AND lower(c.name) = lower(w.name)`,
		legacy, legacy, userID).Scan(&ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, ErrCategoryInvalid
	}
	return ids[0], nil
}

// categoryKind returns the kind of one of the user's categories; ErrCategoryInvalid if it is not theirs.
func categoryKind(tx *gorm.DB, id, userID uint) (string, error) {
	var kinds []string
	if err := tx.Raw(`SELECT kind FROM categories WHERE id = ? AND user_id = ?`, id, userID).Scan(&kinds).Error; err != nil {
		return "", err
	}
	if len(kinds) == 0 {
		return "", ErrCategoryInvalid
	}
	return kinds[0], nil
}

// attachKinds fills CategoryKind for blocks that have a category (one query for the batch).
func attachKinds(db *gorm.DB, tasks []*models.Task) error {
	ids := []uint{}
	for _, t := range tasks {
		if t.CategoryID != nil {
			ids = append(ids, *t.CategoryID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var rows []struct {
		ID   uint
		Kind string
	}
	if err := db.Raw(`SELECT id, kind FROM categories WHERE id IN ?`, ids).Scan(&rows).Error; err != nil {
		return err
	}
	kinds := make(map[uint]string, len(rows))
	for _, r := range rows {
		kinds[r.ID] = r.Kind
	}
	for _, t := range tasks {
		if t.CategoryID != nil {
			if k, ok := kinds[*t.CategoryID]; ok {
				t.CategoryKind = &k
			}
		}
	}
	return nil
}

func attachKindsList(db *gorm.DB, tasks []models.Task) error {
	ptrs := make([]*models.Task, len(tasks))
	for i := range tasks {
		ptrs[i] = &tasks[i]
	}
	return attachKinds(db, ptrs)
}

// isUniqueViolation reports a unique-index violation (on any index when names is empty).
func isUniqueViolation(err error, names ...string) bool {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		return false
	}
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if pg.ConstraintName == n {
			return true
		}
	}
	return false
}

func (r *categoryRepository) List(userID uint) ([]models.Category, error) {
	var out []models.Category
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := EnsureDefaultCategories(tx, userID); err != nil {
			return err
		}
		return tx.Raw(`SELECT c.* FROM categories c LEFT JOIN category_defaults d ON d.system_key = c.system_key
			WHERE c.user_id = ? ORDER BY d.position NULLS LAST, lower(c.name), c.id`, userID).Scan(&out).Error
	})
	return out, err
}

func (r *categoryRepository) FindByID(id, userID uint) (*models.Category, error) {
	var c models.Category
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *categoryRepository) Create(c *models.Category, key string) error {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		userID := c.UserID
		if key != "" {
			if err := lockForKey(tx, userID); err != nil {
				return err
			}
			earlier, found, err := earlierResource(tx, userID, key)
			if err != nil {
				return err
			}
			if found {
				*c = models.Category{}
				return tx.Where("id = ? AND user_id = ?", earlier, userID).First(c).Error
			}
		}
		// The defaults first, so a user's own "Gym" cannot take a default's name before it exists.
		if err := EnsureDefaultCategories(tx, userID); err != nil {
			return err
		}
		if err := tx.Omit("SystemKey", "LegacyName").Create(c).Error; err != nil {
			return err
		}
		if key == "" {
			return nil
		}
		return rememberKey(tx, userID, key, c.ID)
	})
	if isUniqueViolation(err, "uq_categories_user_name") {
		return ErrCategoryNameTaken
	}
	return err
}

func (r *categoryRepository) Update(id, userID uint, cols map[string]any) (*models.Category, error) {
	var out models.Category
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).
			First(&out).Error; err != nil {
			return err
		}
		if len(cols) == 0 {
			return nil
		}
		cols["updated_at"] = gorm.Expr("now()")
		if err := tx.Model(&models.Category{}).Where("id = ? AND user_id = ?", id, userID).Updates(cols).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND user_id = ?", id, userID).First(&out).Error
	})
	if isUniqueViolation(err, "uq_categories_user_name") {
		return nil, ErrCategoryNameTaken
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *categoryRepository) Delete(id, userID uint, reassignTo *uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		var c models.Category
		if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&c).Error; err != nil {
			return err
		}
		// A default would come straight back (they are created lazily): rename it instead.
		if c.SystemKey != nil {
			return ErrCategoryInvalid
		}
		if reassignTo != nil {
			if *reassignTo == id {
				return ErrCategoryInvalid
			}
			kind, err := categoryKind(tx, *reassignTo, userID)
			if err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE tasks SET category_id = ?, category = ? WHERE user_id = ? AND category_id = ?`,
				*reassignTo, models.LegacyCategory(kind), userID, id).Error; err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE recurring_rules SET category_id = ?, updated_at = now() WHERE user_id = ? AND category_id = ?`,
				*reassignTo, userID, id).Error; err != nil {
				return err
			}
			// F9a: the weekly target's minutes move too (capped at 80 h); the old row goes with the category.
			if err := tx.Exec(`INSERT INTO category_targets (user_id, category_id, minutes_per_week)
				SELECT user_id, ?, minutes_per_week FROM category_targets WHERE user_id = ? AND category_id = ?
				ON CONFLICT (user_id, category_id) DO UPDATE
				SET minutes_per_week = least(4800, category_targets.minutes_per_week + excluded.minutes_per_week)`,
				*reassignTo, userID, id).Error; err != nil {
				return err
			}
		} else {
			var used int64
			if err := tx.Raw(`SELECT (SELECT count(*) FROM tasks WHERE category_id = ?) + (SELECT count(*) FROM recurring_rules WHERE category_id = ?)`,
				id, id).Scan(&used).Error; err != nil {
				return err
			}
			if used > 0 {
				return ErrCategoryInUse
			}
		}
		res := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&models.Category{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}
