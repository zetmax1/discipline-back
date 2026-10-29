package models

import "time"

// VocabularyWord represents a daily vocabulary word.
type VocabularyWord struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	Word         string    `gorm:"not null" json:"word"`
	Language     string    `gorm:"not null" json:"language"`       // "english" or "russian"
	Definition   string    `gorm:"not null" json:"definition"`
	Example      string    `json:"example"`
	Synonym      string    `json:"synonym,omitempty"`
	PartOfSpeech string    `json:"partOfSpeech,omitempty"`         // noun, verb, adjective, etc.
	Date         string    `gorm:"not null;index" json:"date"`     // YYYY-MM-DD
	Learned      bool      `gorm:"default:false" json:"learned"`
	Saved        bool      `gorm:"default:false" json:"saved"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
