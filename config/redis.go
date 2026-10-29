package config

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"discipline-backend/internal/ratelimit"
)

// ConnectRedis connects to REDIS_URL and exits if it is unreachable.
func ConnectRedis(cfg *Config) *ratelimit.RedisStore {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("Invalid REDIS_URL: %v", err)
	}
	// Limiter calls sit on every request: fail fast rather than hang.
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = time.Second
	opts.WriteTimeout = time.Second
	store := ratelimit.NewRedisStore(redis.NewClient(opts), "discipline:")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Redis connected successfully")
	return store
}
