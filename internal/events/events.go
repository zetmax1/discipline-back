// Package events writes product events to the internal product_events table (append-only).
// Names are listed in discipline/contracts/events.schema.json (a test keeps both lists equal).
// Events are never sent anywhere else; props carry facts about the action, never personal data.
package events

import (
	"context"
	"encoding/json"
	"log/slog"

	"gorm.io/gorm"
)

// Name is a product event name.
type Name string

const (
	BlockCreated       Name = "block_created"
	BlockStatusChanged Name = "block_status_changed"
	BlockMoved         Name = "block_moved"
	BlockCheckedIn     Name = "block_checked_in"
	CheckinMorningDone Name = "checkin_morning_done"
	SessionStarted     Name = "session_started"
	SessionCompleted   Name = "session_completed"
	SessionAbandoned   Name = "session_abandoned"
	DistractionLogged  Name = "distraction_logged"
	ShutdownDone       Name = "shutdown_done"
	WeekPlanned        Name = "week_planned"       // F13: a range was materialized and created occurrences
	CommitmentCreated  Name = "commitment_created" // F9a: any source (manual, local import, calendar)
	CalendarImported   Name = "calendar_imported"  // F9a: a calendar commit created or updated at least one
)

// All lists every event the API writes.
var All = []Name{
	BlockCreated, BlockStatusChanged, BlockMoved, BlockCheckedIn, CheckinMorningDone,
	SessionStarted, SessionCompleted, SessionAbandoned, DistractionLogged, ShutdownDone, WeekPlanned,
	CommitmentCreated, CalendarImported,
}

// Props are facts about the action (counts, minutes, statuses).
type Props map[string]any

// Recorder writes events. Recording never fails the request: errors are logged.
type Recorder interface {
	Record(userID uint, name Name, props Props)
}

// Noop records nothing (tests and code paths without a database).
type Noop struct{}

func (Noop) Record(uint, Name, Props) {}

type dbRecorder struct {
	db *gorm.DB
}

// NewRecorder writes events to product_events.
func NewRecorder(db *gorm.DB) Recorder {
	return dbRecorder{db: db}
}

func (r dbRecorder) Record(userID uint, name Name, props Props) {
	if props == nil {
		props = Props{}
	}
	raw, err := json.Marshal(props)
	if err == nil {
		err = r.db.Exec(`INSERT INTO product_events (user_id, name, props) VALUES (?, ?, ?::jsonb)`,
			userID, string(name), string(raw)).Error
	}
	if err != nil {
		slog.ErrorContext(context.Background(), "product event not recorded", "event", string(name), "error", err)
	}
}
