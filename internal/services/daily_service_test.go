package services

import (
	"testing"
	"time"

	"discipline-backend/internal/models"
)

// A check-in queued offline counts for the local day the user made it, as long as the client clock
// is believable: the same bounds a queued session start uses (maxOfflineAge, maxClockAhead).
func TestCheckinDay(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tashkent")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 0, 5, 0, 0, loc) // just after midnight, when the queue syncs
	at := func(wall string) *time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04:05", wall, loc)
		if err != nil {
			t.Fatal(err)
		}
		return &tm
	}

	cases := []struct {
		name   string
		madeAt *time.Time
		want   models.Date
	}{
		{"no client time: today", nil, "2026-10-30"},
		{"made before midnight, synced after: the day it was made", at("2026-10-29 23:58:00"), "2026-10-29"},
		{"made just now", at("2026-10-30 00:04:30"), "2026-10-30"},
		{"at the edge of the offline window (just under 24 h old)", at("2026-10-29 00:05:30"), "2026-10-29"},
		{"older than the offline window: today", at("2026-10-29 00:04:30"), "2026-10-30"},
		{"days old: today", at("2026-10-20 10:00:00"), "2026-10-30"},
		{"a little clock skew ahead is accepted", at("2026-10-30 00:05:30"), "2026-10-30"},
		{"more than a minute ahead is clock skew: today", at("2026-10-31 09:00:00"), "2026-10-30"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CheckinDay(c.madeAt, now, loc); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The day is the user's local day, not UTC's.
func TestCheckinDay_UsesTheUsersZone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tashkent")
	if err != nil {
		t.Fatal(err)
	}
	made := time.Date(2026, 10, 29, 20, 30, 0, 0, time.UTC) // 2026-10-30 01:30 in Tashkent
	now := time.Date(2026, 10, 29, 20, 35, 0, 0, time.UTC)
	if got := CheckinDay(&made, now, loc); got != "2026-10-30" {
		t.Errorf("got %q, want 2026-10-30", got)
	}
}
