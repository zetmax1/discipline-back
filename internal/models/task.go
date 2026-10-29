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

// Source constants: who created a block.
const (
	SourceManual        = "manual"
	SourceSmartSchedule = "smart-schedule"
)

// Task represents a scheduled time block.
type Task struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	Name         string    `gorm:"not null" json:"name"`
	Category     string    `gorm:"not null" json:"category"`
	StartTime    ClockTime `gorm:"type:time;not null" json:"startTime"`
	EndTime      ClockTime `gorm:"type:time;not null" json:"endTime"`
	Status       string    `gorm:"not null;default:pending" json:"status"`
	Distractions int       `gorm:"not null;default:0" json:"distractions"`
	Energy       int       `gorm:"not null;default:3" json:"energy"`
	Date         Date      `gorm:"type:date;not null" json:"date"`
	Source       string    `gorm:"not null;default:manual" json:"source"` // manual | smart-schedule
	PlanID       *string   `gorm:"type:uuid" json:"planId,omitempty"`     // set for smart-schedule blocks
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
