package httpserver_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/config"
	"growth-lms/internal/httpserver"
	"growth-lms/internal/logging"
)

// The Supabase CLI (`supabase start`) issues these exact ANON_KEY/
// SERVICE_ROLE_KEY values for every local project by default — they are
// fixed local-dev demo credentials baked into the CLI's default config,
// not a secret specific to this repository, so hardcoding them here is
// safe. Task 5 Stage 6 (certificate issuance) is the first code path
// under test that makes a real Supabase Storage call
// (media.StorageClient.UploadServerSide), so newTestEngine now needs a
// reachable Storage backend rather than the placeholder
// "https://example.supabase.co" used before Stage 6 — every test using
// this engine already requires `supabase start` to be running locally
// (LMS_TEST_DATABASE_URL points at its Postgres), so its Storage API is
// available on the same host.
const (
	localSupabaseURL            = "http://127.0.0.1:54321"
	localSupabaseServiceRoleKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSIsImV4cCI6MTk4MzgxMjk5Nn0.EGIM96RAZx35lJzdJsyH-qQwv8Hdp7fsn3W0YpN81IU"
	localSupabaseAnonKey        = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6ImFub24iLCJleHAiOjE5ODM4MTI5OTZ9.CRXP1A7WOeoJeXxjNni43kdQwgnWNReilDMblYTn_I0"
	testStorageBucket           = "test-bucket"
)

// ensureTestStorageBucket idempotently creates testStorageBucket against
// the local Supabase Storage API, ignoring a "bucket already exists"
// response — tests share this bucket, and t.Parallel/repeated runs must
// not fail on the second creation attempt.
func ensureTestStorageBucket(t *testing.T) {
	t.Helper()
	body := []byte(`{"id":"` + testStorageBucket + `","name":"` + testStorageBucket + `","public":false}`)
	req, err := http.NewRequest(http.MethodPost, localSupabaseURL+"/storage/v1/bucket", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+localSupabaseServiceRoleKey)
	req.Header.Set("apikey", localSupabaseServiceRoleKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("local Supabase Storage not reachable at %s: %v", localSupabaseURL, err)
	}
	defer resp.Body.Close()
	// 200/201 created, 400/409 already exists — anything else is a real
	// problem the test should fail loudly on.
	if resp.StatusCode >= 500 {
		t.Fatalf("failed to ensure test storage bucket: status %d", resp.StatusCode)
	}
}

// newTestEngine builds a full httpserver.New() engine against a real,
// migrated Postgres (adminURL, from testutil.RequireDB), for tests that
// need to exercise routing/middleware/RLS together over real HTTP rather
// than unit-testing a single layer in isolation. Redis is deliberately
// unreachable (127.0.0.1:63799) — none of the course-domain routes this
// package tests touch Redis directly.
func newTestEngine(t *testing.T, adminURL string) (*gin.Engine, *pgxpool.Pool) {
	t.Helper()

	ensureTestStorageBucket(t)

	dbPool, err := pgxpool.New(context.Background(), adminURL)
	require.NoError(t, err)
	t.Cleanup(dbPool.Close)

	cfg := &config.Config{
		Env:      config.EnvDevelopment,
		Port:     8080,
		BaseURL:  "http://localhost:8080",
		Database: config.DatabaseConfig{URL: adminURL},
		Supabase: config.SupabaseConfig{
			URL: localSupabaseURL, AnonKey: localSupabaseAnonKey, ServiceRoleKey: localSupabaseServiceRoleKey,
			StorageBucket: testStorageBucket, JWTSecret: testJWTSecret,
		},
		Redis:    config.RedisConfig{URL: "redis://127.0.0.1:63799"},
		BunnyNet: config.BunnyNetConfig{APIKey: "bunny-key", StorageZone: "zone", CDNURL: "https://cdn.example.com", WebhookSecret: "webhook-secret"},
	}
	logger := logging.New(true, 0)
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:63799"})
	t.Cleanup(func() { _ = redisClient.Close() })

	return httpserver.New(cfg, logger, dbPool, redisClient), dbPool
}
