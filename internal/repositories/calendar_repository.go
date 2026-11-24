package repositories

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"discipline-backend/internal/models"
)

// MaxCalendarSources is the most linked calendars one user keeps (F9a §3.2).
const MaxCalendarSources = 5

var (
	ErrSourceNotFound = errors.New("calendar source not found")
	ErrSourceLimit    = errors.New("calendar source limit reached")
)

// CalendarSource is a linked secret iCal URL; the URL is only ever stored encrypted.
type CalendarSource struct {
	ID             uint
	UserID         uint
	URLCiphertext  []byte `gorm:"column:url_ciphertext"`
	URLFingerprint []byte `gorm:"column:url_fingerprint"`
	URLHost        string `gorm:"column:url_host"`
	LastFetchedAt  *time.Time
	LastStatus     *string
	CreatedAt      time.Time
}

func (CalendarSource) TableName() string { return "calendar_sources" }

// SourceSummary is a source as the web lists it (never the URL).
type SourceSummary struct {
	ID              uint       `json:"id"`
	Host            string     `json:"host"`
	LastFetchedAt   *time.Time `json:"lastFetchedAt"`
	LastStatus      *string    `json:"lastStatus"`
	CommitmentCount int        `json:"commitmentCount"`
}

// ImportItem is one commitment to upsert by its external key.
type ImportItem struct {
	Commitment models.Commitment
	Key        []byte
}

// ImportCommit is one confirmed import, applied in one transaction.
type ImportCommit struct {
	UserID    uint
	Source    string // ics_file or ics_url
	SourceID  *uint  // an existing source (refresh)
	NewSource *CalendarSource
	Items     []ImportItem
	RemoveIDs []uint
	Key       string // Idempotency-Key
}

// ImportOutcome reports what changed; Created lists the new rows (for events).
type ImportOutcome struct {
	Created  []models.Commitment
	Updated  int
	Removed  int
	SourceID *uint
	Replay   bool
}

type CalendarRepository interface {
	Source(userID, id uint) (*CalendarSource, error)
	Sources(userID uint) ([]SourceSummary, error)
	SetStatus(userID, id uint, status string) error
	DeleteSource(userID, id uint, withCommitments bool) error
	// ByKeys returns the user's commitments with these external keys.
	ByKeys(userID uint, keys [][]byte) ([]models.Commitment, error)
	// OfSource returns the commitments imported from a source.
	OfSource(userID, sourceID uint) ([]models.Commitment, error)
	Commit(in ImportCommit) (*ImportOutcome, error)
}

type calendarRepository struct{ db *gorm.DB }

func NewCalendarRepository(db *gorm.DB) CalendarRepository { return &calendarRepository{db: db} }

func (r *calendarRepository) Source(userID, id uint) (*CalendarSource, error) {
	var s CalendarSource
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSourceNotFound
	}
	return &s, err
}

func (r *calendarRepository) Sources(userID uint) ([]SourceSummary, error) {
	out := []SourceSummary{}
	err := r.db.Raw(`SELECT s.id, s.url_host AS host, s.last_fetched_at, s.last_status,
		(SELECT count(*) FROM commitments c WHERE c.calendar_source_id = s.id) AS commitment_count
		FROM calendar_sources s WHERE s.user_id = ? ORDER BY s.id`, userID).Scan(&out).Error
	return out, err
}

func (r *calendarRepository) SetStatus(userID, id uint, status string) error {
	return r.db.Exec(`UPDATE calendar_sources SET last_status = ? WHERE id = ? AND user_id = ?`, status, id, userID).Error
}

func (r *calendarRepository) DeleteSource(userID, id uint, withCommitments bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if withCommitments {
			if err := tx.Exec(`DELETE FROM commitments WHERE user_id = ? AND calendar_source_id = ?`, userID, id).Error; err != nil {
				return err
			}
		}
		// Kept commitments stay as manual ones: ON DELETE SET NULL clears their source.
		res := tx.Exec(`DELETE FROM calendar_sources WHERE id = ? AND user_id = ?`, id, userID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrSourceNotFound
		}
		return nil
	})
}

