package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// TestRLS_CodeExercisesCrossOrgIsolation proves code_exercises (migration
// 000012) is tenant-scoped: a member of one org cannot read or write another
// org's exercises, and only teachers/owners can author them.
func TestRLS_CodeExercisesCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, orgA, "learner")

	// Seed an exercise in org A directly.
	var exA string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO code_exercises (org_id, slug, title, language, is_published)
		 VALUES ($1, 'ex-a', 'Exercise A', 'python', true) RETURNING id`,
		orgA).Scan(&exA))

	// A learner in org A can read org A's exercise.
	txLA, err := dbctx.Begin(ctx, pool, learnerA, orgA, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txLA.Tx.QueryRow(ctx, `SELECT count(*) FROM code_exercises WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 1, count, "an org member must read their org's code exercises")
	// ...but a learner may NOT author an exercise (insert requires teacher).
	_, err = txLA.Tx.Exec(ctx, `INSERT INTO code_exercises (org_id, slug, title, language) VALUES ($1, 'x', 'X', 'python')`, orgA)
	require.Error(t, err, "a learner must not be able to author a code exercise")
	require.NoError(t, txLA.Rollback(ctx))

	// The owner of org B must not see org A's exercise.
	txOB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	require.NoError(t, txOB.Tx.QueryRow(ctx, `SELECT count(*) FROM code_exercises WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 0, count, "an org must not see another org's code exercises")
	require.NoError(t, txOB.Rollback(ctx))

	// The owner of org A can author (teacher/owner privilege).
	txOA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	defer txOA.Rollback(ctx)
	_, err = txOA.Tx.Exec(ctx, `INSERT INTO code_exercises (org_id, slug, title, language) VALUES ($1, 'owner-ex', 'Owner Ex', 'go')`, orgA)
	require.NoError(t, err, "an owner must be able to author a code exercise")
}

// TestRLS_CodeSubmissionsLearnerOwnership proves code_submissions is
// learner-owned: a learner reads/writes only their own submissions, another
// learner cannot see them, and an owner/teacher retains oversight visibility.
func TestRLS_CodeSubmissionsLearnerOwnership(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, org, "learner")
	seedMember(t, admin, learnerB, org, "learner")

	insert := `INSERT INTO code_submissions (org_id, learner_id, language, source, status)
		VALUES ($1, $2, 'python', 'print(1)', 'succeeded')`

	// Learner A records and reads back their own submission.
	txA, err := dbctx.Begin(ctx, pool, learnerA, org, "learner")
	require.NoError(t, err)
	_, err = txA.Tx.Exec(ctx, insert, org, learnerA)
	require.NoError(t, err, "a learner must be able to record their own code submission")
	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM code_submissions WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 1, count, "a learner must read their own code submissions")
	// A learner may NOT submit on another learner's behalf.
	_, err = txA.Tx.Exec(ctx, insert, org, learnerB)
	require.Error(t, err, "a learner must not record a submission on another learner's behalf")
	require.NoError(t, txA.Rollback(ctx))

	// Persist a learner-A submission so others have something to (not) see.
	_, err = admin.Exec(ctx, insert, org, learnerA)
	require.NoError(t, err)

	// Learner B must not see learner A's submissions.
	txB, err := dbctx.Begin(ctx, pool, learnerB, org, "learner")
	require.NoError(t, err)
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM code_submissions WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 0, count, "a learner must not read another learner's code submissions")
	require.NoError(t, txB.Rollback(ctx))

	// The owner sees learner A's submissions for oversight.
	txO, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txO.Rollback(ctx)
	require.NoError(t, txO.Tx.QueryRow(ctx, `SELECT count(*) FROM code_submissions WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 1, count, "an owner must see learners' code submissions for oversight")
}
