package repositories

import (
	"discipline-backend/internal/models"

	"gorm.io/gorm"
)

// VocabRepository defines the interface for vocabulary data access.
type VocabRepository interface {
	Create(word *models.VocabularyWord) error
	FindByUserAndDate(userID uint, date string) ([]models.VocabularyWord, error)
	FindByID(id, userID uint) (*models.VocabularyWord, error)
	Update(word *models.VocabularyWord) error
	FindAllByUser(userID uint) ([]models.VocabularyWord, error)
	FindAllByUserPaginated(userID uint, lang string, page, pageSize int) ([]models.VocabularyWord, int64, error)
}

type vocabRepository struct {
	db *gorm.DB
}

// NewVocabRepository creates a new VocabRepository.
func NewVocabRepository(db *gorm.DB) VocabRepository {
	return &vocabRepository{db: db}
}

func (r *vocabRepository) Create(word *models.VocabularyWord) error {
	return r.db.Create(word).Error
}

func (r *vocabRepository) FindByUserAndDate(userID uint, date string) ([]models.VocabularyWord, error) {
	var words []models.VocabularyWord
	if err := r.db.Where("user_id = ? AND date = ?", userID, date).Find(&words).Error; err != nil {
		return nil, err
	}
	return words, nil
}

func (r *vocabRepository) FindByID(id, userID uint) (*models.VocabularyWord, error) {
	var word models.VocabularyWord
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&word).Error; err != nil {
		return nil, err
	}
	return &word, nil
}

func (r *vocabRepository) Update(word *models.VocabularyWord) error {
	return r.db.Save(word).Error
}

func (r *vocabRepository) FindAllByUser(userID uint) ([]models.VocabularyWord, error) {
	var words []models.VocabularyWord
	if err := r.db.Where("user_id = ?", userID).Order("date DESC").Find(&words).Error; err != nil {
		return nil, err
	}
	return words, nil
}

// FindAllByUserPaginated returns paginated vocabulary words with optional language filter.
func (r *vocabRepository) FindAllByUserPaginated(userID uint, lang string, page, pageSize int) ([]models.VocabularyWord, int64, error) {
	var words []models.VocabularyWord
	var total int64

	query := r.db.Where("user_id = ?", userID)
	if lang != "" {
		query = query.Where("language = ?", lang)
	}

	// Count total
	if err := query.Model(&models.VocabularyWord{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Fetch page
	offset := (page - 1) * pageSize
	if err := query.Order("date DESC, id DESC").Offset(offset).Limit(pageSize).Find(&words).Error; err != nil {
		return nil, 0, err
	}

	return words, total, nil
}
