package services

import (
	"errors"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/repositories"
)

// ErrInvalidTargets wraps every weekly-target validation failure.
var ErrInvalidTargets = errors.New("invalid weekly targets")

// DefaultTargets are the Smart Schedule page's defaults (F9a S3): Deep Work 15 h, Study 10 h, Rest 7 h.
var DefaultTargets = map[string]int{"deep_work": 900, "study": 600, "rest": 420}

// WeeklyTargets is the contract shape (commitments.schema.json weekly_targets).
type WeeklyTargets struct {
	Categories        []repositories.CategoryMinutes `json:"categories"`
	MaxFocusMinPerDay *int                           `json:"maxFocusMinPerDay"`
	RestDay           *int                           `json:"restDay"`
	IsDefault         bool                           `json:"isDefault"`
}

type TargetService interface {
	Get(userID uint) (WeeklyTargets, error)
	Save(userID uint, in WeeklyTargets) (WeeklyTargets, error)
}

type targetService struct{ repo repositories.TargetRepository }

func NewTargetService(repo repositories.TargetRepository) TargetService {
	return &targetService{repo: repo}
}

// defaults returns the page defaults on the user's system categories; nothing is written.
func defaults(repo repositories.TargetRepository, userID uint) (WeeklyTargets, error) {
	sys, err := repo.SystemCategories(userID)
	if err != nil {
		return WeeklyTargets{}, err
	}
	out := WeeklyTargets{Categories: []repositories.CategoryMinutes{}, IsDefault: true}
	for _, key := range []string{"deep_work", "study", "rest"} {
		if id, ok := sys[key]; ok {
			out.Categories = append(out.Categories, repositories.CategoryMinutes{CategoryID: id, MinutesPerWeek: DefaultTargets[key]})
		}
	}
	return out, nil
}

func (s *targetService) Get(userID uint) (WeeklyTargets, error) {
	t, err := s.repo.Get(userID)
	if err != nil {
		return WeeklyTargets{}, err
	}
	if !t.Saved && len(t.Categories) == 0 {
		return defaults(s.repo, userID)
	}
	return WeeklyTargets{Categories: t.Categories, MaxFocusMinPerDay: t.MaxFocusMinPerDay, RestDay: t.RestDay}, nil
}

// ValidTargets checks minutes (0–4800, 15-minute steps, one row per category), the daily focus cap
// (null or 60–960 in 15-minute steps) and the rest day (null or ISO 1–7).
func ValidTargets(in WeeklyTargets) error {
	seen := map[uint]bool{}
	for _, c := range in.Categories {
		if c.MinutesPerWeek < 0 || c.MinutesPerWeek > 4800 || c.MinutesPerWeek%15 != 0 || seen[c.CategoryID] {
			return apierr.Wrap(ErrInvalidTargets, apierr.TargetsInvalid, "minutes per week are 0-4800 in 15-minute steps, one row per category")
		}
		seen[c.CategoryID] = true
	}
	if m := in.MaxFocusMinPerDay; m != nil && (*m < 60 || *m > 960 || *m%15 != 0) {
		return apierr.Wrap(ErrInvalidTargets, apierr.TargetsInvalid, "maxFocusMinPerDay is null or 60-960 in 15-minute steps")
	}
	if d := in.RestDay; d != nil && (*d < 1 || *d > 7) {
		return apierr.Wrap(ErrInvalidTargets, apierr.TargetsInvalid, "restDay is null or an ISO weekday 1-7")
	}
	return nil
}

func (s *targetService) Save(userID uint, in WeeklyTargets) (WeeklyTargets, error) {
	if in.Categories == nil {
		in.Categories = []repositories.CategoryMinutes{}
	}
	if err := ValidTargets(in); err != nil {
		return WeeklyTargets{}, err
	}
	if err := s.repo.Save(userID, repositories.Targets{Categories: in.Categories, MaxFocusMinPerDay: in.MaxFocusMinPerDay, RestDay: in.RestDay}); err != nil {
		return WeeklyTargets{}, err
	}
	return s.Get(userID)
}
