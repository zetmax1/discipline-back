package services

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/events"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrInvalidCheckin is wrapped by every morning check-in validation failure.
var ErrInvalidCheckin = errors.New("invalid check-in")

// Sleep times offered to a user without any check-in yet (23:00-07:00).
const (
	defaultSleepStartMin = 23 * 60
	defaultSleepEndMin   = 7 * 60
	maxCheckinNoteChars  = 500
)

// CheckinInput is a PUT /checkins/today body.
type CheckinInput struct {
	Skipped                    bool
	SleepStartMin, SleepEndMin *int
	SleepQuality, Energy       *int
	Note                       *string
}

// CheckinToday is today's check-in (nil if none) and the sleep times to prefill.
type CheckinToday struct {
	Date                       models.Date
	Checkin                    *models.Checkin
	SleepStartMin, SleepEndMin int
}

// DailyService is the daily loop (F1). `today` is the user's local date and `loc` their zone.
type DailyService interface {
	GetCheckin(userID uint, today models.Date, loc *time.Location) (*CheckinToday, error)
	SaveCheckin(userID uint, today models.Date, loc *time.Location, in CheckinInput) (*models.Checkin, error)

	// SaveSession creates or updates session id; replays of the same body change nothing.
	SaveSession(userID uint, id string, now time.Time, in SessionInput) (*models.FocusSession, error)
	ActiveSession(userID uint, now time.Time) (*models.FocusSession, error)
	// SessionsOn lists the sessions started on local day `date` in loc.
	SessionsOn(userID uint, date string, loc *time.Location, now time.Time) ([]models.FocusSession, error)
	ParkNote(userID uint, id string, text string, sessionID *string) error
	ParkingNotes(userID uint) ([]models.ParkingNote, error)

	GetShutdown(userID uint, today models.Date) (*models.Shutdown, error)
	CloseDay(userID uint, today models.Date, now time.Time, win string) (*models.Shutdown, error)
}

// ErrInvalidShutdown is wrapped by shutdown validation failures.
var ErrInvalidShutdown = errors.New("invalid shutdown")

const maxWinChars = 280

// Session validation errors.
var (
	ErrInvalidSession  = errors.New("invalid session")
	ErrSessionConflict = apierr.New(apierr.SessionActive, "another focus session is running")
	ErrInvalidNote     = errors.New("invalid note")
)

// SessionInput is a PUT /sessions/:id body. Updates are absolute (distractions = the count so
// far), so an offline client can resend its queue.
type SessionInput struct {
	BlockID        *uint
	StartedAt      *time.Time // only for actions queued offline; otherwise the server's now
	PlannedMinutes int
	BreakMinutes   int
	Distractions   int
	EndedAt        *time.Time
	Outcome        *string
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// An offline start may be at most this old when it syncs; a start "in the future" is clock skew.
const (
	maxOfflineAge  = 24 * time.Hour
	maxClockAhead  = time.Minute
	maxParkingNote = 280
)

func invalidSession(msg string) error {
	return apierr.Wrap(ErrInvalidSession, apierr.SessionInvalid, msg)
}

type dailyService struct {
	repo   repositories.DailyRepository
	events events.Recorder
}

// NewDailyService records product events with rec (events.Noop{} for none).
func NewDailyService(repo repositories.DailyRepository, rec events.Recorder) DailyService {
	return &dailyService{repo: repo, events: rec}
}

func invalidCheckin(msg string) error {
	return apierr.Wrap(ErrInvalidCheckin, apierr.CheckinInvalid, msg)
}

// SleepMinutes is the real time between sleep start and end for the check-in of day `date` in loc:
// a start after the end began on the evening before. Counted in real time, so a DST night is
// an hour shorter or longer than the wall clock suggests.
func SleepMinutes(date models.Date, startMin, endMin int, loc *time.Location) (int, bool) {
	day, err := time.ParseInLocation("2006-01-02", string(date), loc)
	if err != nil {
		return 0, false
	}
	wake := wallClock(day, endMin, loc)
	startDay := day
	if startMin > endMin {
		startDay = day.AddDate(0, 0, -1)
	}
	sleep := wallClock(startDay, startMin, loc)
	return int(wake.Sub(sleep).Minutes()), true
}

func wallClock(day time.Time, minutes int, loc *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), minutes/60, minutes%60, 0, 0, loc)
}

