package services

import (
	"strings"
	"sync"
	"time"
)

// Login throttling policy: 5 failures within 15 minutes lock the email for 15 minutes.
const (
	loginMaxFailures   = 5
	loginFailureWindow = 15 * time.Minute
	loginLockDuration  = 15 * time.Minute
)

type loginAttempts struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

// LoginThrottle tracks failed logins per email in memory. It counts attempts for
// any email, existing or not, so the lockout does not reveal which accounts exist.
// State is per process and resets on restart.
type LoginThrottle struct {
	mu         sync.Mutex
	entries    map[string]*loginAttempts
	maxEntries int
	now        func() time.Time
}

// NewLoginThrottle creates a throttle that tracks at most maxEntries emails.
func NewLoginThrottle(maxEntries int) *LoginThrottle {
	return &LoginThrottle{
		entries:    make(map[string]*loginAttempts),
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Locked reports whether the email is locked and how long until it unlocks.
func (t *LoginThrottle) Locked(email string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, ok := t.entries[normalizeEmail(email)]
	if !ok {
		return false, 0
	}
	if remaining := a.lockedUntil.Sub(t.now()); remaining > 0 {
		return true, remaining
	}
	return false, 0
}

// RecordFailure counts a failed login and locks the email once the limit is reached.
func (t *LoginThrottle) RecordFailure(email string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := normalizeEmail(email)
	now := t.now()

	a, ok := t.entries[key]
	if !ok {
		if !t.makeRoom(now) {
			return
		}
		a = &loginAttempts{}
		t.entries[key] = a
	}
	if now.Sub(a.windowStart) > loginFailureWindow || (!a.lockedUntil.IsZero() && !now.Before(a.lockedUntil)) {
		*a = loginAttempts{windowStart: now}
	}
	a.failures++
	if a.failures >= loginMaxFailures {
		a.lockedUntil = now.Add(loginLockDuration)
	}
}

// Reset clears the failure count after a successful login.
func (t *LoginThrottle) Reset(email string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, normalizeEmail(email))
}

// makeRoom frees space for a new entry: expired entries go first, then the oldest
// unlocked one. Active locks are never evicted. Must be called with mu held.
func (t *LoginThrottle) makeRoom(now time.Time) bool {
	if len(t.entries) < t.maxEntries {
		return true
	}
	var oldestKey string
	var oldest time.Time
	for k, a := range t.entries {
		locked := now.Before(a.lockedUntil)
		if !locked && now.Sub(a.windowStart) > loginFailureWindow {
			delete(t.entries, k)
			continue
		}
		if !locked && (oldestKey == "" || a.windowStart.Before(oldest)) {
			oldestKey, oldest = k, a.windowStart
		}
	}
	if len(t.entries) < t.maxEntries {
		return true
	}
	if oldestKey == "" {
		return false
	}
	delete(t.entries, oldestKey)
	return true
}

func (t *LoginThrottle) size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}
