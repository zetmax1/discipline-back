package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"discipline-backend/internal/ratelimit"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestThrottle() (*LoginThrottle, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	th := NewLoginThrottle(ratelimit.NewMemoryStoreWithClock(clk.now))
	th.now = clk.now
	return th, clk
}

func locked(t *testing.T, th *LoginThrottle, email string) (bool, time.Duration) {
	t.Helper()
	l, retry, err := th.Locked(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return l, retry
}

func fail(t *testing.T, th *LoginThrottle, email string) {
	t.Helper()
	if err := th.RecordFailure(context.Background(), email); err != nil {
		t.Fatal(err)
	}
}

func TestLoginThrottle_LocksAfterFiveFailures(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 4; i++ {
		fail(t, th, "a@example.com")
		if l, _ := locked(t, th, "a@example.com"); l {
			t.Fatalf("locked after only %d failures", i+1)
		}
	}
	fail(t, th, "a@example.com")
	l, retry := locked(t, th, "a@example.com")
	if !l {
		t.Fatal("expected lock after 5 failures")
	}
	if retry <= 0 || retry > 15*time.Minute {
		t.Fatalf("retry-after should be within 15m, got %v", retry)
	}
}

func TestLoginThrottle_EmailIsNormalized(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 5; i++ {
		fail(t, th, "  A@Example.COM ")
	}
	if l, _ := locked(t, th, "a@example.com"); !l {
		t.Fatal("case/whitespace variants must share one counter")
	}
}

func TestLoginThrottle_UnlocksAfterFifteenMinutes(t *testing.T) {
	th, clk := newTestThrottle()
	for i := 0; i < 5; i++ {
		fail(t, th, "a@example.com")
	}
	clk.advance(15*time.Minute - time.Second)
	if l, _ := locked(t, th, "a@example.com"); !l {
		t.Fatal("must stay locked for 15 minutes")
	}
	clk.advance(2 * time.Second)
	if l, _ := locked(t, th, "a@example.com"); l {
		t.Fatal("must unlock after 15 minutes")
	}
	fail(t, th, "a@example.com")
	if l, _ := locked(t, th, "a@example.com"); l {
		t.Fatal("counter must restart after the lock expires")
	}
}

func TestLoginThrottle_FailuresOutsideWindowDoNotCount(t *testing.T) {
	th, clk := newTestThrottle()
	for i := 0; i < 4; i++ {
		fail(t, th, "a@example.com")
	}
	clk.advance(16 * time.Minute)
	fail(t, th, "a@example.com")
	if l, _ := locked(t, th, "a@example.com"); l {
		t.Fatal("failures older than 15 minutes must not count")
	}
}

func TestLoginThrottle_ResetClearsFailures(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 4; i++ {
		fail(t, th, "a@example.com")
	}
	if err := th.Reset(context.Background(), "a@example.com"); err != nil {
		t.Fatal(err)
	}
	fail(t, th, "a@example.com")
	if l, _ := locked(t, th, "a@example.com"); l {
		t.Fatal("successful login must reset the counter")
	}
}

func TestLoginThrottle_SprayingOtherEmailsKeepsALock(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 5; i++ {
		fail(t, th, "victim@example.com")
	}
	for i := 0; i < 100; i++ {
		fail(t, th, fmt.Sprintf("u%d@spray.example", i))
	}
	if l, _ := locked(t, th, "victim@example.com"); !l {
		t.Fatal("spraying other emails must not affect an active lock")
	}
}
