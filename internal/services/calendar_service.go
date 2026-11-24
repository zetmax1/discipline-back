package services

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/calendarcrypt"
	"discipline-backend/internal/calendarfetch"
	"discipline-backend/internal/events"
	"discipline-backend/internal/icsimport"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrImport wraps every calendar-import failure; its apierr code says which.
var ErrImport = errors.New("calendar import failed")

func importErr(code apierr.Code, msg string) error { return apierr.Wrap(ErrImport, code, msg) }

// Fetcher is calendarfetch.Fetcher (a fake in tests).
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, string, error)
}

// PreviewSource names the calendar a preview came from (never its URL).
type PreviewSource struct {
	ID   *uint  `json:"id"`
	Host string `json:"host"`
}

type PreviewCandidate struct {
	Key           string   `json:"key"`
	Name          string   `json:"name"`
	SuggestedKind string   `json:"suggestedKind"`
	Weekdays      []int    `json:"weekdays"`
	StartMin      int      `json:"startMin"`
	EndMin        int      `json:"endMin"`
	ValidFrom     string   `json:"validFrom"`
	ValidTo       *string  `json:"validTo"`
	Notes         []string `json:"notes"`
	Status        string   `json:"status"`
	CommitmentID  *uint    `json:"commitmentId"`
}

type PreviewOneOff struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Date     string `json:"date"`
	StartMin int    `json:"startMin"`
	EndMin   int    `json:"endMin"`
	Status   string `json:"status"`
}

type PreviewRemoved struct {
	CommitmentID uint   `json:"commitmentId"`
	Name         string `json:"name"`
}

// Preview is the contract shape (calendar-import.schema.json import_preview).
type Preview struct {
	Source     *PreviewSource     `json:"source"`
	Candidates []PreviewCandidate `json:"candidates"`
	OneOffs    []PreviewOneOff    `json:"oneOffs"`
	Removed    []PreviewRemoved   `json:"removed"`
	Skipped    icsimport.Skipped  `json:"skipped"`
}

// CommitItem is one ticked candidate or one-off, possibly renamed and with a chosen kind.
type CommitItem struct {
	Key string
	CommitmentInput
}

type CommitInput struct {
	URL       string
	SourceID  *uint
	Origin    string // file | url
	Items     []CommitItem
	RemoveIDs []uint
}

// CommitResult is the contract shape (import_result).
type CommitResult struct {
	Created  int   `json:"created"`
	Updated  int   `json:"updated"`
	Removed  int   `json:"removed"`
	SourceID *uint `json:"sourceId"`
}

type CalendarService interface {
	PreviewFile(ctx context.Context, userID uint, data []byte, loc *time.Location, today string) (*Preview, error)
	PreviewURL(ctx context.Context, userID uint, url string, loc *time.Location, today string) (*Preview, error)
	PreviewSource(ctx context.Context, userID, sourceID uint, loc *time.Location, today string) (*Preview, error)
	Commit(userID uint, in CommitInput, key string) (*CommitResult, error)
	Sources(userID uint) ([]repositories.SourceSummary, error)
	DeleteSource(userID, id uint, withCommitments bool) error
}

type calendarService struct {
	repo   repositories.CalendarRepository
	fetch  Fetcher
	box    *calendarcrypt.Box
	events events.Recorder
}

func NewCalendarService(repo repositories.CalendarRepository, fetch Fetcher, box *calendarcrypt.Box, rec events.Recorder) CalendarService {
	return &calendarService{repo: repo, fetch: fetch, box: box, events: rec}
}

// MaxICSFile is the largest uploaded file (the URL body limit is calendarfetch.MaxBody).
const MaxICSFile = 1 << 20

// parseBudget bounds the CPU a preview may use.
const parseBudget = 3 * time.Second

// FetchCode maps a fetcher error to its API code and the source's last_status.
func FetchCode(err error) (apierr.Code, string) {
	switch {
	case errors.Is(err, calendarfetch.ErrInvalid):
		return apierr.ICSURLInvalid, "invalid"
	case errors.Is(err, calendarfetch.ErrBlocked):
		return apierr.ICSURLBlocked, "blocked"
	case errors.Is(err, calendarfetch.ErrTimeout):
		return apierr.ICSFetchTimeout, "timeout"
	case errors.Is(err, calendarfetch.ErrTooLarge):
		return apierr.ICSTooLarge, "too_large"
	case errors.Is(err, calendarfetch.ErrBusy):
		return apierr.ICSBusy, "failed"
	}
	return apierr.ICSFetchFailed, "failed"
}

