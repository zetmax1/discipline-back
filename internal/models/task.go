package models

import "time"

// Category constants
const (
	CategoryDeepWork = "deep-work"
	CategoryStudy    = "study"
	CategoryAdmin    = "admin"
	CategoryGym      = "gym"
	CategoryRest     = "rest"
)

// Status constants
const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusSkipped   = "skipped"
)

// Task represents a scheduled time block.
type Task struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	Name         string    `gorm:"not null" json:"name"`
	Category     string    `gorm:"not null" json:"category"`
	StartTime    string    `gorm:"not null" json:"startTime"`
	EndTime      string    `gorm:"not null" json:"endTime"`
	Status       string    `gorm:"not null;default:pending" json:"status"`
	Distractions int       `gorm:"not null;default:0" json:"distractions"`
	Energy       int       `gorm:"not null;default:3" json:"energy"`
	Date         string    `gorm:"not null;index" json:"date"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
