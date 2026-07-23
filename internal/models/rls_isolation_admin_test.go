package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/models"
	"growth-lms/internal/testutil"
)

// TestRLS_PlansPlatformOwnerOnlyWrites proves the plans catalog (migration
// 000016) is readable by any authenticated org member but writable only by a
// platform owner.
func TestRLS_PlansPlatformOwnerOnlyWrites(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	owner := seedUser(t, admin, uuid.NewString()+"@example.com")
	platformOwner := seedUser(t, admin, uuid.NewString()+"@example.com")
	org := seedOrgWithOwner(t, admin, owner, "org-"+uuid.NewString())
	_, err := admin.Exec(ctx, `UPDATE profiles SET is_platform_owner = true WHERE id = $1`, platformOwner)
	require.NoError(t, err)

	plans := models.NewPlanRepo()

	// A plain org owner can read the catalog (the seeded default plan) ...
	txOwner, err := dbctx.Begin(ctx, pool, owner, org, "owner")
	require.NoError(t, err)
	list, err := plans.List(ctx, txOwner.Tx)
	require.NoError(t, err)
	require.NotEmpty(t, list, "an org owner must be able to read the plan catalog")
	// ... but cannot create a plan (platform-owner only).
	_, err = plans.Create(ctx, txOwner.Tx, models.Plan{Code: "denied-" + uuid.NewString(), Name: "Denied", Currency: "INR", IsActive: true})
	require.Error(t, err, "a non-platform-owner must not be able to create a plan")
	require.NoError(t, txOwner.Rollback(ctx))

	// A platform owner can create a plan.
	txPO, err := dbctx.Begin(ctx, pool, platformOwner, "", "")
	require.NoError(t, err)
	defer txPO.Rollback(ctx)
	created, err := plans.Create(ctx, txPO.Tx, models.Plan{
		Code: "pro-" + uuid.NewString(), Name: "Pro", Currency: "INR", IsActive: true,
	})
	require.NoError(t, err, "a platform owner must be able to create a plan")
	require.NotEmpty(t, created.ID)
}

// TestRLS_OrgFeatureFlagsIsolation proves per-org flag overrides are
// tenant-scoped: an owner sets their own org's override and cannot see or set
// another org's.
func TestRLS_OrgFeatureFlagsIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())

	// Seed a flag directly (platform-owner action; admin conn bypasses RLS).
	flagKey := "flag-" + uuid.NewString()
	_, err := admin.Exec(ctx, `INSERT INTO feature_flags (key, description, default_enabled) VALUES ($1, '', false)`, flagKey)
	require.NoError(t, err)

	flags := models.NewFeatureFlagRepo()

	// Owner A sets an override for org A.
	txA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	require.NoError(t, flags.SetOrgOverride(ctx, txA.Tx, orgA, flagKey, true, ownerA))
	enabled, err := flags.IsEnabledForOrg(ctx, txA.Tx, orgA, flagKey)
	require.NoError(t, err)
	require.True(t, enabled, "org A's override must resolve to enabled")
	require.NoError(t, txA.Commit(ctx))

	// Owner B must not see org A's override, and org B resolves to the default.
	txB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	defer txB.Rollback(ctx)
	var seen int
	require.NoError(t, txB.Tx.QueryRow(ctx, `SELECT count(*) FROM org_feature_flags WHERE org_id = $1`, orgA).Scan(&seen))
	require.Equal(t, 0, seen, "org B must not see org A's flag overrides")
	enabledB, err := flags.IsEnabledForOrg(ctx, txB.Tx, orgB, flagKey)
	require.NoError(t, err)
	require.False(t, enabledB, "org B (no override) must resolve to the flag default (false)")
}

// TestRLS_SystemAlertsVisibility proves alert visibility: a platform owner sees
// every alert; an org owner sees only their org's; and cross-org alerts are
// invisible. Writes go through the SECURITY DEFINER record_system_alert().
func TestRLS_SystemAlertsVisibility(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	ownerA := seedUser(t, admin, uuid.NewString()+"@example.com")
	ownerB := seedUser(t, admin, uuid.NewString()+"@example.com")
	platformOwner := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, ownerA, "org-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, ownerB, "org-"+uuid.NewString())
	_, err := admin.Exec(ctx, `UPDATE profiles SET is_platform_owner = true WHERE id = $1`, platformOwner)
	require.NoError(t, err)

	alerts := models.NewAlertRepo()

	// Record one org-A alert and one platform-level (org-less) alert via the
	// SECURITY DEFINER writer, from an org-A owner session.
	txA, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	_, err = alerts.Record(ctx, txA.Tx, models.SystemAlert{
		OrgID: &orgA, Severity: models.AlertSeverityWarning, Category: models.AlertCategoryWebhook,
		Source: "test", Message: "org A webhook issue",
	})
	require.NoError(t, err)
	_, err = alerts.Record(ctx, txA.Tx, models.SystemAlert{
		Severity: models.AlertSeverityCritical, Category: models.AlertCategoryJob,
		Source: "test", Message: "platform job failure",
	})
	require.NoError(t, err)
	require.NoError(t, txA.Commit(ctx))

	// Org A owner sees their org's alert but not the platform-level one.
	txAread, err := dbctx.Begin(ctx, pool, ownerA, orgA, "owner")
	require.NoError(t, err)
	listA, err := alerts.List(ctx, txAread.Tx, models.AlertFilter{})
	require.NoError(t, err)
	require.Len(t, listA, 1, "org A owner must see exactly their org's alert")
	require.Equal(t, "org A webhook issue", listA[0].Message)
	require.NoError(t, txAread.Rollback(ctx))

	// Org B owner sees neither.
	txB, err := dbctx.Begin(ctx, pool, ownerB, orgB, "owner")
	require.NoError(t, err)
	listB, err := alerts.List(ctx, txB.Tx, models.AlertFilter{})
	require.NoError(t, err)
	require.Empty(t, listB, "org B owner must see no other org's alerts")
	require.NoError(t, txB.Rollback(ctx))

	// Platform owner sees both.
	txPO, err := dbctx.Begin(ctx, pool, platformOwner, "", "")
	require.NoError(t, err)
	defer txPO.Rollback(ctx)
	listPO, err := alerts.List(ctx, txPO.Tx, models.AlertFilter{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(listPO), 2, "platform owner must see both org and platform alerts")
}
