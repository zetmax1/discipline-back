package models

import (
	"time"

	"gorm.io/gorm"
)

// Category constants
const (
	CategoryDeepWork = "deep-work"
	CategoryStudy    = "study"
	CategoryAdmin    = "admin"
	CategoryGym      = "gym"
	CategoryRest     = "rest"
)

// Block statuses: planned -> in_progress -> done | partial | skipped | moved.
const (
	StatusPlanned    = "planned"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusPartial    = "partial"
	StatusSkipped    = "skipped"
	StatusMoved      = "moved" // replaced by the block whose MovedFromID points here
)

// Legacy status names, still accepted as input for one release (mapped to planned / done).
const (
	LegacyStatusPending   = "pending"
	LegacyStatusCompleted = "completed"
)

// OccupiesTime reports whether a block with this status counts for overlap checks.
func OccupiesTime(status string) bool { return status != StatusSkipped && status != StatusMoved }

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
	StartTime    ClockTime `gorm:"type:time;not null" json:"startTime"` // kept until the TIME columns are dropped
	EndTime      ClockTime `gorm:"type:time;not null" json:"endTime"`
	StartMin     int       `gorm:"not null" json:"startMin"` // minutes 0-1440
	EndMin       int       `gorm:"not null" json:"endMin"`
	Status       string    `gorm:"not null;default:planned" json:"status"`
	Distractions int       `gorm:"not null;default:0" json:"distractions"`
	Energy       int       `gorm:"not null;default:3" json:"energy"`
	Date         Date      `gorm:"type:date;not null" json:"date"`
	Source       string    `gorm:"not null;default:manual" json:"source"` // manual | smart-schedule
	PlanID       *string   `gorm:"type:uuid" json:"planId,omitempty"`     // set for smart-schedule blocks
	// Lifecycle (F1): what actually happened.
	ActualStartMin *int      `json:"actualStartMin"`
	ActualEndMin   *int      `json:"actualEndMin"`
	FocusRating    *int      `json:"focusRating"` // 1-5, asked after done/partial
	Note           *string   `json:"note"`
	MovedFromID    *uint     `json:"movedFromId"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// BeforeSave keeps the minute columns in step with StartTime/EndTime on inserts and full saves
// (partial updates of start_time/end_time are synced by the database trigger).
func (t *Task) BeforeSave(*gorm.DB) error {
	if m, ok := t.StartTime.Minutes(); ok {
		t.StartMin = m
	}
	if m, ok := t.EndTime.Minutes(); ok {
		t.EndMin = m
	}
	return nil
}
