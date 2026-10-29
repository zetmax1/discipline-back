package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDateScan(t *testing.T) {
	cases := []struct {
		src  any
		want Date
	}{
		{time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), "2026-10-29"},
		{"2026-10-29", "2026-10-29"},
		{[]byte("2026-10-29"), "2026-10-29"},
	}
	for _, tc := range cases {
		var d Date
		if err := d.Scan(tc.src); err != nil || d != tc.want {
			t.Fatalf("Scan(%#v) = %q, %v; want %q", tc.src, d, err, tc.want)
		}
	}
	var d Date
	if err := d.Scan(42); err == nil {
		t.Fatal("scanning an int into Date must fail")
	}
}

func TestClockTimeScan(t *testing.T) {
	cases := []struct {
		src  any
		want ClockTime
	}{
		{"24:00:00", "24:00"},
		{"09:30:00", "09:30"},
		{[]byte("23:15:00"), "23:15"},
		{"09:30", "09:30"},
	}
	for _, tc := range cases {
		var c ClockTime
		if err := c.Scan(tc.src); err != nil || c != tc.want {
			t.Fatalf("Scan(%#v) = %q, %v; want %q", tc.src, c, err, tc.want)
		}
	}
}

func TestTypesValueAndJSON(t *testing.T) {
	if v, _ := Date("2026-10-29").Value(); v != "2026-10-29" {
		t.Fatalf("Date.Value = %v", v)
	}
	if v, _ := ClockTime("24:00").Value(); v != "24:00" {
		t.Fatalf("ClockTime.Value = %v", v)
	}
	b, _ := json.Marshal(Task{Date: "2026-10-29", StartTime: "09:00", EndTime: "24:00"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["date"] != "2026-10-29" || m["startTime"] != "09:00" || m["endTime"] != "24:00" {
		t.Fatalf("JSON contract changed: %s", b)
	}
}

func TestClockTimeMinutes(t *testing.T) {
	if m, ok := ClockTime("24:00").Minutes(); !ok || m != 1440 {
		t.Fatalf("24:00 = %d %v", m, ok)
	}
	if m, ok := ClockTime("09:30").Minutes(); !ok || m != 570 {
		t.Fatalf("09:30 = %d %v", m, ok)
	}
	if _, ok := ClockTime("9am").Minutes(); ok {
		t.Fatal("9am must not parse")
	}
}

func TestVocabularyWordDateUsesTheDateType(t *testing.T) {
	// The column is DATE; pgx scans it as time.Time, which only models.Date accepts.
	var w VocabularyWord
	if err := (&w.Date).Scan(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)); err != nil || w.Date != "2026-10-29" {
		t.Fatalf("got %q, %v", w.Date, err)
	}
}