func withSleepMinutes(c *models.Checkin, loc *time.Location) *models.Checkin {
	if c != nil && c.SleepStartMin != nil && c.SleepEndMin != nil {
		if m, ok := SleepMinutes(c.Date, *c.SleepStartMin, *c.SleepEndMin, loc); ok {
			c.SleepMinutes = &m
		}
	}
	return c
}

func (s *dailyService) GetCheckin(userID uint, today models.Date, loc *time.Location) (*CheckinToday, error) {
	c, err := s.repo.FindCheckin(userID, today)
	if err != nil {
		return nil, err
	}
	out := &CheckinToday{Date: today, Checkin: withSleepMinutes(c, loc),
		SleepStartMin: defaultSleepStartMin, SleepEndMin: defaultSleepEndMin}
	last, err := s.repo.LastSleepTimes(userID)
	if err != nil {
		return nil, err
	}
	if last != nil && last.SleepStartMin != nil && last.SleepEndMin != nil {
		out.SleepStartMin, out.SleepEndMin = *last.SleepStartMin, *last.SleepEndMin
	}
	return out, nil
}

func inRange(v *int, lo, hi int) bool { return v != nil && *v >= lo && *v <= hi }

func (s *dailyService) SaveCheckin(userID uint, today models.Date, loc *time.Location, in CheckinInput) (*models.Checkin, error) {
	earlier, err := s.repo.FindCheckin(userID, today)
	if err != nil {
		return nil, err
	}
	c := &models.Checkin{UserID: userID, Date: today, Skipped: in.Skipped}
	if !in.Skipped {
		switch {
		case !inRange(in.SleepStartMin, 0, 1439) || !inRange(in.SleepEndMin, 0, 1440):
			return nil, invalidCheckin("sleepStartMin (0-1439) and sleepEndMin (0-1440) are required")
		case *in.SleepStartMin == *in.SleepEndMin:
			return nil, invalidCheckin("sleep start and end must differ")
		case !inRange(in.SleepQuality, 1, 5) || !inRange(in.Energy, 1, 5):
			return nil, invalidCheckin("sleepQuality and energy must be between 1 and 5")
		}
		c.SleepStartMin, c.SleepEndMin, c.SleepQuality, c.Energy = in.SleepStartMin, in.SleepEndMin, in.SleepQuality, in.Energy
		if in.Note != nil {
			note := strings.TrimSpace(*in.Note)
			if utf8.RuneCountInString(note) > maxCheckinNoteChars {
				return nil, invalidCheckin("note must be at most 500 characters")
			}
			if note != "" {
				c.Note = &note
			}
		}
	}
	if err := s.repo.UpsertCheckin(c); err != nil {
		return nil, err
	}
	if earlier == nil { // the first save of the day; edits and replays are not new check-ins
		s.events.Record(userID, events.CheckinMorningDone, events.Props{"skipped": c.Skipped})
	}
	return withSleepMinutes(c, loc), nil
}

func validateSession(id string, in SessionInput) error {
	switch {
	case !uuidPattern.MatchString(id):
		return invalidSession("the session id must be a UUID")
	case in.PlannedMinutes < 10 || in.PlannedMinutes > 180:
		return invalidSession("plannedMinutes must be 10-180")
	case in.BreakMinutes < 0 || in.BreakMinutes > 60:
		return invalidSession("breakMinutes must be 0-60")
	case in.Distractions < 0 || in.Distractions > 500:
		return invalidSession("distractions must be 0-500")
	case (in.EndedAt == nil) != (in.Outcome == nil):
		return invalidSession("endedAt and outcome go together")
	case in.Outcome != nil && *in.Outcome != models.SessionCompleted && *in.Outcome != models.SessionAbandoned:
		return invalidSession("outcome must be completed or abandoned")
	}
	return nil
}

