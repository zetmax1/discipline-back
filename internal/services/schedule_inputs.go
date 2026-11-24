package services

import (
	"errors"
	"time"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/commitments"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrWeekStart: weekStart is not a Monday date.
var ErrWeekStart = errors.New("weekStart must be a Monday")

// BusySegment is one commitment segment of the chosen week, by English day name (the generator's).
type BusySegment struct {
	Day      string `json:"day"`
	StartMin int    `json:"startMin"`
	EndMin   int    `json:"endMin"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// ScheduleGoals are weekly hours by kind (focus → deepWorkHours).
type ScheduleGoals struct {
	DeepWorkHours float64 `json:"deepWorkHours"`
	StudyHours    float64 `json:"studyHours"`
	RestHours     float64 `json:"restHours"`
}

// SmartScheduleV2 is what the API sends the analytics service with F9A
// (analytics.schema.json smart_schedule_request_v2).
type SmartScheduleV2 struct {
	WeekStart         string        `json:"weekStart"`
	Busy              []BusySegment `json:"busy"`
	Goals             ScheduleGoals `json:"goals"`
	RestDay           *int          `json:"restDay"`
	MaxFocusMinPerDay *int          `json:"maxFocusMinPerDay"`
}

// ScheduleInputs builds the generator's input from the user's saved commitments and targets.
type ScheduleInputs interface {
	Build(userID uint, weekStart string) (*SmartScheduleV2, error)
}

type scheduleInputs struct {
	commitments repositories.CommitmentRepository
	targetRepo  repositories.TargetRepository
	targets     TargetService
}

func NewScheduleInputs(c repositories.CommitmentRepository, tr repositories.TargetRepository, t TargetService) ScheduleInputs {
	return &scheduleInputs{commitments: c, targetRepo: tr, targets: t}
}

var dayNames = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func (s *scheduleInputs) Build(userID uint, weekStart string) (*SmartScheduleV2, error) {
	ws, err := time.Parse("2006-01-02", weekStart)
	if err != nil || ws.Format("2006-01-02") != weekStart || ws.Weekday() != time.Monday {
		return nil, apierr.Wrap(ErrWeekStart, apierr.WeekStartInvalid, "weekStart must be a Monday (YYYY-MM-DD)")
	}
	end := ws.AddDate(0, 0, 6).Format("2006-01-02")
	list, err := s.commitments.InRange(userID, models.Date(weekStart), models.Date(end))
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]models.Commitment, len(list))
	for _, c := range list {
		byID[c.ID] = c
	}
	out := &SmartScheduleV2{WeekStart: weekStart, Busy: []BusySegment{}}
	for _, seg := range commitments.Expand(ToExpand(list), weekStart, end) {
		d, _ := time.Parse("2006-01-02", seg.Date)
		c := byID[seg.CommitmentID]
		out.Busy = append(out.Busy, BusySegment{Day: dayNames[models.ISOWeekday(d)-1], StartMin: seg.StartMin, EndMin: seg.EndMin, Name: c.Name, Kind: c.Kind})
	}
	t, err := s.targets.Get(userID)
	if err != nil {
		return nil, err
	}
	kinds, err := s.targetRepo.CategoryKinds(userID)
	if err != nil {
		return nil, err
	}
	for _, c := range t.Categories {
		h := float64(c.MinutesPerWeek) / 60
		switch kinds[c.CategoryID] {
		case "focus":
			out.Goals.DeepWorkHours += h
		case "study":
			out.Goals.StudyHours += h
		case "rest":
			out.Goals.RestHours += h
		}
	}
	out.RestDay, out.MaxFocusMinPerDay = t.RestDay, t.MaxFocusMinPerDay
	return out, nil
}
