package repositories

import (
	"discipline-backend/internal/models"

	"gorm.io/gorm"
)

// UserRepository defines the interface for user data access.
type UserRepository interface {
	Create(user *models.User) error
	FindByEmail(email string) (*models.User, error)
	FindByID(id uint) (*models.User, error)
	// Update writes the editable profile fields (never token_version).
	Update(user *models.User) error
	// IncrementTokenVersion atomically bumps token_version and returns the new value.
	IncrementTokenVersion(id uint) (int64, error)
}

// userRepository implements UserRepository using GORM.
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a new UserRepository.
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(user *models.User) error {
	return r.db.Create(user).Error
}

func (r *userRepository) FindByEmail(email string) (*models.User, error) {
	var user models.User
	if err := r.db.Where("email = ?", email).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) FindByID(id uint) (*models.User, error) {
	var user models.User
	if err := r.db.First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) Update(user *models.User) error {
	return r.db.Model(user).Select("name", "email", "password", "timezone", "updated_at").Updates(user).Error
}

func (r *userRepository) IncrementTokenVersion(id uint) (int64, error) {
	var version int64
	res := r.db.Raw("UPDATE users SET token_version = token_version + 1, updated_at = now() WHERE id = ? RETURNING token_version", id).Scan(&version)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return version, nil
}
