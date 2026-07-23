package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// TestRLS_SimulationsCrossOrgIsolation proves simulations (migration 000014) is
// tenant-scoped: a member of one org cannot read or write another org's
// simulations, and only teachers/owners can author them.
func TestRLS_SimulationsCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, orgA, "learner")

	// Seed a simulation in org A directly.
	var simA string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO simulations (org_id, slug, title, kind, is_published)
		 VALUES ($1, 'sim-a', 'Sim A', 'diagram', true) RETURNING id`,
		orgA).Scan(&simA))

	// A learner in org A can read org A's simulation.
	txLA, err := dbctx.Begin(ctx, pool, learnerA, orgA, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txLA.Tx.QueryRow(ctx, `SELECT count(*) FROM simulations WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 1, count, "an org member must read their org's simulations")
	// ...but a learner may NOT author a simulation (insert requires teacher).
	_, err = txLA.Tx.Exec(ctx, `INSERT INTO simulations (org_id, slug, title, kind) VALUES ($1, 'x', 'X', 'diagram')`, orgA)
	require.Error(t, err, "a learner must not be able to author a simulation")
	require.NoError(t, txLA.Rollback(ctx))

	// The owner of org B must not see org A's simulation.
	txOB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	require.NoError(t, txOB.Tx.QueryRow(ctx, `SELECT count(*) FROM simulations WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 0, count, "an org must not see another org's simulations")
	require.NoError(t, txOB.Rollback(ctx))

	// The owner of org A can author (teacher/owner privilege).
	txOA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	defer txOA.Rollback(ctx)
	_, err = txOA.Tx.Exec(ctx, `INSERT INTO simulations (org_id, slug, title, kind) VALUES ($1, 'owner-sim', 'Owner Sim', 'simulation')`, orgA)
	require.NoError(t, err, "an owner must be able to author a simulation")
}

// TestRLS_SimulationProgressLearnerOwnership proves simulation_progress is
// learner-owned: a learner reads/writes only their own progress, another
// learner cannot see it, and an owner/teacher retains oversight visibility for
// reporting.
func TestRLS_SimulationProgressLearnerOwnership(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, org, "learner")
	seedMember(t, admin, learnerB, org, "learner")

	var sim string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO simulations (org_id, slug, title, kind, is_published)
		 VALUES ($1, 'sim', 'Sim', 'simulation', true) RETURNING id`,
		org).Scan(&sim))

	insert := `INSERT INTO simulation_progress (org_id, simulation_id, learner_id) VALUES ($1, $2, $3)`

	// Learner A records and reads back their own progress.
	txA, err := dbctx.Begin(ctx, pool, learnerA, org, "learner")
	require.NoError(t, err)
	_, err = txA.Tx.Exec(ctx, insert, org, sim, learnerA)
	require.NoError(t, err, "a learner must be able to record their own simulation progress")
	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM simulation_progress WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 1, count, "a learner must read their own simulation progress")
	// A learner may NOT record progress on another learner's behalf.
	_, err = txA.Tx.Exec(ctx, insert, org, sim, learnerB)
	require.Error(t, err, "a learner must not record progress on another learner's behalf")
	require.NoError(t, txA.Rollback(ctx))

	// Persist a learner-A progress row so others have something to (not) see.
	_, err = admin.Exec(ctx, insert, org, sim, learnerA)
	require.NoError(t, err)

	// Learner B must not see learner A's progress.
	txB, err := dbctx.Begin(ctx, pool, learnerB, org, "learner")
	require.NoError(t, err)
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM simulation_progress WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 0, count, "a learner must not read another learner's simulation progress")
	require.NoError(t, txB.Rollback(ctx))

	// The owner sees learner A's progress for reporting/oversight.
	txO, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txO.Rollback(ctx)
	require.NoError(t, txO.Tx.QueryRow(ctx, `SELECT count(*) FROM simulation_progress WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 1, count, "an owner must see learners' simulation progress for reporting")
}
