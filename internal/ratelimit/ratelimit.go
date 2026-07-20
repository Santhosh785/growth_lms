// Package ratelimit provides a generic, Redis-backed rate limiter usable as
// middleware on any route. This package only builds the primitive; it is
// not wired to any specific route here (see Task 3 for auth-route limits).
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter enforces a fixed number of allowed events per key within a
// sliding time window, backed by Redis so limits are shared across all
// instances of the application.
type Limiter struct {
	redis  *redis.Client
	limit  int64
	window time.Duration
	prefix string
}

// New builds a Limiter allowing `limit` events per `window`, per key.
// `prefix` namespaces the Redis keys used (e.g. "ratelimit:login").
func New(client *redis.Client, prefix string, limit int64, window time.Duration) *Limiter {
	return &Limiter{redis: client, limit: limit, window: window, prefix: prefix}
}

// Allow reports whether the event identified by key is permitted under the
// configured limit, incrementing the counter as a side effect. It uses a
// fixed-window counter: the first call for a key in a window sets the
// expiry; subsequent calls increment atomically.
func (l *Limiter) Allow(ctx context.Context, key string) (bool, error) {
	redisKey := fmt.Sprintf("%s:%s", l.prefix, key)

	count, err := l.redis.Incr(ctx, redisKey).Result()
	if err != nil {
		return false, fmt.Errorf("ratelimit: incr failed: %w", err)
	}
	if count == 1 {
		if err := l.redis.Expire(ctx, redisKey, l.window).Err(); err != nil {
			return false, fmt.Errorf("ratelimit: expire failed: %w", err)
		}
	}

	return count <= l.limit, nil
}
