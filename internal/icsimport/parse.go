// Package icsimport turns an iCalendar file into candidate commitments in the user's zone
// (F9a §4.2–4.3). It never stores the file, never keeps a raw UID (keys are SHA-256), and bounds
// every step: file size is checked by the caller, here events, line length, occurrences and CPU.
package icsimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

var (
	ErrInvalid       = errors.New("not a readable calendar")      // ics_invalid
	ErrTooManyEvents = errors.New("more than 3000 events")        // ics_too_many_events
	ErrTooLarge      = errors.New("too many occurrences or time") // ics_too_large
)

const (
	horizonDays = 366
	oneOffDays  = 56
	maxEvents   = 3000
	maxPerEvent = 1000
	maxTotal    = 20000
	maxLine     = 8 << 10
	maxName     = 100
	maxOneOffs  = 200
	dateLayout  = "2006-01-02"
)

// Options: the user's zone and today's date there (YYYY-MM-DD).
type Options struct {
	Location *time.Location
	Today    string
}

// Candidate is a repeating event as one commitment-to-be.
type Candidate struct {
	Key       string
	Name      string
	Weekdays  []int
	StartMin  int
	EndMin    int
	ValidFrom string
	ValidTo   *string
	Notes     []string
}

// OneOff is a single event in the next 56 days.
type OneOff struct {
	Key      string
	Name     string
	Date     string
	StartMin int
	EndMin   int
}

// Skipped counts what was not imported, by reason (counts only; never "failed").
type Skipped struct {
	AllDay             int `json:"allDay"`
	TooLong            int `json:"tooLong"`
	Ended              int `json:"ended"`
	UnsupportedRepeat  int `json:"unsupportedRepeat"`
	TooManyOccurrences int `json:"tooManyOccurrences"`
	UnknownTimezone    int `json:"unknownTimezone"`
	Invalid            int `json:"invalid"`
}

type Result struct {
	Candidates []Candidate
	OneOffs    []OneOff
	Skipped    Skipped
}

func key(uid string, n string) string {
	sum := sha256.Sum256([]byte(uid + ":" + n))
	return hex.EncodeToString(sum[:])
}

// cleanName strips control characters and cuts SUMMARY to 100 runes.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxName {
		s = string(r[:maxName])
	}
	if s == "" {
		return "(no title)"
	}
	return s
}

// precheck: it must look like a calendar, and no unfolded line may exceed 8 KiB.
func precheck(data []byte) error {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	if !bytes.HasPrefix(bytes.ToUpper(trimmed[:min(len(trimmed), 15)]), []byte("BEGIN:VCALENDAR")) {
		return ErrInvalid
	}
	cur := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			cur += len(line) - 1
		} else {
			cur = len(line)
		}
		if cur > maxLine {
			return ErrInvalid
		}
	}
	return nil
}

var durRe = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func parseDuration(s string) (time.Duration, bool) {
	m := durRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n := func(i int) time.Duration { v, _ := strconv.Atoi(m[i]); return time.Duration(v) }
	d := n(2)*7*24*time.Hour + n(3)*24*time.Hour + n(4)*time.Hour + n(5)*time.Minute + n(6)*time.Second
	if m[1] == "-" {
		d = -d
	}
	return d, true
}

type parser struct {
	opt     Options
	today   time.Time
	horizon string
	zones   map[string]*ics.VTimezone
	built   map[string]*vtimezone
}

// zoneOf resolves a DTSTART/DTEND: Z = UTC; TZID = IANA, else the Windows map, else the file's
// VTIMEZONE; no TZID = floating (the user's zone). ok = false: unknown zone.
func (p *parser) zoneOf(prop *ics.IANAProperty) (zone, bool) {
	if strings.HasSuffix(prop.Value, "Z") {
		return ianaZone{time.UTC}, true
	}
	tzids := prop.ICalParameters["TZID"]
	if len(tzids) == 0 {
		return ianaZone{p.opt.Location}, true
	}
	tzid := strings.Trim(tzids[0], `"`)
	if loc, err := time.LoadLocation(tzid); err == nil && tzid != "" && tzid != "Local" {
		return ianaZone{loc}, true
	}
	if name, ok := windowsZones[tzid]; ok {
		if loc, err := time.LoadLocation(name); err == nil {
			return ianaZone{loc}, true
		}
	}
	if z, ok := p.built[tzid]; ok {
		return z, z != nil
	}
	if tz, ok := p.zones[tzid]; ok {
		z, ok := buildVTimezone(tz, p.today.AddDate(2, 0, 0))
		if !ok {
			p.built[tzid] = nil
			return nil, false
		}
		p.built[tzid] = z
		return z, true
	}
	return nil, false
}

