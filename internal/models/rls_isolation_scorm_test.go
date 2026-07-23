package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/testutil"
)

// TestRLS_ScormPackagesCrossOrgIsolation proves scorm_packages (migration
// 000013) is tenant-scoped: a member of one org cannot read or write another
// org's packages, and only teachers/owners can author them.
func TestRLS_ScormPackagesCrossOrgIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, orgA, "learner")

	// Seed a package in org A directly.
	var pkgA string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO scorm_packages (org_id, slug, title, version, launch_href, is_published)
		 VALUES ($1, 'pkg-a', 'Package A', '1.2', 'index.html', true) RETURNING id`,
		orgA).Scan(&pkgA))

	// A learner in org A can read org A's package.
	txLA, err := dbctx.Begin(ctx, pool, learnerA, orgA, "learner")
	require.NoError(t, err)
	var count int
	require.NoError(t, txLA.Tx.QueryRow(ctx, `SELECT count(*) FROM scorm_packages WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 1, count, "an org member must read their org's scorm packages")
	// ...but a learner may NOT author a package (insert requires teacher).
	_, err = txLA.Tx.Exec(ctx, `INSERT INTO scorm_packages (org_id, slug, title, version, launch_href) VALUES ($1, 'x', 'X', '1.2', 'x.html')`, orgA)
	require.Error(t, err, "a learner must not be able to author a scorm package")
	require.NoError(t, txLA.Rollback(ctx))

	// The owner of org B must not see org A's package.
	txOB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	require.NoError(t, txOB.Tx.QueryRow(ctx, `SELECT count(*) FROM scorm_packages WHERE org_id = $1`, orgA).Scan(&count))
	require.Equal(t, 0, count, "an org must not see another org's scorm packages")
	require.NoError(t, txOB.Rollback(ctx))

	// The owner of org A can author (teacher/owner privilege).
	txOA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	defer txOA.Rollback(ctx)
	_, err = txOA.Tx.Exec(ctx, `INSERT INTO scorm_packages (org_id, slug, title, version, launch_href) VALUES ($1, 'owner-pkg', 'Owner Pkg', '2004', 'start.html')`, orgA)
	require.NoError(t, err, "an owner must be able to author a scorm package")
}

// TestRLS_ScormAttemptsLearnerOwnership proves scorm_attempts is learner-owned:
// a learner reads/writes only their own attempts, another learner cannot see
// them, and an owner/teacher retains oversight visibility for reporting.
func TestRLS_ScormAttemptsLearnerOwnership(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	learnerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	seedMember(t, admin, learnerA, org, "learner")
	seedMember(t, admin, learnerB, org, "learner")

	var pkg string
	require.NoError(t, admin.QueryRow(ctx,
		`INSERT INTO scorm_packages (org_id, slug, title, version, launch_href, is_published)
		 VALUES ($1, 'pkg', 'Pkg', '1.2', 'index.html', true) RETURNING id`,
		org).Scan(&pkg))

	insert := `INSERT INTO scorm_attempts (org_id, package_id, learner_id, attempt_number)
		VALUES ($1, $2, $3, 1)`

	// Learner A records and reads back their own attempt.
	txA, err := dbctx.Begin(ctx, pool, learnerA, org, "learner")
	require.NoError(t, err)
	_, err = txA.Tx.Exec(ctx, insert, org, pkg, learnerA)
	require.NoError(t, err, "a learner must be able to record their own scorm attempt")
	var count int
	require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM scorm_attempts WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 1, count, "a learner must read their own scorm attempts")
	// A learner may NOT record an attempt on another learner's behalf.
	_, err = txA.Tx.Exec(ctx, insert, org, pkg, learnerB)
	require.Error(t, err, "a learner must not record an attempt on another learner's behalf")
	require.NoError(t, txA.Rollback(ctx))

	// Persist a learner-A attempt so others have something to (not) see.
	_, err = admin.Exec(ctx, insert, org, pkg, learnerA)
	require.NoError(t, err)

	// Learner B must not see learner A's attempts.
	txB, err := dbctx.Begin(ctx, pool, learnerB, org, "learner")
	require.NoError(t, err)
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM scorm_attempts WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 0, count, "a learner must not read another learner's scorm attempts")
	require.NoError(t, txB.Rollback(ctx))

	// The owner sees learner A's attempts for reporting/oversight.
	txO, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	defer txO.Rollback(ctx)
	require.NoError(t, txO.Tx.QueryRow(ctx, `SELECT count(*) FROM scorm_attempts WHERE learner_id = $1`, learnerA).Scan(&count))
	require.Equal(t, 1, count, "an owner must see learners' scorm attempts for reporting")
}
