package icsimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/ics/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func opts(zone, today string) Options {
	loc, _ := time.LoadLocation(zone)
	return Options{Location: loc, Today: today}
}

func parse(t *testing.T, name string, o Options) *Result {
	t.Helper()
	r, err := Parse(context.Background(), load(t, name), o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// summary renders candidates compactly: name, weekdays, minutes, validity, notes.
func summary(r *Result) []string {
	var out []string
	for _, c := range r.Candidates {
		to := "open"
		if c.ValidTo != nil {
			to = *c.ValidTo
		}
		out = append(out, fmt.Sprintf("%s %v %d-%d %s..%s %v", c.Name, c.Weekdays, c.StartMin, c.EndMin, c.ValidFrom, to, c.Notes))
	}
	return out
}

func TestGoogle_BerlinUserGetsOneCandidatePerClass(t *testing.T) {
	r := parse(t, "google-berlin-dst.ics", opts("Europe/Berlin", "2027-03-01"))
	want := []string{
		"Statistics lecture [1 3] 600-690 2027-03-01..2027-04-28 []",
		"Choir [2] 1080-1170 2027-03-02..open [exdates_ignored overrides_ignored]",
	}
	if got := summary(r); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(r.OneOffs) != 0 {
		t.Fatalf("a moved single instance is not a one-off: %+v", r.OneOffs)
	}
}

func TestGoogle_TashkentUserSeesTheDSTShiftAsSeparateCandidates(t *testing.T) {
	r := parse(t, "google-berlin-dst.ics", opts("Asia/Tashkent", "2027-03-01"))
	got := summary(r)
	want := []string{
		"Statistics lecture [1 3] 840-930 2027-03-01..2027-03-24 [dst_shift]",
		"Statistics lecture [1 3] 780-870 2027-03-29..2027-04-28 [dst_shift]",
		"Choir [2] 1320-1410 2027-03-02..2027-03-23 [dst_shift exdates_ignored overrides_ignored]",
		"Choir [2] 1260-1350 2027-03-30..2027-10-26 [dst_shift exdates_ignored overrides_ignored]",
		"Choir [2] 1320-1410 2027-11-02..2028-03-01 [dst_shift until_horizon exdates_ignored overrides_ignored]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	keys := map[string]bool{}
	for _, c := range r.Candidates {
		if len(c.Key) != 64 || keys[c.Key] {
			t.Fatalf("keys are distinct hex SHA-256 values: %q", c.Key)
		}
		keys[c.Key] = true
		if strings.Contains(c.Key, "google") {
			t.Fatal("the raw UID never appears")
		}
	}
	again := parse(t, "google-berlin-dst.ics", opts("Asia/Tashkent", "2027-03-01"))
	if again.Candidates[0].Key != r.Candidates[0].Key {
		t.Fatal("keys are stable across reads (refresh matches them)")
	}
}

func TestMisc_TimesSkipsAndOneOffs(t *testing.T) {
	r := parse(t, "misc.ics", opts("Asia/Tashkent", "2027-03-01"))
	want := []string{
		"Morning swim [1 2 3 4 5 6 7] 600-660 2027-03-01..2027-03-10 []",
		"Floating class [2 4] 540-600 2027-03-02..open []",
		"Night shift [5] 1380-60 2027-03-05..open []",
	}
	if got := summary(r); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(r.OneOffs) != 1 || r.OneOffs[0].Name != "Dentist, then shopping" || r.OneOffs[0].Date != "2027-03-12" ||
		r.OneOffs[0].StartMin != 900 || r.OneOffs[0].EndMin != 990 {
		t.Fatalf("one-offs: %+v", r.OneOffs)
	}
	s := r.Skipped
	if s.AllDay != 1 || s.TooLong != 1 || s.Ended != 1 || s.UnsupportedRepeat != 3 || s.UnknownTimezone != 1 || s.Invalid != 1 {
		t.Fatalf("skipped: %+v", s)
	}
}

func TestOutlook_VTimezoneAndWindowsZoneNames(t *testing.T) {
	r := parse(t, "outlook-vtimezone.ics", opts("Asia/Tashkent", "2027-03-01"))
	want := []string{
		"Team meeting [1] 840-900 2027-03-01..2027-03-22 [dst_shift]",
		"Team meeting [1] 780-840 2027-03-29..2027-04-19 [dst_shift]",
		"Windows zone name [2] 840-900 2027-03-02..2027-03-23 [dst_shift]",
		"Windows zone name [2] 780-840 2027-03-30..2027-04-20 [dst_shift]",
	}
	if got := summary(r); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestNewYork_FallBack(t *testing.T) {
	r := parse(t, "newyork-fallback.ics", opts("Asia/Tashkent", "2026-10-19"))
	want := []string{
		"NY standup [1] 1080-1140 2026-10-19..2026-10-26 [dst_shift]",
		"NY standup [1] 1140-1200 2026-11-02..2026-11-30 [dst_shift]",
	}
	if got := summary(r); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLimitsAndMalformedInput(t *testing.T) {
	for name, want := range map[string]error{"too-many.ics": ErrTooManyEvents, "long-line.ics": ErrInvalid, "not-ics.ics": ErrInvalid} {
		if _, err := Parse(context.Background(), load(t, name), opts("Asia/Tashkent", "2027-03-01")); !errors.Is(err, want) {
			t.Errorf("%s: want %v, got %v", name, want, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, load(t, "google-berlin-dst.ics"), opts("Asia/Tashkent", "2027-03-01")); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an expired CPU budget stops the expansion: %v", err)
	}
}

func TestCleanName(t *testing.T) {
	if got := cleanName("  Lab\x07 work\u0000 " + strings.Repeat("я", 120)); got != "Lab work "+strings.Repeat("я", 91) {
		t.Fatalf("control characters stripped, cut to 100 runes: %q (%d)", got, len([]rune(got)))
	}
	if cleanName("   ") == "" {
		t.Fatal("an event without a title still gets a name")
	}
}
