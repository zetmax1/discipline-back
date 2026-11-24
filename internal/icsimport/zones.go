package icsimport

import (
	"sort"
	"strconv"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

// zone converts a wall-clock time (carried in a UTC time.Time) to the real instant.
type zone interface {
	toUTC(wall time.Time) time.Time
}

type ianaZone struct{ loc *time.Location }

func (z ianaZone) toUTC(w time.Time) time.Time {
	return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, z.loc).UTC()
}

// windowsZones maps the Windows zone names Outlook writes to IANA names: a subset of CLDR's
// windowsZones.xml (Unicode License v3, THIRD_PARTY.md), "001" territory. Outlook files also carry a
// VTIMEZONE, which is used for any name not listed here.
var windowsZones = map[string]string{
	"UTC": "Etc/UTC", "GMT Standard Time": "Europe/London", "Greenwich Standard Time": "Atlantic/Reykjavik",
	"W. Europe Standard Time": "Europe/Berlin", "Central Europe Standard Time": "Europe/Budapest",
	"Romance Standard Time": "Europe/Paris", "Central European Standard Time": "Europe/Warsaw",
	"GTB Standard Time": "Europe/Bucharest", "FLE Standard Time": "Europe/Kiev", "E. Europe Standard Time": "Europe/Chisinau",
	"Turkey Standard Time": "Europe/Istanbul", "Russian Standard Time": "Europe/Moscow", "Belarus Standard Time": "Europe/Minsk",
	"Arabian Standard Time": "Asia/Dubai", "Azerbaijan Standard Time": "Asia/Baku", "Georgian Standard Time": "Asia/Tbilisi",
	"Caucasus Standard Time": "Asia/Yerevan", "West Asia Standard Time": "Asia/Tashkent", "Ekaterinburg Standard Time": "Asia/Yekaterinburg",
	"Pakistan Standard Time": "Asia/Karachi", "India Standard Time": "Asia/Calcutta", "Central Asia Standard Time": "Asia/Bishkek",
	"Omsk Standard Time": "Asia/Omsk", "SE Asia Standard Time": "Asia/Bangkok", "N. Central Asia Standard Time": "Asia/Novosibirsk",
	"China Standard Time": "Asia/Shanghai", "Singapore Standard Time": "Asia/Singapore", "Tokyo Standard Time": "Asia/Tokyo",
	"Korea Standard Time": "Asia/Seoul", "AUS Eastern Standard Time": "Australia/Sydney",
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "Pacific Standard Time": "America/Los_Angeles",
}

// trans is one VTIMEZONE transition: from `at` (UTC) the offset is `offset` seconds.
type trans struct {
	at     time.Time
	offset int
}

// vtimezone is a VTIMEZONE's STANDARD/DAYLIGHT observances, expanded into transitions.
type vtimezone struct {
	first  int
	trans  []trans
	loaded bool
}

func (z *vtimezone) offsetAt(utc time.Time) int {
	off := z.first
	for _, t := range z.trans {
		if t.at.After(utc) {
			break
		}
		off = t.offset
	}
	return off
}

func (z *vtimezone) toUTC(w time.Time) time.Time {
	guess := w.Add(-time.Duration(z.offsetAt(w)) * time.Second)
	return w.Add(-time.Duration(z.offsetAt(guess)) * time.Second)
}

func parseOffset(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 5 && len(s) != 7 {
		return 0, false
	}
	sign := 1
	switch s[0] {
	case '-':
		sign = -1
	case '+':
	default:
		return 0, false
	}
	h, err1 := strconv.Atoi(s[1:3])
	m, err2 := strconv.Atoi(s[3:5])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return sign * (h*3600 + m*60), true
}

// buildVTimezone expands each observance's transitions up to `until` (yearly rules from 1601 are
// cheap: a few hundred steps). An unreadable observance makes the zone unknown.
func buildVTimezone(tz *ics.VTimezone, until time.Time) (*vtimezone, bool) {
	z := &vtimezone{}
	earliest := time.Time{}
	for _, c := range tz.Components {
		var base *ics.ComponentBase
		switch o := c.(type) {
		case *ics.Standard:
			base = &o.ComponentBase
		case *ics.Daylight:
			base = &o.ComponentBase
		default:
			continue
		}
		from, ok1 := propOffset(base, "TZOFFSETFROM")
		to, ok2 := propOffset(base, "TZOFFSETTO")
		dt := base.GetProperty(ics.ComponentPropertyDtStart)
		if !ok1 || !ok2 || dt == nil {
			return nil, false
		}
		wall, err := time.Parse("20060102T150405", strings.TrimSuffix(dt.Value, "Z"))
		if err != nil {
			return nil, false
		}
		add := func(w time.Time) {
			z.trans = append(z.trans, trans{at: w.Add(-time.Duration(from) * time.Second), offset: to})
		}
		if earliest.IsZero() || wall.Before(earliest) {
			earliest, z.first = wall, from
		}
		rule := base.GetProperty(ics.ComponentPropertyRrule)
		if rule == nil {
			add(wall)
			continue
		}
		opt, err := rrule.StrToROption(rule.Value)
		if err != nil {
			return nil, false
		}
		// rrule-go stops after a few hundred yearly steps, so start the expansion at 1970 (same month,
		// day and time): a yearly observance's dates come from BYMONTH/BYDAY, not from 1601.
		if wall.Year() < 1970 {
			wall = time.Date(1970, wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, time.UTC)
		}
		opt.Dtstart = wall
		r, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, false
		}
		next := r.Iterator()
		for i := 0; i < 2000; i++ {
			w, ok := next()
			if !ok || w.After(until) {
				break
			}
			if w.Year() >= 1970 {
				add(w)
			}
		}
	}
	sort.Slice(z.trans, func(i, j int) bool { return z.trans[i].at.Before(z.trans[j].at) })
	z.loaded = len(z.trans) > 0
	return z, z.loaded
}

func propOffset(b *ics.ComponentBase, name string) (int, bool) {
	p := b.GetProperty(ics.ComponentProperty(name))
	if p == nil {
		return 0, false
	}
	return parseOffset(p.Value)
}