func wallOf(v string) (time.Time, bool) {
	t, err := time.Parse("20060102T150405", strings.TrimSuffix(v, "Z"))
	return t, err == nil
}

type occurrence struct {
	date     string
	weekday  int
	startMin int
	endMin   int
}

// local converts an instant and duration to the user's date, weekday and minutes; an end on the next
// local day is overnight (endMin < startMin), an end exactly at midnight is 1440.
func (p *parser) local(start time.Time, dur time.Duration) occurrence {
	s := start.In(p.opt.Location)
	e := start.Add(dur).In(p.opt.Location)
	o := occurrence{date: s.Format(dateLayout), weekday: (int(s.Weekday())+6)%7 + 1, startMin: s.Hour()*60 + s.Minute(), endMin: e.Hour()*60 + e.Minute()}
	if e.Format(dateLayout) != o.date && o.endMin == 0 {
		o.endMin = 1440
	}
	return o
}

// Parse reads the calendar and builds the preview's candidates, one-offs and skip counts.
func Parse(ctx context.Context, data []byte, opt Options) (*Result, error) {
	if err := precheck(data); err != nil {
		return nil, err
	}
	cal, err := ics.ParseCalendar(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	events := cal.Events()
	if len(events) > maxEvents {
		return nil, ErrTooManyEvents
	}
	today, err := time.ParseInLocation(dateLayout, opt.Today, opt.Location)
	if err != nil {
		return nil, ErrInvalid
	}
	p := &parser{opt: opt, today: today, horizon: today.AddDate(0, 0, horizonDays).Format(dateLayout),
		zones: map[string]*ics.VTimezone{}, built: map[string]*vtimezone{}}
	for _, tz := range cal.Timezones() {
		if id := tz.GetProperty(ics.ComponentPropertyTzid); id != nil {
			p.zones[strings.Trim(id.Value, `"`)] = tz
		}
	}
	overridden := map[string]bool{}
	for _, e := range events {
		if e.GetProperty(ics.ComponentPropertyRecurrenceId) != nil {
			overridden[uidOf(e)] = true
		}
	}
	res := &Result{Candidates: []Candidate{}, OneOffs: []OneOff{}}
	total := 0
	for _, e := range events {
		if e.GetProperty(ics.ComponentPropertyRecurrenceId) != nil {
			continue // a moved single instance: not kept (owner answer 4), noted on its series
		}
		if err := p.event(ctx, e, overridden, res, &total); err != nil {
			return nil, err
		}
	}
	if len(res.OneOffs) > maxOneOffs {
		res.OneOffs = res.OneOffs[:maxOneOffs]
	}
	return res, nil
}

func uidOf(e *ics.VEvent) string {
	if u := e.GetProperty(ics.ComponentPropertyUniqueId); u != nil {
		return u.Value
	}
	return ""
}

func (p *parser) event(ctx context.Context, e *ics.VEvent, overridden map[string]bool, res *Result, total *int) error {
	sk := &res.Skipped
	if st := e.GetProperty(ics.ComponentPropertyStatus); st != nil && strings.EqualFold(st.Value, "CANCELLED") {
		sk.Invalid++
		return nil
	}
	dt := e.GetProperty(ics.ComponentPropertyDtStart)
	if dt == nil {
		sk.Invalid++
		return nil
	}
	if v := dt.ICalParameters["VALUE"]; (len(v) > 0 && strings.EqualFold(v[0], "DATE")) || len(dt.Value) == 8 {
		sk.AllDay++
		return nil
	}
	z, ok := p.zoneOf(dt)
	if !ok {
		sk.UnknownTimezone++
		return nil
	}
	wall, ok := wallOf(dt.Value)
	if !ok {
		sk.Invalid++
		return nil
	}
	start := z.toUTC(wall)
	var dur time.Duration
	if end := e.GetProperty(ics.ComponentPropertyDtEnd); end != nil {
		ez, ok := p.zoneOf(end)
		ew, ok2 := wallOf(end.Value)
		if !ok || !ok2 {
			sk.Invalid++
			return nil
		}
		dur = ez.toUTC(ew).Sub(start)
	} else if d := e.GetProperty(ics.ComponentPropertyDuration); d != nil {
		if dur, ok = parseDuration(d.Value); !ok {
			sk.Invalid++
			return nil
		}
	}
	if dur <= 0 || dur >= 24*time.Hour {
		sk.TooLong++
		return nil
	}
	uid := uidOf(e)
	name := ""
	if s := e.GetProperty(ics.ComponentPropertySummary); s != nil {
		name = s.Value
	}
	name = cleanName(name)
	rule := e.GetProperty(ics.ComponentPropertyRrule)
	if rule == nil {
		o := p.local(start, dur)
		switch {
		case o.date < p.opt.Today:
			sk.Ended++
		case o.date <= p.today.AddDate(0, 0, oneOffDays).Format(dateLayout):
			res.OneOffs = append(res.OneOffs, OneOff{Key: key(uid, "0"), Name: name, Date: o.date, StartMin: o.startMin, EndMin: o.endMin})
		}
		return nil
	}
	ro, err := rrule.StrToROption(rule.Value)
	if err != nil {
		sk.UnsupportedRepeat++
		return nil
	}
	if (ro.Freq != rrule.DAILY && ro.Freq != rrule.WEEKLY) || ro.Interval > 1 || len(ro.Bysetpos) > 0 || len(ro.Bymonthday) > 0 ||
		len(ro.Byyearday) > 0 || len(ro.Byweekno) > 0 || len(ro.Byhour) > 0 || len(ro.Byminute) > 0 || len(ro.Bysecond) > 0 {
		sk.UnsupportedRepeat++
		return nil
	}
	until := ro.Until
	hasEnd := ro.Count > 0 || !until.IsZero()
	ro.Until, ro.Dtstart = time.Time{}, wall // expand in wall-clock time; UNTIL is checked on the real instant
	r, err := rrule.NewRRule(*ro)
	if err != nil {
		sk.UnsupportedRepeat++
		return nil
	}
	var occ []occurrence
	hitHorizon := false
	next := r.Iterator()
	for i := 0; ; i++ {
		if i%256 == 0 && ctx.Err() != nil {
			return ErrTooLarge
		}
		w, ok := next()
		if !ok {
			break
		}
		s := z.toUTC(w)
		if !until.IsZero() && s.After(until) {
			break
		}
		o := p.local(s, dur)
		if o.date > p.horizon {
			hitHorizon = true
			break
		}
		if o.date < p.opt.Today {
			continue
		}
		occ = append(occ, o)
		if len(occ) > maxPerEvent {
			sk.TooManyOccurrences++
			return nil
		}
		if *total++; *total > maxTotal {
			return ErrTooLarge
		}
	}
	if len(occ) == 0 {
		if hasEnd && !hitHorizon {
			sk.Ended++
		}
		return nil
	}
	// Runs of the same local times; a DST shift seen from another zone starts a new run.
	type group struct {
		start, end int
		days       []int
		from, to   string
	}
	var groups []*group
	for _, o := range occ {
		g := (*group)(nil)
		if len(groups) > 0 {
			g = groups[len(groups)-1]
		}
		if g == nil || g.start != o.startMin || g.end != o.endMin {
			g = &group{start: o.startMin, end: o.endMin, from: o.date}
			groups = append(groups, g)
		}
		if !slices.Contains(g.days, o.weekday) {
			g.days = append(g.days, o.weekday)
		}
		g.to = o.date
	}
	for i, g := range groups {
		sort.Ints(g.days)
		var notes []string
		if len(groups) > 1 {
			notes = append(notes, "dst_shift")
		}
		to := g.to
		validTo := &to
		if i == len(groups)-1 && hitHorizon {
			if len(groups) == 1 && !hasEnd {
				validTo = nil
			} else {
				h := p.horizon
				validTo = &h
				notes = append(notes, "until_horizon")
			}
		}
		if len(e.GetProperties(ics.ComponentPropertyExdate)) > 0 {
			notes = append(notes, "exdates_ignored")
		}
		if overridden[uid] {
			notes = append(notes, "overrides_ignored")
		}
		if notes == nil {
			notes = []string{}
		}
		res.Candidates = append(res.Candidates, Candidate{Key: key(uid, strconv.Itoa(i)), Name: name, Weekdays: g.days,
			StartMin: g.start, EndMin: g.end, ValidFrom: g.from, ValidTo: validTo, Notes: notes})
	}
	return nil
}
