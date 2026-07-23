package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb, "test:"), mr
}

func TestGetOrLoad_CachesAfterMiss(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	calls := 0
	load := func() (int, error) {
		calls++
		return 42, nil
	}

	v, err := GetOrLoad(ctx, c, "k", time.Minute, load)
	if err != nil || v != 42 {
		t.Fatalf("first load = %d, %v", v, err)
	}
	v, err = GetOrLoad(ctx, c, "k", time.Minute, load)
	if err != nil || v != 42 {
		t.Fatalf("second load = %d, %v", v, err)
	}
	if calls != 1 {
		t.Fatalf("loader called %d times, want 1 (second read should hit cache)", calls)
	}
}

func TestGetOrLoad_DoesNotCacheErrors(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	wantErr := errors.New("boom")
	calls := 0
	load := func() (string, error) {
		calls++
		return "", wantErr
	}

	if _, err := GetOrLoad(ctx, c, "k", time.Minute, load); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, err := GetOrLoad(ctx, c, "k", time.Minute, load); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls != 2 {
		t.Fatalf("loader called %d times, want 2 (errors must not be cached)", calls)
	}
}

func TestInvalidate(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	calls := 0
	load := func() (int, error) {
		calls++
		return calls, nil
	}

	if v, _ := GetOrLoad(ctx, c, "k", time.Minute, load); v != 1 {
		t.Fatalf("got %d, want 1", v)
	}
	c.Invalidate(ctx, "k")
	if v, _ := GetOrLoad(ctx, c, "k", time.Minute, load); v != 2 {
		t.Fatalf("got %d, want 2 after invalidate", v)
	}
}

func TestNilCache_AlwaysLoads(t *testing.T) {
	ctx := context.Background()
	var c *Cache // nil, e.g. Redis unavailable at startup

	calls := 0
	load := func() (int, error) {
		calls++
		return 7, nil
	}
	for i := 0; i < 3; i++ {
		if v, err := GetOrLoad(ctx, c, "k", time.Minute, load); err != nil || v != 7 {
			t.Fatalf("nil-cache load = %d, %v", v, err)
		}
	}
	if calls != 3 {
		t.Fatalf("loader called %d times, want 3 (nil cache disables caching)", calls)
	}
	c.Invalidate(ctx, "k") // must not panic
}
