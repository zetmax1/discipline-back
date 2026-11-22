package models

import (
	"encoding/json"
	"time"
)

// Commitment sources (F9a).
const (
	CommitmentManual      = "manual"
	CommitmentLocalImport = "local_import"
	CommitmentICSFile     = "ics_file"
	CommitmentICSURL      = "ics_url"
)

// Commitment is a recurring fixed time the planner must not use (sleep, university, work).
// Wall-clock minutes in the user's zone; EndMin < StartMin crosses midnight; validity dates apply
// to the start day. Kind is one of the category kinds (colour, later metrics), not a category.
type Commitment struct {
	ID               uint      `gorm:"primaryKey"`
	UserID           uint      `gorm:"not null"`
	Name             string    `gorm:"not null"`
	Kind             string    `gorm:"not null"`
	Weekdays         Weekdays  `gorm:"type:smallint[];not null"`
	StartMin         int       `gorm:"not null"`
	EndMin           int       `gorm:"not null"`
	ValidFrom        Date      `gorm:"type:date;not null"`
	ValidTo          *Date     `gorm:"type:date"`
	Source           string    `gorm:"not null"`
	CalendarSourceID *uint     `gorm:"column:calendar_source_id"`
	ExternalKey      []byte    `gorm:"column:external_key"` // SHA-256(UID | group); never the raw UID
	CreatedAt        time.Time `gorm:"autoCreateTime"`
	UpdatedAt        time.Time `gorm:"autoUpdateTime"`
}

// Overnight reports whether the commitment crosses midnight.
func (c Commitment) Overnight() bool { return c.EndMin < c.StartMin }

// MarshalJSON writes the contract shape (discipline/contracts/commitments.schema.json).
func (c Commitment) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID               uint     `json:"id"`
		Name             string   `json:"name"`
		Kind             string   `json:"kind"`
		Weekdays         Weekdays `json:"weekdays"`
		StartMin         int      `json:"startMin"`
		EndMin           int      `json:"endMin"`
		ValidFrom        Date     `json:"validFrom"`
		ValidTo          *Date    `json:"validTo"`
		Source           string   `json:"source"`
		CalendarSourceID *uint    `json:"calendarSourceId"`
		Overnight        bool     `json:"overnight"`
	}{c.ID, c.Name, c.Kind, c.Weekdays, c.StartMin, c.EndMin, c.ValidFrom, c.ValidTo, c.Source, c.CalendarSourceID, c.Overnight()})
}
