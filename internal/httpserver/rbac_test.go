package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/config"
	"growth-lms/internal/httpserver"
	"growth-lms/internal/logging"
	"growth-lms/internal/testutil"
)

const testJWTSecret = "test-only-hs256-secret-do-not-use-in-prod"

func mintToken(t *testing.T, userID, email string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":   userID,
		"email": email,
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testJWTSecret))
	require.NoError(t, err)
	return signed
}

func seedAuthUser(t *testing.T, pool *pgxpool.Pool, id, email string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1, $2)`, id, email)
	require.NoError(t, err)
}

func seedMembership(t *testing.T, pool *pgxpool.Pool, userID, orgSlug, role string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO memberships (user_id, org_id, role)
		SELECT $1, id, $3 FROM organizations WHERE slug = $2
	`, userID, orgSlug, role)
	require.NoError(t, err)
}

// TestRBAC_InvitationCreation exercises the permission matrix end to end
// over real HTTP against a real, migrated, RLS-enforcing Postgres: an
// org owner can invite a new member, and a learner in the same org
// cannot — proving RequireRole is actually wired to the routes, not just
// unit-tested in isolation. The Postgres connection used here is a
// superuser (same one testutil.DB migrates with), matching how the real
// app connects via LMS_DATABASE_URL in production/Supabase — RBAC is
// enforced by RequireRole in this test, and separately proven at the DB
// layer (as app_test, a non-superuser) by the RLS isolation tests in
// internal/models.
func TestRBAC_InvitationCreation(t *testing.T) {
	adminURL := testutil.RequireDB(t)
	testutil.DB(t) // ensures migrations have run

	dbPool, err := pgxpool.New(context.Background(), adminURL)
	require.NoError(t, err)
	t.Cleanup(dbPool.Close)

	cfg := &config.Config{
		Env:      config.EnvDevelopment,
		Port:     8080,
		BaseURL:  "http://localhost:8080",
		Database: config.DatabaseConfig{URL: adminURL},
		Supabase: config.SupabaseConfig{
			URL:            "https://example.supabase.co",
			AnonKey:        "test-anon-key",
			ServiceRoleKey: "test-service-role-key",
			StorageBucket:  "test-bucket",
			JWTSecret:      testJWTSecret,
		},
		Redis: config.RedisConfig{URL: "redis://127.0.0.1:63799"},
	}

	logger := logging.New(true, 0)
	// Unreachable on purpose: none of the routes exercised by this test
	// touch Redis (rate limiting only guards /api/auth/*).
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:63799"})
	t.Cleanup(func() { _ = redisClient.Close() })

	engine := httpserver.New(cfg, logger, dbPool, redisClient)

	ownerID := uuid.NewString()
	learnerID := uuid.NewString()
	seedAuthUser(t, dbPool, ownerID, "owner-"+ownerID+"@example.com")
	seedAuthUser(t, dbPool, learnerID, "learner-"+learnerID+"@example.com")

	slug := "rbac-test-" + uuid.NewString()
	ownerToken := mintToken(t, ownerID, "owner@example.com")

	createOrgBody, _ := json.Marshal(map[string]string{"name": "RBAC Test Org", "slug": slug})
	req := httptest.NewRequest(http.MethodPost, "/api/orgs", bytes.NewReader(createOrgBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	seedMembership(t, dbPool, learnerID, slug, "learner")

	inviteBody, _ := json.Marshal(map[string]string{"email": "invitee@example.com", "role": "learner"})

	// Owner can invite.
	req = httptest.NewRequest(http.MethodPost, "/api/orgs/"+slug+"/invitations", bytes.NewReader(inviteBody))
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// Learner cannot invite.
	learnerToken := mintToken(t, learnerID, "learner@example.com")
	req = httptest.NewRequest(http.MethodPost, "/api/orgs/"+slug+"/invitations", bytes.NewReader(inviteBody))
	req.Header.Set("Authorization", "Bearer "+learnerToken)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}
