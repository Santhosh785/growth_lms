package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// TestRLS_BoardVersionsCrossOrgIsolation proves collab_board_versions (migration
// 000015) is tenant-scoped: a member of one org cannot read another org's board
// versions, and a member may only save a checkpoint under their own identity.
func TestRLS_BoardVersionsCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	memberA := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())
	seedMember(t, admin, memberA, orgA, "learner")

	var courseA string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO courses (org_id, title, created_by) VALUES ($1, 'Course A', $2) RETURNING id`,
		orgA, ownerA).Scan(&courseA))
	var boardA string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO collab_boards (org_id, course_id, title, created_by)
		 VALUES ($1, $2, 'Board A', $3) RETURNING id`, orgA, courseA, ownerA).Scan(&boardA))
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO collab_board_versions (org_id, board_id, label, created_by)
		 VALUES ($1, $2, 'v1', $3) RETURNING id`, orgA, boardA, ownerA).Scan(new(string)))

	// A member of org A can read the board's versions.
	txA, err := dbctx.Begin(ctx, pool, memberA, orgA, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM collab_board_versions WHERE board_id = $1`, boardA).Scan(&count))
	require.Equal(t, 1, count, "an org member must read their board's versions")
	// ...but may not save a checkpoint under another user's identity.
	_, err = txA.Tx.Exec(ctx,
		`INSERT INTO collab_board_versions (org_id, board_id, created_by) VALUES ($1, $2, $3)`, orgA, boardA, ownerA)
	require.Error(t, err, "a member must not save a version as another user")
	// ...and may save one as themselves.
	_, err = txA.Tx.Exec(ctx,
		`INSERT INTO collab_board_versions (org_id, board_id, created_by) VALUES ($1, $2, $3)`, orgA, boardA, memberA)
	require.NoError(t, err, "a member must be able to save their own checkpoint")
	require.NoError(t, txA.Rollback(ctx))

	// The owner of org B must not see org A's board versions.
	txB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	defer txB.Rollback(ctx)
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM collab_board_versions WHERE board_id = $1`, boardA).Scan(&count))
	require.Equal(t, 0, count, "an org must not see another org's board versions")
}

// TestRLS_BoardTemplatesCrossOrgIsolation proves collab_board_templates is
// tenant-scoped and teacher-authored: any member reads their org's templates, a
// learner cannot author one, and another org cannot see them.
func TestRLS_BoardTemplatesCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, orgA, "learner")

	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO collab_board_templates (org_id, title, created_by)
		 VALUES ($1, 'Retro Grid', $2) RETURNING id`, orgA, ownerA).Scan(new(string)))

	// A learner in org A can read templates but cannot author one.
	txL, err := dbctx.Begin(ctx, pool, learnerA, orgA, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txL.Tx.QueryRow(ctx, `SELECT count(*) FROM collab_board_templates WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 1, count, "an org member must read their org's board templates")
	_, err = txL.Tx.Exec(ctx, `INSERT INTO collab_board_templates (org_id, title) VALUES ($1, 'x')`, orgA)
	require.Error(t, err, "a learner must not author a board template")
	require.NoError(t, txL.Rollback(ctx))

	// The owner of org A can author one.
	txOA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	_, err = txOA.Tx.Exec(ctx, `INSERT INTO collab_board_templates (org_id, title) VALUES ($1, 'SWOT')`, orgA)
	require.NoError(t, err, "an owner must be able to author a board template")
	require.NoError(t, txOA.Rollback(ctx))

	// The owner of org B must not see org A's templates.
	txB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	defer txB.Rollback(ctx)
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM collab_board_templates WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 0, count, "an org must not see another org's board templates")
}
