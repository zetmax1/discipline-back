package models

import "time"

// Checkin is the morning check-in for one local day (F1).
type Checkin struct {
	ID            uint      `gorm:"primaryKey" json:"-"`
	UserID        uint      `gorm:"not null" json:"-"`
	Date          Date      `gorm:"type:date;not null" json:"date"`
	Skipped       bool      `gorm:"not null" json:"skipped"`
	SleepStartMin *int      `json:"sleepStartMin"` // > SleepEndMin: began the evening before
	SleepEndMin   *int      `json:"sleepEndMin"`
	SleepMinutes  *int      `gorm:"-" json:"sleepMinutes"` // real elapsed time, computed in the user's zone
	SleepQuality  *int      `json:"sleepQuality"`
	Energy        *int      `json:"energy"`
	Note          *string   `json:"note"`
	CreatedAt     time.Time `json:"-"`
	UpdatedAt     time.Time `json:"-"`
}

// Focus session outcomes.
const (
	SessionCompleted = "completed"
	SessionAbandoned = "abandoned"
)

// FocusSession is a timed work interval, optionally inside a block (F1). The id is made by the client.
type FocusSession struct {
	ID             string    `gorm:"type:uuid;primaryKey"`
	UserID         uint      `gorm:"not null"`
	BlockID        *uint     `gorm:"column:block_id"`
	StartedAt      time.Time `gorm:"not null"`
	PlannedMinutes int       `gorm:"not null"`
	BreakMinutes   int       `gorm:"not null"`
	EndedAt        *time.Time
	Outcome        *string
	Distractions   int `gorm:"not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ParkingNote is a thought parked during a session (F10 turns these into inbox tasks).
type ParkingNote struct {
	ID        string  `gorm:"type:uuid;primaryKey"`
	UserID    uint    `gorm:"not null"`
	SessionID *string `gorm:"type:uuid"`
	Text      string  `gorm:"not null"`
	CreatedAt time.Time
}

func (ParkingNote) TableName() string { return "parking_lot" }
