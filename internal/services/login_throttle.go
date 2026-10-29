package services

import (
	"context"
	"strings"
	"time"

	"discipline-backend/internal/ratelimit"
)

// Login throttling policy: 5 failures within a sliding 15 minutes lock the email
// until enough of them are older than 15 minutes.
const (
	loginMaxFailures   = 5
	loginFailureWindow = 15 * time.Minute
)

// LoginThrottle tracks failed logins per email in a shared store. It counts
// attempts for any email, existing or not, so the lockout does not reveal which
// accounts exist.
type LoginThrottle struct {
	store ratelimit.Store
	now   func() time.Time
}

// NewLoginThrottle creates a throttle backed by store.
func NewLoginThrottle(store ratelimit.Store) *LoginThrottle {
	return &LoginThrottle{store: store, now: time.Now}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func failureKey(email string) string { return "login:fail:email:" + normalizeEmail(email) }

// Locked reports whether the email is locked and how long until it unlocks.
func (t *LoginThrottle) Locked(ctx context.Context, email string) (bool, time.Duration, error) {
	ev, err := t.store.Events(ctx, failureKey(email), loginFailureWindow)
	if err != nil || len(ev) < loginMaxFailures {
		return false, 0, err
	}
	// Unlocks when the failure that keeps the count at the limit leaves the window.
	freeing := ev[len(ev)-loginMaxFailures]
	return true, freeing.Add(loginFailureWindow).Sub(t.now()), nil
}

// RecordFailure counts a failed login.
func (t *LoginThrottle) RecordFailure(ctx context.Context, email string) error {
	return t.store.Record(ctx, failureKey(email), loginFailureWindow)
}

// Reset clears the failure count after a successful login.
func (t *LoginThrottle) Reset(ctx context.Context, email string) error {
	return t.store.Clear(ctx, failureKey(email))
}