func parseErr(err error) error {
	switch {
	case errors.Is(err, icsimport.ErrTooManyEvents):
		return importErr(apierr.ICSTooManyEvents, "more than 3000 events")
	case errors.Is(err, icsimport.ErrTooLarge):
		return importErr(apierr.ICSTooLarge, "too many occurrences")
	}
	return importErr(apierr.ICSInvalid, "not a readable calendar")
}

func (s *calendarService) build(ctx context.Context, userID uint, data []byte, loc *time.Location, today string, src *repositories.CalendarSource) (*Preview, error) {
	ctx, cancel := context.WithTimeout(ctx, parseBudget)
	defer cancel()
	res, err := icsimport.Parse(ctx, data, icsimport.Options{Location: loc, Today: today})
	if err != nil {
		return nil, parseErr(err)
	}
	keys := make([][]byte, 0, len(res.Candidates)+len(res.OneOffs))
	for _, c := range res.Candidates {
		k, _ := hex.DecodeString(c.Key)
		keys = append(keys, k)
	}
	for _, o := range res.OneOffs {
		k, _ := hex.DecodeString(o.Key)
		keys = append(keys, k)
	}
	existing, err := s.repo.ByKeys(userID, keys)
	if err != nil {
		return nil, err
	}
	byKey := map[string]models.Commitment{}
	for _, c := range existing {
		byKey[hex.EncodeToString(c.ExternalKey)] = c
	}
	status := func(key string, want models.Commitment) (string, *uint) {
		c, ok := byKey[key]
		if !ok {
			return "new", nil
		}
		id := c.ID
		edited := c.UpdatedAt.After(c.CreatedAt.Add(time.Second))
		if src != nil && src.LastFetchedAt != nil {
			edited = c.UpdatedAt.After(src.LastFetchedAt.Add(time.Second))
		}
		same := c.StartMin == want.StartMin && c.EndMin == want.EndMin && slices.Equal(c.Weekdays, want.Weekdays) &&
			c.ValidFrom == want.ValidFrom && ((c.ValidTo == nil) == (want.ValidTo == nil)) && (c.ValidTo == nil || *c.ValidTo == *want.ValidTo)
		switch {
		case edited:
			return "edited_in_app", &id
		case same:
			return "unchanged", &id
		}
		return "changed", &id
	}
	p := &Preview{Candidates: []PreviewCandidate{}, OneOffs: []PreviewOneOff{}, Removed: []PreviewRemoved{}, Skipped: res.Skipped}
	seen := map[string]bool{}
	for _, c := range res.Candidates {
		want := models.Commitment{Weekdays: c.Weekdays, StartMin: c.StartMin, EndMin: c.EndMin, ValidFrom: models.Date(c.ValidFrom)}
		if c.ValidTo != nil {
			to := models.Date(*c.ValidTo)
			want.ValidTo = &to
		}
		st, id := status(c.Key, want)
		seen[c.Key] = true
		p.Candidates = append(p.Candidates, PreviewCandidate{Key: c.Key, Name: c.Name, SuggestedKind: "personal", Weekdays: c.Weekdays,
			StartMin: c.StartMin, EndMin: c.EndMin, ValidFrom: c.ValidFrom, ValidTo: c.ValidTo, Notes: c.Notes, Status: st, CommitmentID: id})
	}
	for _, o := range res.OneOffs {
		d := models.Date(o.Date)
		st, _ := status(o.Key, models.Commitment{StartMin: o.StartMin, EndMin: o.EndMin, ValidFrom: d, ValidTo: &d})
		seen[o.Key] = true
		p.OneOffs = append(p.OneOffs, PreviewOneOff{Key: o.Key, Name: o.Name, Date: o.Date, StartMin: o.StartMin, EndMin: o.EndMin, Status: st})
	}
	if src != nil {
		mine, err := s.repo.OfSource(userID, src.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range mine {
			if c.ExternalKey != nil && !seen[hex.EncodeToString(c.ExternalKey)] {
				p.Removed = append(p.Removed, PreviewRemoved{CommitmentID: c.ID, Name: c.Name})
			}
		}
	}
	return p, nil
}

func (s *calendarService) PreviewFile(ctx context.Context, userID uint, data []byte, loc *time.Location, today string) (*Preview, error) {
	if len(data) > MaxICSFile {
		return nil, importErr(apierr.ICSFileTooLarge, "the file is larger than 1 MiB")
	}
	return s.build(ctx, userID, data, loc, today, nil)
}

func (s *calendarService) PreviewURL(ctx context.Context, userID uint, url string, loc *time.Location, today string) (*Preview, error) {
	data, host, err := s.fetch.Fetch(ctx, url)
	if err != nil {
		code, _ := FetchCode(err)
		return nil, importErr(code, "the calendar could not be read")
	}
	p, err := s.build(ctx, userID, data, loc, today, nil)
	if err != nil {
		return nil, err
	}
	p.Source = &PreviewSource{Host: host}
	return p, nil
}

func (s *calendarService) PreviewSource(ctx context.Context, userID, sourceID uint, loc *time.Location, today string) (*Preview, error) {
	src, err := s.repo.Source(userID, sourceID)
	if err != nil {
		return nil, err
	}
	url, err := s.box.Open(src.URLCiphertext) // decrypted only here, for this refresh
	if err != nil {
		return nil, importErr(apierr.ICSFetchFailed, "the linked calendar can no longer be read")
	}
	data, host, err := s.fetch.Fetch(ctx, url)
	if err != nil {
		code, status := FetchCode(err)
		_ = s.repo.SetStatus(userID, src.ID, status) // "Couldn't refresh — try again later"
		return nil, importErr(code, "the calendar could not be read")
	}
	p, err := s.build(ctx, userID, data, loc, today, src)
	if err != nil {
		_ = s.repo.SetStatus(userID, src.ID, "invalid")
		return nil, err
	}
	id := src.ID
	p.Source = &PreviewSource{ID: &id, Host: host}
	return p, nil
}

func (s *calendarService) Commit(userID uint, in CommitInput, key string) (*CommitResult, error) {
	if in.Origin != "file" && in.Origin != "url" {
		return nil, invalidCommitment(apierr.ValidationFailed, "origin must be file or url")
	}
	if len(in.Items) > 200 {
		return nil, invalidCommitment(apierr.ValidationFailed, "at most 200 items")
	}
	c := repositories.ImportCommit{UserID: userID, Source: models.CommitmentICSFile, Key: key}
	if in.Origin == "url" {
		c.Source = models.CommitmentICSURL
		switch {
		case in.SourceID != nil:
			c.SourceID = in.SourceID
		case in.URL != "":
			u, err := calendarfetch.Normalize(in.URL) // checked again; the URL is not fetched here
			if err != nil {
				code, _ := FetchCode(err)
				return nil, importErr(code, "not a usable calendar address")
			}
			sealed, err := s.box.Seal(u.String())
			if err != nil {
				return nil, err
			}
			c.NewSource = &repositories.CalendarSource{URLCiphertext: sealed, URLFingerprint: s.box.Fingerprint(u.String()), URLHost: u.Hostname()}
		default:
			return nil, invalidCommitment(apierr.ValidationFailed, "a link import needs url or sourceId")
		}
	}
	oneOffs := 0
	for _, item := range in.Items {
		k, err := hex.DecodeString(item.Key)
		if err != nil || len(k) != 32 {
			return nil, invalidCommitment(apierr.ValidationFailed, "key must be 64 hex characters")
		}
		n, err := NormalizeCommitment(item.CommitmentInput)
		if err != nil {
			return nil, err
		}
		if n.ValidTo != nil && *n.ValidTo == n.ValidFrom {
			oneOffs++
		}
		c.Items = append(c.Items, repositories.ImportItem{Commitment: n, Key: k})
	}
	c.RemoveIDs = in.RemoveIDs
	out, err := s.repo.Commit(c)
	if err != nil {
		return nil, err
	}
	res := &CommitResult{Created: len(out.Created), Updated: out.Updated, Removed: out.Removed, SourceID: out.SourceID}
	if out.Replay {
		return res, nil
	}
	for _, created := range out.Created {
		s.events.Record(userID, events.CommitmentCreated, CreatedProps(created))
	}
	if res.Created+res.Updated > 0 {
		s.events.Record(userID, events.CalendarImported, events.Props{"origin": in.Origin, "refresh": in.SourceID != nil,
			"created": res.Created, "updated": res.Updated, "removed": res.Removed, "one_offs": oneOffs})
	}
	return res, nil
}

func (s *calendarService) Sources(userID uint) ([]repositories.SourceSummary, error) {
	return s.repo.Sources(userID)
}

func (s *calendarService) DeleteSource(userID, id uint, withCommitments bool) error {
	return s.repo.DeleteSource(userID, id, withCommitments)
}
