package ratelimit

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is a single-process Store for tests. It is not shared between
// instances and forgets everything on restart; production uses RedisStore.
type MemoryStore struct {
	mu     sync.Mutex
	events map[string][]time.Time
	now    func() time.Time
}

// NewMemoryStore returns an empty MemoryStore on the wall clock.
func NewMemoryStore() *MemoryStore { return NewMemoryStoreWithClock(time.Now) }

// NewMemoryStoreWithClock returns an empty MemoryStore that reads time from now.
func NewMemoryStoreWithClock(now func() time.Time) *MemoryStore {
	return &MemoryStore{events: make(map[string][]time.Time), now: now}
}

// pruneLocked drops events that left the window and returns the rest. Must be called with mu held.
func (s *MemoryStore) pruneLocked(key string, window time.Duration, now time.Time) []time.Time {
	ev := s.events[key]
	i := 0
	for i < len(ev) && now.Sub(ev[i]) >= window {
		i++
	}
	ev = ev[i:]
	if len(ev) == 0 {
		delete(s.events, key)
	} else {
		s.events[key] = ev
	}
	return ev
}

func (s *MemoryStore) Hit(_ context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	ev := s.pruneLocked(key, window, now)
	if len(ev) < limit {
		s.events[key] = append(ev, now)
		return true, 0, nil
	}
	return false, ev[len(ev)-limit].Add(window).Sub(now), nil
}

func (s *MemoryStore) Record(_ context.Context, key string, window time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.events[key] = append(s.pruneLocked(key, window, now), now)
	return nil
}

func (s *MemoryStore) Events(_ context.Context, key string, window time.Duration) ([]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.pruneLocked(key, window, s.now())...), nil
}

func (s *MemoryStore) Clear(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.events, key)
	return nil
}

func (s *MemoryStore) Ping(context.Context) error { return nil }
