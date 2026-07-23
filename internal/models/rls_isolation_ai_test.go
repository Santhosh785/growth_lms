package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// TestRLS_AIGenerationsActorVisibility proves the ai_generations ledger
// (migration 000010) is actor-scoped for members: a learner can log a
// generation and read their own rows, but cannot read another member's,
// while an owner/teacher sees the whole org ledger.
func TestRLS_AIGenerationsActorVisibility(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, org, "learner")
	seedMember(t, admin, learnerB, org, "learner")

	insert := `INSERT INTO ai_generations (org_id, actor_user_id, kind, provider, model, prompt_version, status)
		VALUES ($1, $2, 'tutor', 'stub', 'stub-echo', 'tutor/v1', 'succeeded')`

	// Learner A logs their own tutor generation and reads it back.
	txA, err := dbctx.Begin(ctx, pool, learnerA, org, "learner")
	require.NoError(t, err)
	_, err = txA.Tx.Exec(ctx, insert, org, learnerA)
	require.NoError(t, err, "a member must be able to log their own AI generation")

	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM ai_generations WHERE org_id = $1`, org).Scan(&count))
	require.Equal(t, 1, count, "a member must read their own AI generations")
	require.NoError(t, txA.Rollback(ctx))

	// Persist a learner-A row via admin so other callers have something to (not) see.
	_, err = admin.Exec(ctx, insert, org, learnerA)
	require.NoError(t, err)

	// Learner B must not see learner A's generation.
	txB, err := dbctx.Begin(ctx, pool, learnerB, org, "learner")
	require.NoError(t, err)
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM ai_generations WHERE org_id = $1`, org).Scan(&count))
	require.Equal(t, 0, count, "a learner must not read another member's AI generations")
	require.NoError(t, txB.Rollback(ctx))

	// The owner sees the whole org ledger.
	txO, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txO.Rollback(ctx)
	require.NoError(t, txO.Tx.QueryRow(ctx, `SELECT count(*) FROM ai_generations WHERE org_id = $1`, org).Scan(&count))
	require.Equal(t, 1, count, "an owner must see the org's whole AI generation ledger")
}

// TestRLS_AIGenerationsCrossOrgIsolation proves org A's owner cannot read
// org B's AI ledger.
func TestRLS_AIGenerationsCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-b-"+uuid.NewString())

	_, err := admin.Exec(ctx, `INSERT INTO ai_generations (org_id, actor_user_id, kind, provider, model, prompt_version, status)
		VALUES ($1, $2, 'outline', 'stub', 'stub-echo', 'outline/v1', 'succeeded')`, orgB, ownerB)
	require.NoError(t, err)

	txA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	defer txA.Rollback(ctx)

	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM ai_generations WHERE org_id = $1`, orgB).Scan(&count))
	require.Equal(t, 0, count, "org A's owner must not see org B's AI generations")
}

// TestRLS_AITutorSessionsLearnerIsolation proves a tutor session is private
// to its owning learner: another member cannot read it, but an owner/teacher
// can (for support/oversight).
func TestRLS_AITutorSessionsLearnerIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, org, "learner")
	seedMember(t, admin, learnerB, org, "learner")

	// A course is required by the FK; seed a minimal one as admin.
	var courseID string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO courses (org_id, title, status, created_by) VALUES ($1, 'C', 'draft', $2) RETURNING id`,
		org, owner).Scan(&courseID))

	// Learner A creates a tutor session.
	txA, err := dbctx.Begin(ctx, pool, learnerA, org, "learner")
	require.NoError(t, err)
	_, err = txA.Tx.Exec(ctx,
		`INSERT INTO ai_tutor_sessions (org_id, course_id, learner_id, title) VALUES ($1, $2, $3, 'q')`,
		org, courseID, learnerA)
	require.NoError(t, err, "a learner must create their own tutor session")
	require.NoError(t, txA.Rollback(ctx))

	_, err = admin.Exec(ctx,
		`INSERT INTO ai_tutor_sessions (org_id, course_id, learner_id, title) VALUES ($1, $2, $3, 'q')`,
		org, courseID, learnerA)
	require.NoError(t, err)

	// Learner B must not see learner A's session.
	txB, err := dbctx.Begin(ctx, pool, learnerB, org, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM ai_tutor_sessions WHERE course_id = $1`, courseID).Scan(&count))
	require.Equal(t, 0, count, "a learner must not read another learner's tutor session")
	require.NoError(t, txB.Rollback(ctx))

	// The owner (is_org_teacher) can see it for oversight.
	txO, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txO.Rollback(ctx)
	require.NoError(t, txO.Tx.QueryRow(ctx, `SELECT count(*) FROM ai_tutor_sessions WHERE course_id = $1`, courseID).Scan(&count))
	require.Equal(t, 1, count, "an owner/teacher must see learners' tutor sessions in their org")
}
