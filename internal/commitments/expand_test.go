package commitments

import (
	"reflect"
	"testing"
)

func c(id uint, weekdays []int, start, end int, from string, to string) Commitment {
	var vt *string
	if to != "" {
		vt = &to
	}
	return Commitment{ID: id, Weekdays: weekdays, StartMin: start, EndMin: end, ValidFrom: from, ValidTo: vt}
}

// 2026-11-16 is a Monday.
func TestExpand_OvernightSplitsOnTheStartDay(t *testing.T) {
	// M2: Mon–Fri 23:00–07:00 blocks Mon 23:00–24:00 and Tue 00:00–07:00, never Mon 00:00–07:00.
	got := Expand([]Commitment{c(1, []int{1, 2, 3, 4, 5}, 1380, 420, "2026-01-01", "")}, "2026-11-16", "2026-11-17")
	want := []Segment{
		{CommitmentID: 1, Date: "2026-11-16", StartMin: 1380, EndMin: 1440, Part: "evening"},
		{CommitmentID: 1, Date: "2026-11-17", StartMin: 0, EndMin: 420, Part: "morning"},
		{CommitmentID: 1, Date: "2026-11-17", StartMin: 1380, EndMin: 1440, Part: "evening"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestExpand_MondayGetsSundaysTailOnlyWhenSundayIsAWeekday(t *testing.T) {
	every := Expand([]Commitment{c(1, []int{1, 2, 3, 4, 5, 6, 7}, 1380, 420, "2026-01-01", "")}, "2026-11-16", "2026-11-16")
	if len(every) != 2 || every[0] != (Segment{CommitmentID: 1, Date: "2026-11-16", StartMin: 0, EndMin: 420, Part: "morning"}) {
		t.Fatalf("Sunday's tail should land on Monday 00:00–07:00, got %+v", every)
	}
	weekdays := Expand([]Commitment{c(1, []int{1, 2, 3, 4, 5}, 1380, 420, "2026-01-01", "")}, "2026-11-16", "2026-11-16")
	if len(weekdays) != 1 || weekdays[0].Part != "evening" {
		t.Fatalf("Mon–Fri: Monday has no morning tail (Sunday is not a weekday), got %+v", weekdays)
	}
}

func TestExpand_ValidityAppliesToTheStartDay(t *testing.T) {
	// validTo = Friday still blocks Saturday 00:00–07:00 (the tail of Friday night).
	got := Expand([]Commitment{c(1, []int{1, 2, 3, 4, 5}, 1380, 420, "2026-11-16", "2026-11-20")}, "2026-11-21", "2026-11-22")
	want := []Segment{{CommitmentID: 1, Date: "2026-11-21", StartMin: 0, EndMin: 420, Part: "morning"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	// Before validFrom nothing, not even a tail.
	if got := Expand([]Commitment{c(1, []int{1, 2, 3, 4, 5, 6, 7}, 1380, 420, "2026-11-17", "")}, "2026-11-16", "2026-11-16"); len(got) != 0 {
		t.Fatalf("nothing before validFrom, got %+v", got)
	}
}

func TestExpand_WholeDaySegmentsAndOneOffs(t *testing.T) {
	got := Expand([]Commitment{
		c(1, []int{1, 3, 5}, 540, 840, "2026-01-01", ""),     // university Mon/Wed/Fri
		c(2, []int{4}, 600, 660, "2026-11-19", "2026-11-19"), // a one-off Thursday
		c(3, []int{7}, 600, 1440, "2026-01-01", ""),          // Sunday 10:00–24:00 (end 1440 is not overnight)
	}, "2026-11-16", "2026-11-22")
	want := []Segment{
		{CommitmentID: 1, Date: "2026-11-16", StartMin: 540, EndMin: 840, Part: "whole"},
		{CommitmentID: 1, Date: "2026-11-18", StartMin: 540, EndMin: 840, Part: "whole"},
		{CommitmentID: 2, Date: "2026-11-19", StartMin: 600, EndMin: 660, Part: "whole"},
		{CommitmentID: 1, Date: "2026-11-20", StartMin: 540, EndMin: 840, Part: "whole"},
		{CommitmentID: 3, Date: "2026-11-22", StartMin: 600, EndMin: 1440, Part: "whole"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestIsoWeekday_AcrossASunday(t *testing.T) {
	for date, want := range map[string]int{"2026-11-15": 7, "2026-11-16": 1, "2026-11-21": 6, "2026-11-22": 7} {
		if got := isoWeekday(mustDate(date)); got != want {
			t.Errorf("%s: got %d want %d", date, got, want)
		}
	}
}

func TestExpand_DSTDaysKeepWallClockMinutes(t *testing.T) {
	// 2026-03-29 is Europe's spring-forward Sunday; minutes are wall-clock and never shift.
	got := Expand([]Commitment{c(1, []int{7}, 120, 180, "2026-01-01", "")}, "2026-03-29", "2026-03-29")
	if len(got) != 1 || got[0].StartMin != 120 || got[0].EndMin != 180 {
		t.Fatalf("got %+v", got)
	}
}