func (s *dailyService) SaveSession(userID uint, id string, now time.Time, in SessionInput) (*models.FocusSession, error) {
	if err := validateSession(id, in); err != nil {
		return nil, err
	}
	id = strings.ToLower(id)
	if in.BlockID != nil {
		ok, err := s.repo.BlockBelongsTo(userID, *in.BlockID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, invalidSession("blockId is not one of your blocks")
		}
	}
	var started, ended bool
	var newDistractions int
	saved, err := s.repo.SaveSession(userID, id, now, func(existing, other *models.FocusSession) (*models.FocusSession, bool, error) {
		if existing != nil {
			if existing.EndedAt != nil { // finished sessions don't change; replays are answered as they are
				return existing, false, nil
			}
			newDistractions = max(0, in.Distractions-existing.Distractions)
			ended = in.EndedAt != nil
			existing.Distractions = in.Distractions
			if in.EndedAt != nil {
				if in.EndedAt.Before(existing.StartedAt) {
					return nil, false, invalidSession("endedAt must not be before the start")
				}
				existing.EndedAt, existing.Outcome = in.EndedAt, in.Outcome
			}
			return existing, false, nil
		}
		if other != nil && in.EndedAt == nil {
			return nil, false, ErrSessionConflict
		}
		startAt := now
		if in.StartedAt != nil && !in.StartedAt.After(now.Add(maxClockAhead)) && in.StartedAt.After(now.Add(-maxOfflineAge)) {
			startAt = *in.StartedAt
		}
		if in.EndedAt != nil && in.EndedAt.Before(startAt) {
			return nil, false, invalidSession("endedAt must not be before the start")
		}
		started, ended, newDistractions = true, in.EndedAt != nil, in.Distractions
		return &models.FocusSession{
			ID: id, UserID: userID, BlockID: in.BlockID, StartedAt: startAt, PlannedMinutes: in.PlannedMinutes,
			BreakMinutes: in.BreakMinutes, Distractions: in.Distractions, EndedAt: in.EndedAt, Outcome: in.Outcome,
		}, true, nil
	})
	if err != nil {
		return nil, err
	}
	facts := events.Props{"planned_minutes": saved.PlannedMinutes, "in_block": saved.BlockID != nil}
	if started {
		s.events.Record(userID, events.SessionStarted, facts)
	}
	if newDistractions > 0 {
		s.events.Record(userID, events.DistractionLogged, events.Props{"count": newDistractions})
	}
	if ended {
		facts["distractions"] = saved.Distractions
		name := events.SessionCompleted
		if saved.Outcome != nil && *saved.Outcome == models.SessionAbandoned {
			name = events.SessionAbandoned
		}
		s.events.Record(userID, name, facts)
	}
	return saved, nil
}

func (s *dailyService) ActiveSession(userID uint, now time.Time) (*models.FocusSession, error) {
	return s.repo.ActiveSession(userID, now)
}

func (s *dailyService) SessionsOn(userID uint, date string, loc *time.Location, now time.Time) ([]models.FocusSession, error) {
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, invalidSession("date must be YYYY-MM-DD")
	}
	return s.repo.SessionsStartedBetween(userID, day, day.AddDate(0, 0, 1), now)
}

func (s *dailyService) ParkNote(userID uint, id string, text string, sessionID *string) error {
	text = strings.TrimSpace(text)
	if !uuidPattern.MatchString(id) || text == "" || utf8.RuneCountInString(text) > maxParkingNote ||
		(sessionID != nil && !uuidPattern.MatchString(*sessionID)) {
		return apierr.Wrap(ErrInvalidNote, apierr.ParkingNoteInvalid, "a note needs a UUID id and 1-280 characters")
	}
	return s.repo.AddParkingNote(&models.ParkingNote{ID: strings.ToLower(id), UserID: userID, SessionID: sessionID, Text: text})
}

func (s *dailyService) ParkingNotes(userID uint) ([]models.ParkingNote, error) {
	return s.repo.ParkingNotes(userID)
}

func (s *dailyService) GetShutdown(userID uint, today models.Date) (*models.Shutdown, error) {
	return s.repo.FindShutdown(userID, today)
}

func (s *dailyService) CloseDay(userID uint, today models.Date, now time.Time, win string) (*models.Shutdown, error) {
	win = strings.TrimSpace(win)
	if utf8.RuneCountInString(win) > maxWinChars {
		return nil, apierr.Wrap(ErrInvalidShutdown, apierr.ShutdownInvalid, "win must be at most 280 characters")
	}
	earlier, err := s.repo.FindShutdown(userID, today)
	if err != nil {
		return nil, err
	}
	sd := &models.Shutdown{UserID: userID, Date: today, ClosedAt: now}
	if win != "" {
		sd.Win = &win
	}
	closed, err := s.repo.CloseDay(sd)
	if err == nil && earlier == nil {
		s.events.Record(userID, events.ShutdownDone, events.Props{"has_win": win != ""})
	}
	return closed, err
}
