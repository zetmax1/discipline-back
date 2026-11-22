// Package commitments expands commitments (recurring fixed times the planner must not use) into
// dated segments: one function, used by GET /commitments/occurrences, the Smart Schedule proxy and
// the planner views (docs/design/F9a.md §4.5). Times are wall-clock minutes in the user's zone.
package commitments

import (
	"slices"
	"sort"
	"time"
)

// Commitment is what the expander needs: ISO weekdays, minutes 0–1440 and validity dates
// (YYYY-MM-DD). EndMin < StartMin crosses midnight.
type Commitment struct {
	ID        uint
	Weekdays  []int
	StartMin  int
	EndMin    int
	ValidFrom string
	ValidTo   *string
}

// Segment is one dated piece of a commitment. Part is "whole", or "evening"/"morning" for the two
// halves of an overnight commitment.
type Segment struct {
	CommitmentID uint   `json:"commitmentId"`
	Date         string `json:"date"`
	StartMin     int    `json:"startMin"`
	EndMin       int    `json:"endMin"`
	Part         string `json:"part"`
}

const layout = "2006-01-02"

func mustDate(s string) time.Time {
	t, err := time.Parse(layout, s)
	if err != nil {
		panic(err)
	}
	return t
}

// isoWeekday converts Go's Sunday = 0 … Saturday = 6 to ISO Monday = 1 … Sunday = 7.
func isoWeekday(t time.Time) int { return (int(t.Weekday())+6)%7 + 1 }

// Expand returns the segments on dates in [from, to] (both YYYY-MM-DD, valid). Weekdays and
// validity apply to the start day; an overnight commitment's morning part lands on the next day
// even when that day is past ValidTo. The day before `from` is read so its tail is included.
func Expand(list []Commitment, from, to string) []Segment {
	start, end := mustDate(from), mustDate(to)
	out := []Segment{}
	for d := start.AddDate(0, 0, -1); !d.After(end); d = d.AddDate(0, 0, 1) {
		day := d.Format(layout)
		next := d.AddDate(0, 0, 1).Format(layout)
		for _, c := range list {
			if day < c.ValidFrom || (c.ValidTo != nil && day > *c.ValidTo) || !slices.Contains(c.Weekdays, isoWeekday(d)) {
				continue
			}
			if c.EndMin > c.StartMin {
				out = appendIn(out, Segment{c.ID, day, c.StartMin, c.EndMin, "whole"}, from, to)
				continue
			}
			out = appendIn(out, Segment{c.ID, day, c.StartMin, 1440, "evening"}, from, to)
			out = appendIn(out, Segment{c.ID, next, 0, c.EndMin, "morning"}, from, to)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].StartMin < out[j].StartMin
	})
	return out
}

func appendIn(out []Segment, s Segment, from, to string) []Segment {
	if s.Date < from || s.Date > to {
		return out
	}
	return append(out, s)
}
