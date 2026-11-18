// Package ratelimit keeps sliding-window event logs shared by every API instance,
// so limits survive redeploys and apply across replicas.
package ratelimit

import (
	"context"
	"time"
)

// Store records timestamped events per key and answers sliding-window questions.
// An event older than the window (age >= window) no longer counts.
type Store interface {
	// Hit records an event if fewer than limit events are in the window. When it
	// refuses, retryAfter is how long until enough old events leave the window.
	Hit(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
	// Record adds an event unconditionally.
	Record(ctx context.Context, key string, window time.Duration) error
	// Events returns the times of the events still in the window, oldest first.
	Events(ctx context.Context, key string, window time.Duration) ([]time.Time, error)
	// Clear removes every event under key.
	Clear(ctx context.Context, key string) error
	// Ping reports whether the store is reachable.
	Ping(ctx context.Context) error
}
