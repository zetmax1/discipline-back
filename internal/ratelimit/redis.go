package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore keeps each key's events in a sorted set scored by Unix milliseconds.
// Every write refreshes the key's TTL to the window, so idle keys disappear.
type RedisStore struct {
	client redis.UniversalClient
	prefix string
	now    func() time.Time
}

// NewRedisStore stores keys under prefix (e.g. "rl:").
func NewRedisStore(client redis.UniversalClient, prefix string) *RedisStore {
	return &RedisStore{client: client, prefix: prefix, now: time.Now}
}

// hitScript prunes events that left the window, then adds one if the count is
// below the limit. Otherwise it returns the score of the event whose departure
// frees a slot. KEYS[1]=key, ARGV = now_ms, window_ms, limit, member.
var hitScript = redis.NewScript(`
local now, window, limit = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
local n = redis.call('ZCARD', KEYS[1])
if n < limit then
  redis.call('ZADD', KEYS[1], now, ARGV[4])
  redis.call('PEXPIRE', KEYS[1], window)
  return {1, 0}
end
local freeing = redis.call('ZRANGE', KEYS[1], n - limit, n - limit, 'WITHSCORES')
return {0, tonumber(freeing[2]) + window - now}
`)

func (s *RedisStore) Hit(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	now := s.now().UnixMilli()
	res, err := hitScript.Run(ctx, s.client, []string{s.prefix + key},
		now, window.Milliseconds(), limit, member(now)).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	return res[0] == 1, time.Duration(res[1]) * time.Millisecond, nil
}

func (s *RedisStore) Record(ctx context.Context, key string, window time.Duration) error {
	now := s.now().UnixMilli()
	k := s.prefix + key
	_, err := s.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.ZRemRangeByScore(ctx, k, "-inf", strconv.FormatInt(now-window.Milliseconds(), 10))
		p.ZAdd(ctx, k, redis.Z{Score: float64(now), Member: member(now)})
		p.PExpire(ctx, k, window)
		return nil
	})
	return err
}

func (s *RedisStore) Events(ctx context.Context, key string, window time.Duration) ([]time.Time, error) {
	now := s.now().UnixMilli()
	k := s.prefix + key
	var rng *redis.ZSliceCmd
	_, err := s.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.ZRemRangeByScore(ctx, k, "-inf", strconv.FormatInt(now-window.Milliseconds(), 10))
		rng = p.ZRangeWithScores(ctx, k, 0, -1)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, len(rng.Val()))
	for _, z := range rng.Val() {
		out = append(out, time.UnixMilli(int64(z.Score)).UTC())
	}
	return out, nil
}

func (s *RedisStore) Clear(ctx context.Context, key string) error {
	return s.client.Del(ctx, s.prefix+key).Err()
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// member makes each event unique within its sorted set.
func member(nowMs int64) string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return strconv.FormatInt(nowMs, 10) + "-" + hex.EncodeToString(buf)
}
