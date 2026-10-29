package models

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"time"
)

// Date is a calendar date stored in a Postgres DATE column and exchanged as "YYYY-MM-DD".
type Date string

// Scan implements sql.Scanner (pgx returns DATE as time.Time).
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*d = Date(v.Format("2006-01-02"))
	case string:
		*d = Date(v)
	case []byte:
		*d = Date(v)
	default:
		return fmt.Errorf("cannot scan %T into Date", src)
	}
	return nil
}

// Value implements driver.Valuer.
func (d Date) Value() (driver.Value, error) { return string(d), nil }

// ClockTime is a time of day stored in a Postgres TIME column and exchanged as
// "HH:MM". "24:00" (end of day) is valid.
type ClockTime string

// Scan implements sql.Scanner (pgx returns TIME as "HH:MM:SS").
func (c *ClockTime) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("cannot scan %T into ClockTime", src)
	}
	if len(s) > 5 {
		s = s[:5]
	}
	*c = ClockTime(s)
	return nil
}

// Value implements driver.Valuer.
func (c ClockTime) Value() (driver.Value, error) { return string(c), nil }

// Minutes returns minutes since midnight for a strict "HH:MM" value (00:00–24:00).
func (c ClockTime) Minutes() (int, bool) {
	s := string(c)
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || h < 0 || m < 0 || m > 59 || h > 24 || (h == 24 && m != 0) {
		return 0, false
	}
	return h*60 + m, true
}
