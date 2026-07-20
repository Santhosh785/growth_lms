package httpserver_test

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/config"
	"growth-lms/internal/httpserver"
	"growth-lms/internal/logging"
)

// newTestEngine builds a full httpserver.New() engine against a real,
// migrated Postgres (adminURL, from testutil.RequireDB), for tests that
// need to exercise routing/middleware/RLS together over real HTTP rather
// than unit-testing a single layer in isolation. Redis is deliberately
// unreachable (127.0.0.1:63799) — none of the course-domain routes this
// package tests touch Redis directly.
func newTestEngine(t *testing.T, adminURL string) (*gin.Engine, *pgxpool.Pool) {
	t.Helper()

	dbPool, err := pgxpool.New(context.Background(), adminURL)
	require.NoError(t, err)
	t.Cleanup(dbPool.Close)

	cfg := &config.Config{
		Env:      config.EnvDevelopment,
		Port:     8080,
		BaseURL:  "http://localhost:8080",
		Database: config.DatabaseConfig{URL: adminURL},
		Supabase: config.SupabaseConfig{
			URL: "https://example.supabase.co", AnonKey: "anon", ServiceRoleKey: "service",
			StorageBucket: "bucket", JWTSecret: testJWTSecret,
		},
		Redis:    config.RedisConfig{URL: "redis://127.0.0.1:63799"},
		BunnyNet: config.BunnyNetConfig{APIKey: "bunny-key", StorageZone: "zone", CDNURL: "https://cdn.example.com", WebhookSecret: "webhook-secret"},
	}
	logger := logging.New(true, 0)
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:63799"})
	t.Cleanup(func() { _ = redisClient.Close() })

	return httpserver.New(cfg, logger, dbPool, redisClient), dbPool
}
