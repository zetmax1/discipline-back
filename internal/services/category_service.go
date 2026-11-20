package services

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrInvalidCategory is wrapped by every category validation failure.
var ErrInvalidCategory = errors.New("invalid category")

const maxCategoryNameChars = 64

var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// CategoryInput is a create (all fields) or an update (nil = not provided).
type CategoryInput struct {
	Name, Color, Icon, Kind *string
}

// CategoryService validates and stores a user's categories.
type CategoryService interface {
	List(userID uint) ([]models.Category, error)
	// Create with an idempotency key returns the first result on a replay.
	Create(userID uint, in CategoryInput, key string) (*models.Category, error)
	Update(id, userID uint, in CategoryInput) (*models.Category, error)
	Delete(id, userID uint, reassignTo *uint) error
}

type categoryService struct {
	repo repositories.CategoryRepository
}

// NewCategoryService creates a CategoryService.
func NewCategoryService(repo repositories.CategoryRepository) CategoryService {
	return &categoryService{repo: repo}
}

func invalidCategory(code apierr.Code, msg string) error {
	return apierr.Wrap(ErrInvalidCategory, code, msg)
}

func categoryName(s *string) (string, error) {
	if s == nil {
		return "", invalidCategory(apierr.CategoryInvalid, "name is required")
	}
	name := strings.TrimSpace(*s)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxCategoryNameChars {
		return "", invalidCategory(apierr.CategoryInvalid, "name must be 1-64 characters")
	}
	return name, nil
}

func categoryColor(s *string) (string, error) {
	if s == nil || !colorPattern.MatchString(*s) {
		return "", invalidCategory(apierr.CategoryColorInvalid, "color must be #RRGGBB")
	}
	return strings.ToUpper(*s), nil
}

func categoryIcon(s *string) (string, error) {
	if s == nil || !slices.Contains(models.CategoryIcons, *s) {
		return "", invalidCategory(apierr.CategoryIconInvalid, "icon is not one of the supported icons")
	}
	return *s, nil
}

func (s *categoryService) List(userID uint) ([]models.Category, error) { return s.repo.List(userID) }

func (s *categoryService) Create(userID uint, in CategoryInput, key string) (*models.Category, error) {
	name, err := categoryName(in.Name)
	if err != nil {
		return nil, err
	}
	color, err := categoryColor(in.Color)
	if err != nil {
		return nil, err
	}
	icon, err := categoryIcon(in.Icon)
	if err != nil {
		return nil, err
	}
	if in.Kind == nil || !slices.Contains(models.CategoryKinds, *in.Kind) {
		return nil, invalidCategory(apierr.CategoryKindInvalid, "kind must be one of focus, study, admin, exercise, rest, personal")
	}
	c := &models.Category{UserID: userID, Name: name, Color: color, Icon: icon, Kind: *in.Kind, NameCustom: true}
	if err := s.repo.Create(c, key); err != nil {
		return nil, err
	}
	return c, nil
}

// Update writes only the provided fields. The kind never changes; a new name marks it custom.
func (s *categoryService) Update(id, userID uint, in CategoryInput) (*models.Category, error) {
	if in.Kind != nil {
		return nil, invalidCategory(apierr.CategoryKindInvalid, "kind cannot be changed; create a new category instead")
	}
	cols := map[string]any{}
	if in.Name != nil {
		name, err := categoryName(in.Name)
		if err != nil {
			return nil, err
		}
		cols["name"], cols["name_custom"] = name, true
	}
	if in.Color != nil {
		color, err := categoryColor(in.Color)
		if err != nil {
			return nil, err
		}
		cols["color"] = color
	}
	if in.Icon != nil {
		icon, err := categoryIcon(in.Icon)
		if err != nil {
			return nil, err
		}
		cols["icon"] = icon
	}
	return s.repo.Update(id, userID, cols)
}

func (s *categoryService) Delete(id, userID uint, reassignTo *uint) error {
	return s.repo.Delete(id, userID, reassignTo)
}
