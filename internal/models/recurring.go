package models

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Weekdays are ISO weekdays (Monday = 1 … Sunday = 7), stored in a smallint[] column.
type Weekdays []int

// Scan implements sql.Scanner for the array literal Postgres sends ("{1,3,5}").
func (w *Weekdays) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("cannot scan %T into Weekdays", src)
	}
	s = strings.Trim(s, "{}")
	out := Weekdays{}
	if s != "" {
		for _, part := range strings.Split(s, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return fmt.Errorf("weekdays %q: %w", s, err)
			}
			out = append(out, n)
		}
	}
	*w = out
	return nil
}

// Value implements driver.Valuer as an array literal.
func (w Weekdays) Value() (driver.Value, error) {
	parts := make([]string, len(w))
	for i, d := range w {
		parts[i] = strconv.Itoa(d)
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

// Has reports whether iso (1-7) is one of the weekdays.
func (w Weekdays) Has(iso int) bool {
	for _, d := range w {
		if d == iso {
			return true
		}
	}
	return false
}

// ISOWeekday converts Go's weekday (Sunday = 0 … Saturday = 6) to ISO (Monday = 1 … Sunday = 7).
func ISOWeekday(t time.Time) int { return (int(t.Weekday())+6)%7 + 1 }

// GoWeekday converts an ISO weekday (1-7) back to Go's time.Weekday.
func GoWeekday(iso int) time.Weekday { return time.Weekday(iso % 7) }

// RecurringRule plans a block on the given weekdays between ValidFrom and ValidTo (inclusive).
// Times are wall-clock minutes, so 09:00 stays 09:00 across a DST change.
type RecurringRule struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	UserID          uint      `gorm:"not null" json:"-"`
	CategoryID      uint      `gorm:"not null" json:"categoryId"`
	Name            string    `gorm:"not null" json:"name"`
	Weekdays        Weekdays  `gorm:"type:smallint[];not null" json:"weekdays"`
	StartMin        int       `gorm:"not null" json:"startMin"`
	EndMin          int       `gorm:"not null" json:"endMin"`
	ValidFrom       Date      `gorm:"type:date;not null" json:"validFrom"`
	ValidTo         *Date     `gorm:"type:date" json:"validTo"`
	SplitFromRuleID *uint     `json:"splitFromRuleId"`
	CreatedAt       time.Time `json:"-"`
	UpdatedAt       time.Time `json:"-"`
}

// RecurringException is a date a rule deliberately has no block on: cancelled by the user, a
// conflict with another block, or moved elsewhere. It is never a miss.
type RecurringException struct {
	RuleID uint   `json:"ruleId"`
	Date   Date   `json:"date"`
	Reason string `json:"reason"`
}
