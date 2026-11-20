package models

import "time"

// Category kinds: metrics and rules use the kind, never the name (VISION glossary).
const (
	KindFocus    = "focus"
	KindStudy    = "study"
	KindAdmin    = "admin"
	KindExercise = "exercise"
	KindRest     = "rest"
	KindPersonal = "personal"
)

// CategoryKinds lists every kind.
var CategoryKinds = []string{KindFocus, KindStudy, KindAdmin, KindExercise, KindRest, KindPersonal}

// CategoryIcons lists the icons a category may use (matches chk_categories_icon).
var CategoryIcons = []string{
	"target", "book-open", "briefcase", "dumbbell", "coffee", "user", "graduation-cap", "heart",
	"home", "moon", "music", "palette", "plane", "shopping-cart", "users", "wrench",
}

// SystemKeyPersonal is the default category a block without one falls back to.
const SystemKeyPersonal = "personal"

// legacyByKind is the tasks.category string the API writes next to category_id while the legacy
// column exists, so the previous API and analytics versions keep reading a known value.
var legacyByKind = map[string]string{
	KindFocus: CategoryDeepWork, KindStudy: CategoryStudy, KindAdmin: CategoryAdmin,
	KindExercise: CategoryGym, KindRest: CategoryRest, KindPersonal: "personal",
}

// LegacyCategory returns the legacy tasks.category string for a kind.
func LegacyCategory(kind string) string {
	if s, ok := legacyByKind[kind]; ok {
		return s
	}
	return "personal"
}

// Category is a user's block category: editable name, colour and icon, a fixed kind.
type Category struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"not null" json:"-"`
	Name       string    `gorm:"not null" json:"name"`
	Color      string    `gorm:"type:char(7);not null" json:"color"`
	Icon       string    `gorm:"not null" json:"icon"`
	Kind       string    `gorm:"not null" json:"kind"`
	SystemKey  *string   `json:"systemKey"`
	NameCustom bool      `gorm:"not null;default:false" json:"nameCustom"`
	LegacyName *string   `json:"-"`
	CreatedAt  time.Time `json:"-"`
	UpdatedAt  time.Time `json:"-"`
}
