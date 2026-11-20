package models

import (
	"testing"
	"time"
)

func TestISOWeekdayRoundTripsAcrossASunday(t *testing.T) {
	// Saturday 2026-11-07, Sunday 2026-11-08, Monday 2026-11-09.
	for date, iso := range map[string]int{"2026-11-07": 6, "2026-11-08": 7, "2026-11-09": 1, "2026-11-04": 3} {
		d, _ := time.Parse("2006-01-02", date)
		if got := ISOWeekday(d); got != iso {
			t.Errorf("%s: ISO %d, got %d", date, iso, got)
		}
		if GoWeekday(iso) != d.Weekday() {
			t.Errorf("%s: back to %s, got %s", date, d.Weekday(), GoWeekday(iso))
		}
	}
	if GoWeekday(7) != time.Sunday {
		t.Fatal("ISO 7 is Sunday (Go's 0)")
	}
}

func TestWeekdaysScanAndValue(t *testing.T) {
	var w Weekdays
	if err := w.Scan("{1,3,5}"); err != nil || len(w) != 3 || !w.Has(3) || w.Has(2) {
		t.Fatalf("scan: %v %v", w, err)
	}
	if v, _ := w.Value(); v != "{1,3,5}" {
		t.Fatalf("value: %v", v)
	}
	if err := w.Scan([]byte("{}")); err != nil || len(w) != 0 {
		t.Fatalf("empty: %v %v", w, err)
	}
}