func (r *calendarRepository) ByKeys(userID uint, keys [][]byte) ([]models.Commitment, error) {
	out := []models.Commitment{}
	if len(keys) == 0 {
		return out, nil
	}
	err := r.db.Where("user_id = ? AND external_key IN ?", userID, keys).Find(&out).Error
	return out, err
}

func (r *calendarRepository) OfSource(userID, sourceID uint) ([]models.Commitment, error) {
	out := []models.Commitment{}
	err := r.db.Where("user_id = ? AND calendar_source_id = ?", userID, sourceID).Order("id").Find(&out).Error
	return out, err
}

func (r *calendarRepository) Commit(in ImportCommit) (*ImportOutcome, error) {
	out := &ImportOutcome{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := lockCommitments(tx, in.UserID); err != nil {
			return err
		}
		if in.Key != "" {
			_, found, err := earlierResource(tx, in.UserID, in.Key)
			if err != nil {
				return err
			}
			if found {
				out.Replay = true
				return nil
			}
		}
		var sourceID *uint
		switch {
		case in.SourceID != nil:
			var n int64
			if err := tx.Model(&CalendarSource{}).Where("id = ? AND user_id = ?", *in.SourceID, in.UserID).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				return ErrSourceNotFound
			}
			sourceID = in.SourceID
		case in.NewSource != nil:
			var existing CalendarSource
			err := tx.Where("user_id = ? AND url_fingerprint = ?", in.UserID, in.NewSource.URLFingerprint).First(&existing).Error
			if err == nil {
				sourceID = &existing.ID
				break
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var n int64
			if err := tx.Model(&CalendarSource{}).Where("user_id = ?", in.UserID).Count(&n).Error; err != nil {
				return err
			}
			if n >= MaxCalendarSources {
				return ErrSourceLimit
			}
			s := *in.NewSource
			s.UserID = in.UserID
			if err := tx.Create(&s).Error; err != nil {
				return err
			}
			sourceID = &s.ID
		}
		out.SourceID = sourceID
		if len(in.RemoveIDs) > 0 {
			if sourceID == nil {
				return ErrSourceNotFound
			}
			res := tx.Exec(`DELETE FROM commitments WHERE user_id = ? AND calendar_source_id = ? AND id IN ?`, in.UserID, *sourceID, in.RemoveIDs)
			if res.Error != nil {
				return res.Error
			}
			out.Removed = int(res.RowsAffected)
		}
		for _, item := range in.Items {
			c := item.Commitment
			var existing models.Commitment
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND external_key = ?", in.UserID, item.Key).First(&existing).Error
			if err == nil {
				if err := tx.Model(&models.Commitment{}).Where("id = ?", existing.ID).Updates(map[string]any{
					"name": c.Name, "kind": c.Kind, "weekdays": c.Weekdays, "start_min": c.StartMin, "end_min": c.EndMin,
					"valid_from": c.ValidFrom, "valid_to": c.ValidTo, "calendar_source_id": sourceID, "updated_at": gorm.Expr("now()"),
				}).Error; err != nil {
					return err
				}
				out.Updated++
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			var n int64
			if err := tx.Model(&models.Commitment{}).Where("user_id = ?", in.UserID).Count(&n).Error; err != nil {
				return err
			}
			if n >= MaxCommitments {
				return ErrCommitmentLimit
			}
			c.UserID, c.Source, c.CalendarSourceID, c.ExternalKey = in.UserID, in.Source, sourceID, item.Key
			if err := tx.Create(&c).Error; err != nil {
				return err
			}
			out.Created = append(out.Created, c)
		}
		if sourceID != nil {
			if err := tx.Exec(`UPDATE calendar_sources SET last_fetched_at = now(), last_status = 'ok' WHERE id = ?`, *sourceID).Error; err != nil {
				return err
			}
		}
		if in.Key != "" {
			return rememberKey(tx, in.UserID, in.Key, 0)
		}
		return nil
	})
	return out, err
}
