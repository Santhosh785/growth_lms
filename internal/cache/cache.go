// Package cache is a thin JSON-over-Redis cache for hot, low-churn read paths
// (plan.md Task 11 release gate: caching). It is intentionally small: a
// GetOrLoad read-through and an Invalidate, both fail-open so a Redis outage
// degrades to uncached reads rather than an outage of its own.
package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache namespaces a set of keys under a prefix on a shared Redis client. A
// nil *Cache (or one built from a nil client) is valid and disables caching:
// GetOrLoad always calls the loader and Invalidate is a no-op, so callers need
// no nil checks.
type Cache struct {
	rdb    *redis.Client
	prefix string
}

// New returns a Cache writing keys as prefix+key on rdb. Passing a nil client
// yields a disabled cache.
func New(rdb *redis.Client, prefix string) *Cache {
	return &Cache{rdb: rdb, prefix: prefix}
}

func (c *Cache) enabled() bool { return c != nil && c.rdb != nil }

// GetOrLoad returns the cached value for key if present and decodable;
// otherwise it calls load, stores the result for ttl (best-effort), and
// returns it. Loader errors are propagated and never cached. Any Redis or
// decode error falls through to load, so the cache can only speed reads up,
// never break them.
func GetOrLoad[T any](ctx context.Context, c *Cache, key string, ttl time.Duration, load func() (T, error)) (T, error) {
	if !c.enabled() {
		return load()
	}
	full := c.prefix + key

	if data, err := c.rdb.Get(ctx, full).Bytes(); err == nil {
		var v T
		if json.Unmarshal(data, &v) == nil {
			return v, nil
		}
	}

	v, err := load()
	if err != nil {
		return v, err
	}
	if data, err := json.Marshal(v); err == nil {
		_ = c.rdb.Set(ctx, full, data, ttl).Err()
	}
	return v, nil
}

// Invalidate removes key (best-effort). Call it when the underlying data
// changes so a stale entry cannot outlive its TTL.
func (c *Cache) Invalidate(ctx context.Context, key string) {
	if !c.enabled() {
		return
	}
	_ = c.rdb.Del(ctx, c.prefix+key).Err()
}
