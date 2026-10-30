package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"discipline-backend/internal/ratelimit"
)

// Login throttling policy. Failures are counted in sliding 15-minute windows:
//   - per (email, IP): failures 1-3 cost nothing; after failure n >= 4 the next
//     attempt waits 2^(n-3) seconds (2, 4, 8 ...), at most 15 minutes;
//   - per IP: 30 failures, whatever the emails, block that IP;
//   - per email: from 3 failures (any IPs) every attempt needs a captcha.
//
// Nothing locks an account: an attacker can only slow their own (email, IP) pair
// and IP, and make the owner solve a captcha.
const (
	loginFailureWindow = 15 * time.Minute
	loginFreeFailures  = 3
	loginMaxDelay      = 15 * time.Minute
	loginIPMaxFailures = 30
	loginCaptchaAfter  = 3
)

// LoginState is what the throttle requires before a login attempt may proceed.
type LoginState struct {
	// RetryAfter > 0 means refuse the attempt (429) for that long.
	RetryAfter time.Duration
	// CaptchaRequired means the attempt needs a valid captcha token.
	CaptchaRequired bool
}

// LoginThrottle tracks failed logins in a shared store. It counts attempts for
// any email, existing or not, so its answers do not reveal which accounts exist.
type LoginThrottle struct {
	store ratelimit.Store
	now   func() time.Time
}

// NewLoginThrottle creates a throttle backed by store.
func NewLoginThrottle(store ratelimit.Store) *LoginThrottle {
	return &LoginThrottle{store: store, now: time.Now}
}

// emailID normalizes an email and hashes it, so the store never holds addresses.
func emailID(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func emailIPKey(email, ip string) string { return "login:fail:emailip:" + emailID(email) + ":" + ip }
func ipKey(ip string) string             { return "login:fail:ip:" + ip }
func emailKey(email string) string       { return "login:fail:email:" + emailID(email) }

// delayAfter is the wait imposed after the n-th failure of one (email, IP) pair.
func delayAfter(n int) time.Duration {
	if n <= loginFreeFailures {
		return 0
	}
	shift := n - loginFreeFailures
	if shift >= 30 {
		return loginMaxDelay
	}
	return min(time.Duration(1<<shift)*time.Second, loginMaxDelay)
}

// Check reports whether an attempt for email from ip must wait or needs a captcha.
func (t *LoginThrottle) Check(ctx context.Context, email, ip string) (LoginState, error) {
	var st LoginState
	now := t.now()

	pair, err := t.store.Events(ctx, emailIPKey(email, ip), loginFailureWindow)
	if err != nil {
		return st, err
	}
	if n := len(pair); n > 0 {
		st.RetryAfter = max(0, pair[n-1].Add(delayAfter(n)).Sub(now))
	}

	byIP, err := t.store.Events(ctx, ipKey(ip), loginFailureWindow)
	if err != nil {
		return st, err
	}
	if n := len(byIP); n >= loginIPMaxFailures {
		// Blocked until enough failures leave the window to drop below the cap.
		st.RetryAfter = max(st.RetryAfter, byIP[n-loginIPMaxFailures].Add(loginFailureWindow).Sub(now))
	}

	byEmail, err := t.store.Events(ctx, emailKey(email), loginFailureWindow)
	if err != nil {
		return st, err
	}
	st.CaptchaRequired = len(byEmail) >= loginCaptchaAfter
	return st, nil
}

// RecordFailure counts a failed login and reports whether the email now needs a captcha.
func (t *LoginThrottle) RecordFailure(ctx context.Context, email, ip string) (bool, error) {
	for _, key := range []string{emailIPKey(email, ip), ipKey(ip), emailKey(email)} {
		if err := t.store.Record(ctx, key, loginFailureWindow); err != nil {
			return false, err
		}
	}
	byEmail, err := t.store.Events(ctx, emailKey(email), loginFailureWindow)
	if err != nil {
		return false, err
	}
	return len(byEmail) >= loginCaptchaAfter, nil
}

// RecordSuccess clears the (email, IP) delay. The per-IP and per-email counts
// stay, so a success cannot be used to reset an attack's budget.
func (t *LoginThrottle) RecordSuccess(ctx context.Context, email, ip string) error {
	return t.store.Clear(ctx, emailIPKey(email, ip))
}
