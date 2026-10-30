package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// clock is a settable time source shared by a store under test.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)} }

// stores returns every Store implementation to run the conformance tests against.
// The redis subtests skip, visibly, unless TEST_REDIS_URL points at a throwaway server.
func stores(t *testing.T) map[string]func(*testing.T, *clock) Store {
	out := map[string]func(*testing.T, *clock) Store{
		"memory": func(_ *testing.T, c *clock) Store { return NewMemoryStoreWithClock(c.now) },
	}
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		out["redis"] = func(t *testing.T, _ *clock) Store {
			t.Skip("TEST_REDIS_URL not set: Redis store not tested (start docker-compose.test.yml or run make test)")
			return nil
		}
		return out
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("TEST_REDIS_URL: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	out["redis"] = func(_ *testing.T, c *clock) Store {
		buf := make([]byte, 6)
		_, _ = rand.Read(buf)
		s := NewRedisStore(client, "test:"+hex.EncodeToString(buf)+":")
		s.now = c.now
		return s
	}
	return out
}

func TestStore_HitAllowsLimitPerSlidingWindow(t *testing.T) {
	ctx := context.Background()
	for name, mk := range stores(t) {
		t.Run(name, func(t *testing.T) {
			clk := newClock()
			s := mk(t, clk)
			for i := 0; i < 3; i++ {
				ok, _, err := s.Hit(ctx, "k", 3, time.Minute)
				if err != nil || !ok {
					t.Fatalf("hit %d: ok=%v err=%v", i+1, ok, err)
				}
				clk.advance(10 * time.Second) // hits at :00, :10, :20
			}
			ok, retry, err := s.Hit(ctx, "k", 3, time.Minute)
			if err != nil || ok {
				t.Fatalf("4th hit within the window must be refused, ok=%v err=%v", ok, err)
			}
			if retry != 30*time.Second { // now :30, oldest :00 leaves at 1:00
				t.Fatalf("retry-after must be when the oldest hit leaves the window, got %v", retry)
			}
			clk.advance(30 * time.Second) // 1:00: the :00 hit is exactly one window old
			if ok, _, _ := s.Hit(ctx, "k", 3, time.Minute); !ok {
				t.Fatal("a hit must be allowed once the oldest one has left the window")
			}
			if ok, _, _ := s.Hit(ctx, "other", 3, time.Minute); !ok {
				t.Fatal("keys are independent")
			}
		})
	}
}

func TestStore_RefusedHitsAreNotCounted(t *testing.T) {
	ctx := context.Background()
	for name, mk := range stores(t) {
		t.Run(name, func(t *testing.T) {
			clk := newClock()
			s := mk(t, clk)
			s.Hit(ctx, "k", 1, time.Minute)
			for i := 0; i < 5; i++ {
				clk.advance(time.Second)
				s.Hit(ctx, "k", 1, time.Minute)
			}
			clk.advance(55 * time.Second) // exactly one window after the only counted hit
			if ok, _, _ := s.Hit(ctx, "k", 1, time.Minute); !ok {
				t.Fatal("refused hits must not extend the window")
			}
		})
	}
}

func TestStore_RecordEventsAndClear(t *testing.T) {
	ctx := context.Background()
	for name, mk := range stores(t) {
		t.Run(name, func(t *testing.T) {
			clk := newClock()
			s := mk(t, clk)
			start := clk.now()
			for i := 0; i < 3; i++ {
				if err := s.Record(ctx, "f", 15*time.Minute); err != nil {
					t.Fatal(err)
				}
				clk.advance(5 * time.Minute) // events at 0, 5, 10 min
			}
			ev, err := s.Events(ctx, "f", 15*time.Minute) // now 15 min: the first event leaves
			if err != nil {
				t.Fatal(err)
			}
			if len(ev) != 2 || !ev[0].Equal(start.Add(5*time.Minute)) || !ev[1].Equal(start.Add(10*time.Minute)) {
				t.Fatalf("expected the events at +5m and +10m, oldest first; got %v", ev)
			}
			if err := s.Clear(ctx, "f"); err != nil {
				t.Fatal(err)
			}
			if ev, _ := s.Events(ctx, "f", 15*time.Minute); len(ev) != 0 {
				t.Fatalf("clear must remove every event, got %v", ev)
			}
		})
	}
}

func TestRedisStore_KeysExpireSoMemoryIsBounded(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, _ := redis.ParseURL(url)
	client := redis.NewClient(opts)
	defer client.Close()
	s := NewRedisStore(client, "test:ttl:")
	ctx := context.Background()
	s.Hit(ctx, "a", 5, time.Minute)
	s.Record(ctx, "b", 15*time.Minute)
	for key, max := range map[string]time.Duration{"test:ttl:a": time.Minute, "test:ttl:b": 15 * time.Minute} {
		ttl, err := client.PTTL(ctx, key).Result()
		if err != nil || ttl <= 0 || ttl > max {
			t.Fatalf("%s must expire within its window, ttl=%v err=%v", key, ttl, err)
		}
	}
	client.Del(ctx, "test:ttl:a", "test:ttl:b")
}
