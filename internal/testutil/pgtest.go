// Package testutil provides a real, migrated Postgres database for
// integration tests that need actual Row-Level Security enforcement —
// something no mock or in-memory substitute can prove. Tests using this
// package are skipped automatically when LMS_TEST_DATABASE_URL isn't set,
// so `go test ./...` still passes on a machine with no Postgres; CI sets
// the variable and always runs them (see .github/workflows/ci.yml).
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// appTestRole is a non-superuser, non-BYPASSRLS role every RLS test
// connects as. The CI/admin connection (LMS_TEST_DATABASE_URL) is a
// superuser and would silently bypass every RLS policy, making the tests
// meaningless — see the "RLS caveat" note in
// db/migrations/000002_auth_tenancy.up.sql.
const (
	appTestRole     = "app_test"
	appTestPassword = "app_test_password"
)

// migrationsDir resolves db/migrations relative to this source file, so
// tests work regardless of the working directory `go test` was invoked
// from.
func migrationsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "db", "migrations")
}

// RequireDB skips the calling test unless LMS_TEST_DATABASE_URL is set.
// Call it first in any test that needs a real Postgres.
func RequireDB(t *testing.T) string {
	t.Helper()
	adminURL := envOrSkip(t, "LMS_TEST_DATABASE_URL")
	return adminURL
}

func envOrSkip(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s not set; skipping test that requires a real Postgres", name)
	}
	return v
}

// DB runs all migrations against the database identified by
// LMS_TEST_DATABASE_URL (an admin/superuser connection), ensures the
// app_test role exists with the privileges RLS tests need, and returns a
// pool connected AS app_test — never as the admin role, so RLS policies
// actually apply to every query the test issues.
func DB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL := RequireDB(t)

	// Different Go packages run as separate test binaries and, under `go
	// test ./...`, execute concurrently — all sharing the one
	// LMS_TEST_DATABASE_URL. Without serializing, two binaries' concurrent
	// `GRANT ... ON ALL TABLES` (or CREATE ROLE) statements race and
	// Postgres rejects one with "tuple concurrently updated". A session
	// advisory lock, held for the duration of migrate+role setup, makes
	// every test binary wait its turn instead of racing.
	unlock := acquireSetupLock(t, adminURL)
	defer unlock()

	runMigrations(t, adminURL)
	ensureAppTestRole(t, adminURL)

	appURL := withCredentials(t, adminURL, appTestRole, appTestPassword)
	pool, err := pgxpool.New(context.Background(), appURL)
	if err != nil {
		t.Fatalf("testutil: connect as %s: %v", appTestRole, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// AdminDB returns a pool connected with full (superuser/owner)
// privileges, for seeding test fixtures that must bypass RLS (e.g.
// inserting rows for two different organizations before testing that
// neither can see the other's data as app_test).
func AdminDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL := RequireDB(t)
	pool, err := pgxpool.New(context.Background(), adminURL)
	if err != nil {
		t.Fatalf("testutil: connect as admin: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// setupAdvisoryLockKey is an arbitrary constant identifying this
// package's cross-process setup lock; any int64 works as long as it's
// unlikely to collide with locks taken elsewhere in the schema.
const setupAdvisoryLockKey = 727384001

// acquireSetupLock blocks until it holds a Postgres session advisory
// lock, and returns a function that releases it. The lock is held on a
// dedicated connection for the caller to close/release explicitly,
// separate from whatever connection runs the migrations/GRANTs.
func acquireSetupLock(t *testing.T, adminURL string) func() {
	t.Helper()
	db, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatalf("testutil: open setup-lock connection: %v", err)
	}
	// A single *sql.DB can hand out more than one pooled connection, which
	// would defeat a *session* advisory lock (each new connection has no
	// memory of a lock taken on a different one). Capping the pool to one
	// connection guarantees the lock and its later unlock happen on the
	// same session.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`SELECT pg_advisory_lock($1)`, setupAdvisoryLockKey); err != nil {
		db.Close()
		t.Fatalf("testutil: acquire setup lock: %v", err)
	}

	return func() {
		_, _ = db.Exec(`SELECT pg_advisory_unlock($1)`, setupAdvisoryLockKey)
		db.Close()
	}
}

func runMigrations(t *testing.T, adminURL string) {
	t.Helper()
	migrateURL := toPgx5URL(t, adminURL)

	m, err := migrate.New("file://"+migrationsDir(), migrateURL)
	if err != nil {
		t.Fatalf("testutil: init migrator: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("testutil: run migrations: %v", err)
	}
}

// toPgx5URL rewrites a postgres:// URL to the pgx5:// scheme the
// golang-migrate pgx driver registers itself under.
func toPgx5URL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("testutil: parse LMS_TEST_DATABASE_URL: %v", err)
	}
	u.Scheme = "pgx5"
	return u.String()
}

func withCredentials(t *testing.T, raw, user, password string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("testutil: parse LMS_TEST_DATABASE_URL: %v", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

// ensureAppTestRole creates the app_test role if it doesn't already
// exist, explicitly WITHOUT superuser or bypassrls, and grants it the
// table privileges RLS policies need to even attempt a query (GRANT is a
// coarser gate than RLS — a role needs both).
func ensureAppTestRole(t *testing.T, adminURL string) {
	t.Helper()

	db, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatalf("testutil: open admin connection: %v", err)
	}
	defer db.Close()

	stmts := []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
			END IF;
		END $$;`, appTestRole, appTestRole, appTestPassword),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s;`, appTestRole),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s;`, appTestRole),
		fmt.Sprintf(`GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO %s;`, appTestRole),
		fmt.Sprintf(`GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO %s;`, appTestRole),
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("testutil: setup app_test role: %v\nstatement: %s", err, stmt)
		}
	}
}
