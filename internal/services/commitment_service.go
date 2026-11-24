package services

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/commitments"
	"discipline-backend/internal/events"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrInvalidCommitment is wrapped by every commitment validation failure (the code says which).
var ErrInvalidCommitment = errors.New("invalid commitment")

func invalidCommitment(code apierr.Code, msg string) error {
	return apierr.Wrap(ErrInvalidCommitment, code, msg)
}

// CommitmentKinds are the category kinds (F13); commitments use the kind, not a category.
var CommitmentKinds = []string{"focus", "study", "admin", "exercise", "rest", "personal"}

// CommitmentInput is a commitment as a client sends it (POST, PUT, local import, calendar commit).
type CommitmentInput struct {
	Name      string
	Kind      string
	Weekdays  []int
	StartMin  int
	EndMin    int
	ValidFrom string
	ValidTo   *string
}

func validDate(s string) bool {
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Format("2006-01-02") == s
}

// NormalizeCommitment validates in and returns the stored fields (weekdays sorted, unique).
func NormalizeCommitment(in CommitmentInput) (models.Commitment, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return models.Commitment{}, invalidCommitment(apierr.ValidationFailed, "name must be 1-100 characters")
	}
	if !slices.Contains(CommitmentKinds, in.Kind) {
		return models.Commitment{}, invalidCommitment(apierr.CommitmentKindInvalid, "kind must be one of the category kinds")
	}
	days := slices.Clone(in.Weekdays)
	slices.Sort(days)
	days = slices.Compact(days)
	if len(days) == 0 || days[0] < 1 || days[len(days)-1] > 7 {
		return models.Commitment{}, invalidCommitment(apierr.CommitmentWeekdaysInvalid, "weekdays must be ISO days 1-7, at least one")
	}
	if in.StartMin < 0 || in.StartMin > 1439 || in.EndMin < 1 || in.EndMin > 1440 || in.StartMin == in.EndMin {
		return models.Commitment{}, invalidCommitment(apierr.CommitmentTimesInvalid, "startMin 0-1439, endMin 1-1440, and they differ")
	}
	if !validDate(in.ValidFrom) || (in.ValidTo != nil && (!validDate(*in.ValidTo) || *in.ValidTo < in.ValidFrom)) {
		return models.Commitment{}, invalidCommitment(apierr.CommitmentDatesInvalid, "validFrom/validTo must be dates, validTo not before validFrom")
	}
	c := models.Commitment{Name: name, Kind: in.Kind, Weekdays: days, StartMin: in.StartMin, EndMin: in.EndMin, ValidFrom: models.Date(in.ValidFrom)}
	if in.ValidTo != nil {
		to := models.Date(*in.ValidTo)
		c.ValidTo = &to
	}
	return c, nil
}

// CreatedProps are commitment_created's props: facts only, never the name.
func CreatedProps(c models.Commitment) events.Props {
	return events.Props{"source": c.Source, "kind": c.Kind, "overnight": c.Overnight(), "weekday_count": len(c.Weekdays)}
}

// MaxRangeDaysCommitments bounds GET /commitments/occurrences (as F13's range).
const MaxRangeDaysCommitments = 42

type CommitmentService interface {
	List(userID uint) ([]models.Commitment, error)
	Create(userID uint, in CommitmentInput, key string) (*models.Commitment, error)
	Update(id, userID uint, in CommitmentInput) (*models.Commitment, error)
	Delete(id, userID uint) error
	Occurrences(userID uint, start, end string) ([]commitments.Segment, error)
}

type commitmentService struct {
	repo   repositories.CommitmentRepository
	events events.Recorder
}

func NewCommitmentService(repo repositories.CommitmentRepository, rec events.Recorder) CommitmentService {
	return &commitmentService{repo: repo, events: rec}
}

func (s *commitmentService) List(userID uint) ([]models.Commitment, error) {
	return s.repo.List(userID)
}

func (s *commitmentService) Create(userID uint, in CommitmentInput, key string) (*models.Commitment, error) {
	c, err := NormalizeCommitment(in)
	if err != nil {
		return nil, err
	}
	c.UserID, c.Source = userID, models.CommitmentManual
	created, err := s.repo.Create(&c, key)
	if err != nil {
		return nil, err
	}
	if created {
		s.events.Record(userID, events.CommitmentCreated, CreatedProps(c))
	}
	return &c, nil
}

func (s *commitmentService) Update(id, userID uint, in CommitmentInput) (*models.Commitment, error) {
	n, err := NormalizeCommitment(in)
	if err != nil {
		return nil, err
	}
	return s.repo.Update(id, userID, func(c *models.Commitment) {
		c.Name, c.Kind, c.Weekdays, c.StartMin, c.EndMin, c.ValidFrom, c.ValidTo = n.Name, n.Kind, n.Weekdays, n.StartMin, n.EndMin, n.ValidFrom, n.ValidTo
	})
}

func (s *commitmentService) Delete(id, userID uint) error { return s.repo.Delete(id, userID) }

// ToExpand converts stored commitments for the expander.
func ToExpand(list []models.Commitment) []commitments.Commitment {
	out := make([]commitments.Commitment, len(list))
	for i, c := range list {
		var to *string
		if c.ValidTo != nil {
			s := string(*c.ValidTo)
			to = &s
		}
		out[i] = commitments.Commitment{ID: c.ID, Weekdays: c.Weekdays, StartMin: c.StartMin, EndMin: c.EndMin, ValidFrom: string(c.ValidFrom), ValidTo: to}
	}
	return out
}

func (s *commitmentService) Occurrences(userID uint, start, end string) ([]commitments.Segment, error) {
	ts, err1 := time.Parse("2006-01-02", start)
	te, err2 := time.Parse("2006-01-02", end)
	if err1 != nil || err2 != nil || ts.Format("2006-01-02") != start || te.Format("2006-01-02") != end || te.Before(ts) {
		return nil, invalidCommitment(apierr.DateInvalid, "startDate and endDate must be YYYY-MM-DD dates, startDate first")
	}
	if int(te.Sub(ts).Hours()/24)+1 > MaxRangeDaysCommitments {
		return nil, invalidCommitment(apierr.RangeTooLong, "a range covers at most 42 days")
	}
	list, err := s.repo.InRange(userID, models.Date(start), models.Date(end))
	if err != nil {
		return nil, err
	}
	return commitments.Expand(ToExpand(list), start, end), nil
}
