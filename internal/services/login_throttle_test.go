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

const ipA, ipB = "203.0.113.1", "198.51.100.2"

func check(t *testing.T, th *LoginThrottle, email, ip string) LoginState {
	t.Helper()
	st, err := th.Check(context.Background(), email, ip)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func fail(t *testing.T, th *LoginThrottle, email, ip string) bool {
	t.Helper()
	captcha, err := th.RecordFailure(context.Background(), email, ip)
	if err != nil {
		t.Fatal(err)
	}
	return captcha
}

func TestLoginThrottle_NoDelayForTheFirstThreeFailures(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 3; i++ {
		fail(t, th, "a@example.com", ipA)
		if st := check(t, th, "a@example.com", ipA); st.RetryAfter != 0 {
			t.Fatalf("no delay expected after %d failures, got %v", i+1, st.RetryAfter)
		}
	}
}

func TestLoginThrottle_ProgressiveDelayPerEmailAndIP(t *testing.T) {
	th, clk := newTestThrottle()
	for i := 0; i < 3; i++ {
		fail(t, th, "a@example.com", ipA)
	}
	for _, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second} {
		fail(t, th, "a@example.com", ipA)
		if st := check(t, th, "a@example.com", ipA); st.RetryAfter != want {
			t.Fatalf("expected a %v delay, got %v", want, st.RetryAfter)
		}
		clk.advance(want - time.Second)
		if st := check(t, th, "a@example.com", ipA); st.RetryAfter != time.Second {
			t.Fatalf("one second before the delay ends, expected 1s left, got %v", st.RetryAfter)
		}
		clk.advance(time.Second)
		if st := check(t, th, "a@example.com", ipA); st.RetryAfter != 0 {
			t.Fatalf("the delay must end after %v, still %v", want, st.RetryAfter)
		}
	}
	if st := check(t, th, "a@example.com", ipB); st.RetryAfter != 0 {
		t.Fatal("the delay is per (email, IP): the same email from another IP must not wait")
	}
	if st := check(t, th, "b@example.com", ipA); st.RetryAfter != 0 {
		t.Fatal("the delay is per (email, IP): another email from the same IP must not wait")
	}
}

func TestLoginThrottle_DelayIsCappedAtFifteenMinutes(t *testing.T) {
	th, clk := newTestThrottle()
	for i := 0; i < 14; i++ { // 2^(14-3) s = 2048 s > 15 min
		fail(t, th, "a@example.com", ipA)
		clk.advance(time.Second)
	}
	clk.advance(-time.Second)
	if st := check(t, th, "a@example.com", ipA); st.RetryAfter != 15*time.Minute {
		t.Fatalf("delay must be capped at 15m, got %v", st.RetryAfter)
	}
}

func TestLoginThrottle_FailuresOlderThanFifteenMinutesDoNotCount(t *testing.T) {
	th, clk := newTestThrottle()
	for i := 0; i < 3; i++ {
		fail(t, th, "a@example.com", ipA)
	}
	clk.advance(15 * time.Minute)
	fail(t, th, "a@example.com", ipA)
	if st := check(t, th, "a@example.com", ipA); st.RetryAfter != 0 || st.CaptchaRequired {
		t.Fatalf("only one failure is in the window: no delay, no captcha; got %+v", st)
	}
}

func TestLoginThrottle_CaptchaAfterThreeFailuresPerEmailFromAnyIP(t *testing.T) {
	th, _ := newTestThrottle()
	if fail(t, th, "  A@Example.COM ", ipA) || fail(t, th, "a@example.com", ipB) {
		t.Fatal("captcha must not be required before 3 failures")
	}
	if st := check(t, th, "a@example.com", "192.0.2.3"); st.CaptchaRequired {
		t.Fatal("captcha must not be required after 2 failures")
	}
	if !fail(t, th, "a@example.com", "192.0.2.3") {
		t.Fatal("the 3rd failure must report that a captcha is now required")
	}
	st := check(t, th, "a@example.com", "192.0.2.99")
	if !st.CaptchaRequired {
		t.Fatal("3 failures for an email (any IPs, any case) must require a captcha from every IP")
	}
	if st.RetryAfter != 0 {
		t.Fatal("failures spread over IPs must not delay a new IP: there is no per-email lockout")
	}
}

func TestLoginThrottle_PerIPCapAcrossEmails(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 29; i++ {
		fail(t, th, fmt.Sprintf("u%d@spray.example", i), ipA)
	}
	if st := check(t, th, "fresh@example.com", ipA); st.RetryAfter != 0 {
		t.Fatalf("29 failures from one IP must not block it yet, got %v", st.RetryAfter)
	}
	fail(t, th, "u29@spray.example", ipA)
	st := check(t, th, "fresh@example.com", ipA)
	if st.RetryAfter <= 0 || st.RetryAfter > 15*time.Minute {
		t.Fatalf("30 failures from one IP must block it for up to 15m, got %v", st.RetryAfter)
	}
	if st := check(t, th, "fresh@example.com", ipB); st.RetryAfter != 0 {
		t.Fatal("other IPs are not affected by one IP's failures")
	}
}

func TestLoginThrottle_AttackerCannotLockOutTheOwner(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 20; i++ {
		fail(t, th, "victim@example.com", ipA)
	}
	st := check(t, th, "victim@example.com", ipB)
	if st.RetryAfter != 0 {
		t.Fatalf("the owner on another IP must not be locked out, got %v", st.RetryAfter)
	}
	if !st.CaptchaRequired {
		t.Fatal("the owner must solve a captcha instead")
	}
}

func TestLoginThrottle_SuccessClearsTheDelayButNotTheCaptcha(t *testing.T) {
	th, _ := newTestThrottle()
	for i := 0; i < 4; i++ {
		fail(t, th, "a@example.com", ipA)
	}
	if err := th.RecordSuccess(context.Background(), "a@example.com", ipA); err != nil {
		t.Fatal(err)
	}
	st := check(t, th, "a@example.com", ipA)
	if st.RetryAfter != 0 {
		t.Fatalf("a successful login must clear its (email, IP) delay, got %v", st.RetryAfter)
	}
	if !st.CaptchaRequired {
		t.Fatal("a success must not clear the per-email captcha requirement")
	}
}
