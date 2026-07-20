package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// seedUser inserts directly into auth.users, which fires the
// on_auth_user_created trigger and creates the matching profiles row —
// exercising the same path a real Supabase signup takes. Must run
// against an admin (RLS-bypassing) connection.
func seedUser(t *testing.T, admin *pgxpool.Pool, email string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := admin.Exec(context.Background(),
		`INSERT INTO auth.users (id, email) VALUES ($1, $2)`, id, email)
	require.NoError(t, err)
	return id
}

// seedOrgWithOwner inserts an organization and its owner membership
// directly (bypassing create_organization(), which requires a session
// context this admin connection doesn't have) — equivalent end state,
// reached the way an admin/superuser connection reaches it rather than
// the way the app does.
func seedOrgWithOwner(t *testing.T, admin *pgxpool.Pool, ownerUserID, slug string) string {
	t.Helper()
	orgID := uuid.NewString()
	ctx := context.Background()
	_, err := admin.Exec(ctx,
		`INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES ($1, $2, $3, $4)`,
		orgID, slug, slug, ownerUserID)
	require.NoError(t, err)
	_, err = admin.Exec(ctx,
		`INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'owner')`,
		ownerUserID, orgID)
	require.NoError(t, err)
	return orgID
}

func TestRLS_OrganizationIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	userA := seedUser(t, admin, uuid.NewString()+"@example.com")
	userB := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, userA, "org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, userB, "org-b-"+uuid.NewString())

	txA, err := dbctx.Begin(ctx, pool, userA, "", "")
	require.NoError(t, err)
	defer txA.Rollback(ctx)

	// User A can see their own organization.
	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE id = $1`, orgA).Scan(&count))
	require.Equal(t, 1, count, "user A should see their own organization")

	// User A cannot see user B's organization, even by direct ID — RLS
	// hides the row entirely rather than returning a permission error,
	// which is what makes tenant isolation real instead of cosmetic.
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE id = $1`, orgB).Scan(&count))
	require.Equal(t, 0, count, "user A must not see user B's organization")

	// User A cannot UPDATE user B's organization.
	tag, err := txA.Tx.Exec(ctx, `UPDATE organizations SET name = 'hijacked' WHERE id = $1`, orgB)
	require.NoError(t, err)
	require.Equal(t, int64(0), tag.RowsAffected(), "cross-org UPDATE must affect zero rows")

	// User A cannot DELETE user B's organization.
	tag, err = txA.Tx.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, orgB)
	require.NoError(t, err)
	require.Equal(t, int64(0), tag.RowsAffected(), "cross-org DELETE must affect zero rows")
}

func TestRLS_MembershipIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	userA := seedUser(t, admin, uuid.NewString()+"@example.com")
	userB := seedUser(t, admin, uuid.NewString()+"@example.com")
	_ = seedOrgWithOwner(t, admin, userA, "org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, userB, "org-b-"+uuid.NewString())

	txA, err := dbctx.Begin(ctx, pool, userA, "", "")
	require.NoError(t, err)
	defer txA.Rollback(ctx)

	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE org_id = $1`, orgB).Scan(&count))
	require.Equal(t, 0, count, "user A must not see org B's memberships")

	// User A cannot insert themselves into org B as owner.
	_, err = txA.Tx.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'owner')`, userA, orgB)
	require.Error(t, err, "cross-org membership self-insertion must be rejected by RLS WITH CHECK")
}

func TestRLS_APITokenIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	userA := seedUser(t, admin, uuid.NewString()+"@example.com")
	userB := seedUser(t, admin, uuid.NewString()+"@example.com")
	_ = seedOrgWithOwner(t, admin, userA, "org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, userB, "org-b-"+uuid.NewString())

	_, err := admin.Exec(ctx,
		`INSERT INTO api_tokens (org_id, name, token_hash, token_prefix, created_by_user_id) VALUES ($1, 'seed', 'hash', 'prefix01', $2)`,
		orgB, userB)
	require.NoError(t, err)

	txA, err := dbctx.Begin(ctx, pool, userA, "", "")
	require.NoError(t, err)
	defer txA.Rollback(ctx)

	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE org_id = $1`, orgB).Scan(&count))
	require.Equal(t, 0, count, "user A must not see org B's api tokens")
}
