package repositories

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"discipline-backend/internal/models"
)

// MaxCommitments is the most commitments one user keeps (F9a S1).
const MaxCommitments = 100

var (
	ErrCommitmentNotFound = errors.New("commitment not found")
	ErrCommitmentLimit    = errors.New("commitment limit reached")
)

// CommitmentRepository stores commitments; every query is scoped by the caller's user id.
type CommitmentRepository interface {
	List(userID uint) ([]models.Commitment, error)
	// InRange returns the commitments whose start days can touch [from-1, to].
	InRange(userID uint, from, to models.Date) ([]models.Commitment, error)
	// Create inserts c; with a key, a replay returns the first row (created = false).
	Create(c *models.Commitment, key string) (created bool, err error)
	Update(id, userID uint, apply func(*models.Commitment)) (*models.Commitment, error)
	Delete(id, userID uint) error
}

type commitmentRepository struct{ db *gorm.DB }

func NewCommitmentRepository(db *gorm.DB) CommitmentRepository { return &commitmentRepository{db: db} }

func (r *commitmentRepository) List(userID uint) ([]models.Commitment, error) {
	out := []models.Commitment{}
	err := r.db.Where("user_id = ?", userID).Order("start_min, id").Find(&out).Error
	return out, err
}

func (r *commitmentRepository) InRange(userID uint, from, to models.Date) ([]models.Commitment, error) {
	out := []models.Commitment{}
	err := r.db.Where("user_id = ? AND valid_from <= ? AND (valid_to IS NULL OR valid_to >= (?::date - 1))", userID, to, from).
		Order("id").Find(&out).Error
	return out, err
}

// lockCommitments serialises one user's commitment creates (limit check + idempotency key).
func lockCommitments(tx *gorm.DB, userID uint) error {
	return tx.Exec(`SELECT id FROM users WHERE id = ? FOR NO KEY UPDATE`, userID).Error
}

func (r *commitmentRepository) Create(c *models.Commitment, key string) (bool, error) {
	created := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockCommitments(tx, c.UserID); err != nil {
			return err
		}
		if key != "" {
			earlier, found, err := earlierResource(tx, c.UserID, key)
			if err != nil {
				return err
			}
			if found {
				userID := c.UserID
				*c = models.Commitment{}
				err := tx.Where("id = ? AND user_id = ?", earlier, userID).First(c).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrCommitmentNotFound
				}
				return err
			}
		}
		var n int64
		if err := tx.Model(&models.Commitment{}).Where("user_id = ?", c.UserID).Count(&n).Error; err != nil {
			return err
		}
		if n >= MaxCommitments {
			return ErrCommitmentLimit
		}
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		created = true
		if key == "" {
			return nil
		}
		return rememberKey(tx, c.UserID, key, c.ID)
	})
	return created, err
}

func (r *commitmentRepository) Update(id, userID uint, apply func(*models.Commitment)) (*models.Commitment, error) {
	var c models.Commitment
	err := r.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).First(&c).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCommitmentNotFound
		}
		if err != nil {
			return err
		}
		apply(&c)
		// Source, calendar source and external key stay: an imported commitment edited in the app
		// shows as "edited in the app" on the next refresh.
		return tx.Model(&models.Commitment{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]any{
			"name": c.Name, "kind": c.Kind, "weekdays": c.Weekdays, "start_min": c.StartMin, "end_min": c.EndMin,
			"valid_from": c.ValidFrom, "valid_to": c.ValidTo, "updated_at": gorm.Expr("now()"),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *commitmentRepository) Delete(id, userID uint) error {
	res := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&models.Commitment{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCommitmentNotFound
	}
	return nil
}
