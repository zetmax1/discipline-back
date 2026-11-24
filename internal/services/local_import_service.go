package services

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"discipline-backend/internal/events"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// LegacyCommitment is the browser's pre-F9a Smart Schedule commitment (localStorage).
type LegacyCommitment struct {
	Name      string   `json:"name"`
	StartTime string   `json:"startTime"`
	EndTime   string   `json:"endTime"`
	Days      []string `json:"days"`
}

// LegacyGoals are the browser's weekly hours.
type LegacyGoals struct {
	DeepWorkHours float64 `json:"deepWorkHours"`
	StudyHours    float64 `json:"studyHours"`
	RestHours     float64 `json:"restHours"`
}

// LocalImportResult is the contract shape (commitments.schema.json local_import_result).
type LocalImportResult struct {
	Imported       int  `json:"imported"`
	Duplicates     int  `json:"duplicates"`
	Invalid        int  `json:"invalid"`
	TargetsApplied bool `json:"targetsApplied"`
}

var dayNumber = map[string]int{"monday": 1, "tuesday": 2, "wednesday": 3, "thursday": 4, "friday": 5, "saturday": 6, "sunday": 7}

// exampleKinds: the page's example names in uz, ru and en map to a kind; anything else is personal.
// A fixed lookup, no guessing (F9a S2).
var exampleKinds = map[string]string{
	"uyqu": "rest", "сон": "rest", "sleep": "rest",
	"universitet": "study", "университет": "study", "university": "study",
	"sport zali": "exercise", "спортзал": "exercise", "gym": "exercise",
}

var hhmm = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// clock reads "HH:MM"; "24:00" is 1440. ok is false for anything else.
func clock(s string) (int, bool) {
	m := hhmm.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	if mm > 59 || h > 24 || (h == 24 && mm != 0) {
		return 0, false
	}
	return h*60 + mm, true
}

// MapLegacy turns a browser commitment into an input valid from `today`, or false when it can't be.
func MapLegacy(l LegacyCommitment, today string) (CommitmentInput, bool) {
	start, ok1 := clock(l.StartTime)
	end, ok2 := clock(l.EndTime)
	if !ok1 || !ok2 {
		return CommitmentInput{}, false
	}
	if end == 0 { // an end of 00:00 means midnight
		end = 1440
	}
	var days []int
	for _, d := range l.Days {
		n, ok := dayNumber[strings.ToLower(strings.TrimSpace(d))]
		if !ok {
			return CommitmentInput{}, false
		}
		days = append(days, n)
	}
	kind, ok := exampleKinds[strings.ToLower(strings.TrimSpace(l.Name))]
	if !ok {
		kind = "personal"
	}
	return CommitmentInput{Name: l.Name, Kind: kind, Weekdays: days, StartMin: start, EndMin: end, ValidFrom: today}, true
}

func hoursToMinutes(h float64) int {
	m := int(math.Round(h*60/15)) * 15
	return max(0, min(4800, m))
}

type LocalImportService interface {
	Import(userID uint, list []LegacyCommitment, goals *LegacyGoals, today string) (LocalImportResult, error)
}

type localImportService struct {
	commitments repositories.CommitmentRepository
	targets     repositories.TargetRepository
	events      events.Recorder
}

func NewLocalImportService(c repositories.CommitmentRepository, t repositories.TargetRepository, rec events.Recorder) LocalImportService {
	return &localImportService{commitments: c, targets: t, events: rec}
}

func sameSlot(a, b models.Commitment) bool {
	if a.Name != b.Name || a.StartMin != b.StartMin || a.EndMin != b.EndMin || len(a.Weekdays) != len(b.Weekdays) {
		return false
	}
	for i := range a.Weekdays {
		if a.Weekdays[i] != b.Weekdays[i] {
			return false
		}
	}
	return true
}

// Import saves the browser's commitments once: exact duplicates (same name, weekdays, start, end)
// are skipped, so a second device changes nothing; goals become targets only while none are saved.
func (s *localImportService) Import(userID uint, list []LegacyCommitment, goals *LegacyGoals, today string) (LocalImportResult, error) {
	var res LocalImportResult
	existing, err := s.commitments.List(userID)
	if err != nil {
		return res, err
	}
	for _, l := range list {
		in, ok := MapLegacy(l, today)
		if !ok {
			res.Invalid++
			continue
		}
		c, err := NormalizeCommitment(in)
		if err != nil {
			res.Invalid++
			continue
		}
		dup := false
		for _, e := range existing {
			if sameSlot(e, c) {
				dup = true
				break
			}
		}
		if dup {
			res.Duplicates++
			continue
		}
		c.UserID, c.Source = userID, models.CommitmentLocalImport
		if _, err := s.commitments.Create(&c, ""); err != nil {
			if errors.Is(err, repositories.ErrCommitmentLimit) {
				res.Invalid++
				continue
			}
			return res, err
		}
		existing = append(existing, c)
		res.Imported++
		s.events.Record(userID, events.CommitmentCreated, CreatedProps(c))
	}
	if goals != nil {
		sys, err := s.targets.SystemCategories(userID)
		if err != nil {
			return res, err
		}
		t := repositories.Targets{Categories: []repositories.CategoryMinutes{}}
		for key, h := range map[string]float64{"deep_work": goals.DeepWorkHours, "study": goals.StudyHours, "rest": goals.RestHours} {
			if id, ok := sys[key]; ok {
				t.Categories = append(t.Categories, repositories.CategoryMinutes{CategoryID: id, MinutesPerWeek: hoursToMinutes(h)})
			}
		}
		if res.TargetsApplied, err = s.targets.ApplyIfNone(userID, t); err != nil {
			return res, err
		}
	}
	return res, nil
}
