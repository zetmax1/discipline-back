package services

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestThrottle(max int) (*LoginThrottle, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	th := NewLoginThrottle(max)
	th.now = clk.now
	return th, clk
}

func TestLoginThrottle_LocksAfterFiveFailures(t *testing.T) {
	th, _ := newTestThrottle(100)
	for i := 0; i < 4; i++ {
		th.RecordFailure("a@example.com")
		if locked, _ := th.Locked("a@example.com"); locked {
			t.Fatalf("locked after only %d failures", i+1)
		}
	}
	th.RecordFailure("a@example.com")
	locked, retry := th.Locked("a@example.com")
	if !locked {
		t.Fatal("expected lock after 5 failures")
	}
	if retry <= 0 || retry > 15*time.Minute {
		t.Fatalf("retry-after should be within 15m, got %v", retry)
	}
}

func TestLoginThrottle_EmailIsNormalized(t *testing.T) {
	th, _ := newTestThrottle(100)
	for i := 0; i < 5; i++ {
		th.RecordFailure("  A@Example.COM ")
	}
	if locked, _ := th.Locked("a@example.com"); !locked {
		t.Fatal("case/whitespace variants must share one counter")
	}
}

func TestLoginThrottle_UnlocksAfterFifteenMinutes(t *testing.T) {
	th, clk := newTestThrottle(100)
	for i := 0; i < 5; i++ {
		th.RecordFailure("a@example.com")
	}
	clk.advance(15*time.Minute - time.Second)
	if locked, _ := th.Locked("a@example.com"); !locked {
		t.Fatal("must stay locked for 15 minutes")
	}
	clk.advance(2 * time.Second)
	if locked, _ := th.Locked("a@example.com"); locked {
		t.Fatal("must unlock after 15 minutes")
	}
	th.RecordFailure("a@example.com")
	if locked, _ := th.Locked("a@example.com"); locked {
		t.Fatal("counter must restart after the lock expires")
	}
}

func TestLoginThrottle_FailuresOutsideWindowDoNotCount(t *testing.T) {
	th, clk := newTestThrottle(100)
	for i := 0; i < 4; i++ {
		th.RecordFailure("a@example.com")
	}
	clk.advance(16 * time.Minute)
	th.RecordFailure("a@example.com")
	if locked, _ := th.Locked("a@example.com"); locked {
		t.Fatal("failures older than 15 minutes must not count")
	}
}

func TestLoginThrottle_ResetClearsFailures(t *testing.T) {
	th, _ := newTestThrottle(100)
	for i := 0; i < 4; i++ {
		th.RecordFailure("a@example.com")
	}
	th.Reset("a@example.com")
	th.RecordFailure("a@example.com")
	if locked, _ := th.Locked("a@example.com"); locked {
		t.Fatal("successful login must reset the counter")
	}
}

func TestLoginThrottle_SizeIsCappedAndKeepsLocks(t *testing.T) {
	th, _ := newTestThrottle(3)
	for i := 0; i < 5; i++ {
		th.RecordFailure("victim@example.com")
	}
	for i := 0; i < 100; i++ {
		th.RecordFailure(string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@spray.example")
	}
	if n := th.size(); n > 3 {
		t.Fatalf("throttle tracked %d emails, cap is 3", n)
	}
	if locked, _ := th.Locked("victim@example.com"); !locked {
		t.Fatal("spraying other emails must not evict an active lock")
	}
}
