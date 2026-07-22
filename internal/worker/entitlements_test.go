package worker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/testutil"
)

// TestSweepExpiredEntitlements proves the expire-sweep correctly flips an
// active, fixed-term entitlement whose expires_at has passed to
// 'expired', updates the corresponding learner_course_access row, and
// writes one audit_events entry — while leaving a still-active
// (non-expired, or perpetual/no-expiry) entitlement untouched.
func TestSweepExpiredEntitlements(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()
	logger := discardLogger()

	days := 30
	expiredFixture := seedCommerceFixture(t, pool, models.OfferTypeSubscription, &days)
	stillActiveFixture := seedCommerceFixture(t, pool, models.OfferTypeSubscription, &days)
	perpetualFixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)

	entitlements := models.NewEntitlementRepo()
	access := models.NewLearnerCourseAccessRepo()

	pastExpiry := time.Now().Add(-time.Hour)
	expiredEntitlement, err := entitlements.Create(ctx, pool, models.Entitlement{
		OrgID:     expiredFixture.orgID,
		OrderID:   &expiredFixture.orderID,
		LearnerID: expiredFixture.learnerID,
		CourseID:  expiredFixture.courseID,
		Status:    models.EntitlementStatusActive,
		ExpiresAt: &pastExpiry,
	})
	require.NoError(t, err)
	_, err = access.Create(ctx, pool, expiredFixture.orgID, expiredFixture.learnerID, expiredFixture.courseID, &expiredEntitlement.ID)
	require.NoError(t, err)

	futureExpiry := time.Now().Add(24 * time.Hour)
	stillActiveEntitlement, err := entitlements.Create(ctx, pool, models.Entitlement{
		OrgID:     stillActiveFixture.orgID,
		OrderID:   &stillActiveFixture.orderID,
		LearnerID: stillActiveFixture.learnerID,
		CourseID:  stillActiveFixture.courseID,
		Status:    models.EntitlementStatusActive,
		ExpiresAt: &futureExpiry,
	})
	require.NoError(t, err)
	_, err = access.Create(ctx, pool, stillActiveFixture.orgID, stillActiveFixture.learnerID, stillActiveFixture.courseID, &stillActiveEntitlement.ID)
	require.NoError(t, err)

	perpetualEntitlement, err := entitlements.Create(ctx, pool, models.Entitlement{
		OrgID:     perpetualFixture.orgID,
		OrderID:   &perpetualFixture.orderID,
		LearnerID: perpetualFixture.learnerID,
		CourseID:  perpetualFixture.courseID,
		Status:    models.EntitlementStatusActive,
		ExpiresAt: nil,
	})
	require.NoError(t, err)
	_, err = access.Create(ctx, pool, perpetualFixture.orgID, perpetualFixture.learnerID, perpetualFixture.courseID, &perpetualEntitlement.ID)
	require.NoError(t, err)

	require.NoError(t, sweepExpiredEntitlements(ctx, pool, logger))

	got, err := entitlements.Get(ctx, pool, expiredEntitlement.ID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusExpired, got.Status)

	accessRow, err := access.Get(ctx, pool, expiredFixture.learnerID, expiredFixture.courseID)
	require.NoError(t, err)
	require.Equal(t, models.AccessStatusExpired, accessRow.AccessStatus)

	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM audit_events WHERE action = 'entitlement.expired' AND resource_id = $1`, expiredEntitlement.ID))

	stillGot, err := entitlements.Get(ctx, pool, stillActiveEntitlement.ID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusActive, stillGot.Status, "a not-yet-expired fixed-term entitlement must never be swept")

	perpetualGot, err := entitlements.Get(ctx, pool, perpetualEntitlement.ID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusActive, perpetualGot.Status, "a perpetual (no expires_at) entitlement must never be swept")

	// Idempotent: running the sweep again does nothing further.
	require.NoError(t, sweepExpiredEntitlements(ctx, pool, logger))
	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM audit_events WHERE action = 'entitlement.expired' AND resource_id = $1`, expiredEntitlement.ID))
}
