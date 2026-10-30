package repositories

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"discipline-backend/internal/models"
)

// DailyRepository stores the daily loop: check-ins, focus sessions, parking lot, shutdowns (F1).
type DailyRepository interface {
	// FindCheckin returns the check-in for date, or nil.
	FindCheckin(userID uint, date models.Date) (*models.Checkin, error)
	// LastSleepTimes returns the sleep times of the most recent non-skipped check-in, or nil.
	LastSleepTimes(userID uint) (*models.Checkin, error)
	// UpsertCheckin creates or replaces the check-in for its date.
	UpsertCheckin(c *models.Checkin) error

	// SaveSession runs decide with the session `id` (nil if new) and the user's other running
	// session (nil if none), after ending forgotten sessions, under the user's lock; then stores
	// what decide returns.
	SaveSession(userID uint, id string, now time.Time, decide SessionDecision) (*models.FocusSession, error)
	// ActiveSession ends forgotten sessions, then returns the running one (or nil).
	ActiveSession(userID uint, now time.Time) (*models.FocusSession, error)
	// SessionsStartedBetween lists sessions with from <= started_at < to, oldest first.
	SessionsStartedBetween(userID uint, from, to, now time.Time) ([]models.FocusSession, error)
	// BlockBelongsTo reports whether block id is the user's.
	BlockBelongsTo(userID, blockID uint) (bool, error)
	// AddParkingNote stores a note once (replays with the same id are ignored).
	AddParkingNote(n *models.ParkingNote) error
	ParkingNotes(userID uint) ([]models.ParkingNote, error)
}

// SessionDecision builds the row to store; create says whether it is new.
type SessionDecision func(existing, otherActive *models.FocusSession) (session *models.FocusSession, create bool, err error)

// staleGraceMinutes: a session still running this long after its planned end was forgotten.
const staleGraceMinutes = 60

type dailyRepository struct {
	db *gorm.DB
}

func NewDailyRepository(db *gorm.DB) DailyRepository {
	return &dailyRepository{db: db}
}

func (r *dailyRepository) FindCheckin(userID uint, date models.Date) (*models.Checkin, error) {
	var c models.Checkin
	err := r.db.Where("user_id = ? AND date = ?", userID, date).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *dailyRepository) LastSleepTimes(userID uint) (*models.Checkin, error) {
	var c models.Checkin
	err := r.db.Where("user_id = ? AND NOT skipped", userID).Order("date DESC").First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *dailyRepository) UpsertCheckin(c *models.Checkin) error {
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "date"}},
		DoUpdates: clause.AssignmentColumns([]string{"skipped", "sleep_start_min", "sleep_end_min",
			"sleep_quality", "energy", "note", "updated_at"}),
	}).Create(c).Error
}

// abandonStale ends sessions left running past their planned end + the grace period, as
// abandoned at their planned end.
func abandonStale(tx *gorm.DB, userID uint, now time.Time) error {
	return tx.Exec(`UPDATE focus_sessions
		SET ended_at = started_at + planned_minutes * interval '1 minute', outcome = ?, updated_at = now()
		WHERE user_id = ? AND ended_at IS NULL
		  AND started_at + (planned_minutes + ?) * interval '1 minute' < ?`,
		models.SessionAbandoned, userID, staleGraceMinutes, now).Error
}

func findSession(tx *gorm.DB, query string, args ...any) (*models.FocusSession, error) {
	var s models.FocusSession
	err := tx.Where(query, args...).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *dailyRepository) SaveSession(userID uint, id string, now time.Time, decide SessionDecision) (*models.FocusSession, error) {
	var out *models.FocusSession
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTasks(tx, userID); err != nil {
			return err
		}
		if err := abandonStale(tx, userID, now); err != nil {
			return err
		}
		existing, err := findSession(tx, "id = ? AND user_id = ?", id, userID)
		if err != nil {
			return err
		}
		other, err := findSession(tx, "user_id = ? AND ended_at IS NULL AND id <> ?", userID, id)
		if err != nil {
			return err
		}
		s, create, err := decide(existing, other)
		if err != nil {
			return err
		}
		if create {
			err = tx.Create(s).Error
		} else {
			err = tx.Save(s).Error
		}
		out = s
		return err
	})
	return out, err
}

func (r *dailyRepository) ActiveSession(userID uint, now time.Time) (*models.FocusSession, error) {
	if err := abandonStale(r.db, userID, now); err != nil {
		return nil, err
	}
	return findSession(r.db, "user_id = ? AND ended_at IS NULL", userID)
}

func (r *dailyRepository) SessionsStartedBetween(userID uint, from, to, now time.Time) ([]models.FocusSession, error) {
	if err := abandonStale(r.db, userID, now); err != nil {
		return nil, err
	}
	var out []models.FocusSession
	err := r.db.Where("user_id = ? AND started_at >= ? AND started_at < ?", userID, from, to).
		Order("started_at").Find(&out).Error
	return out, err
}

func (r *dailyRepository) BlockBelongsTo(userID, blockID uint) (bool, error) {
	var n int64
	err := r.db.Model(&models.Task{}).Where("id = ? AND user_id = ?", blockID, userID).Count(&n).Error
	return n > 0, err
}

func (r *dailyRepository) AddParkingNote(n *models.ParkingNote) error {
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(n).Error
}

func (r *dailyRepository) ParkingNotes(userID uint) ([]models.ParkingNote, error) {
	var out []models.ParkingNote
	err := r.db.Where("user_id = ?", userID).Order("created_at").Find(&out).Error
	return out, err
}
