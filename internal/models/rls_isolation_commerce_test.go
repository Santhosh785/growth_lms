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

// seedCommerceIsolationFixture seeds one org's worth of offers/orders/
// payments/entitlements rows directly via the admin (RLS-bypassing)
// connection, for TestRLS_CommerceIsolation below. Mirrors
// TestRLS_CourseDomainIsolation's seeding style in rls_isolation_test.go.
func seedCommerceIsolationFixture(t *testing.T, admin *pgxpool.Pool, orgID, ownerID string) (offerID, courseID, orderID, paymentID, entitlementID, learnerID string) {
	t.Helper()
	ctx := context.Background()

	learnerID = seedUser(t, admin, uuid.NewString()+"@example.com")
	_, err := admin.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'learner')`, learnerID, orgID)
	require.NoError(t, err)

	require.NoError(t, admin.QueryRow(ctx, `
		INSERT INTO courses (org_id, title, created_by, status) VALUES ($1, 'B Course', $2, 'published') RETURNING id
	`, orgID, ownerID).Scan(&courseID))

	require.NoError(t, admin.QueryRow(ctx, `
		INSERT INTO offers (org_id, course_id, type, price, currency, tax_rate_percent, created_by)
		VALUES ($1, $2, 'paid', 999, 'INR', 18, $3) RETURNING id
	`, orgID, courseID, ownerID).Scan(&offerID))

	require.NoError(t, admin.QueryRow(ctx, `
		INSERT INTO orders (org_id, offer_id, learner_id, currency, subtotal, tax_amount, commission_amount, total, commission_rate_snapshot, status)
		VALUES ($1, $2, $3, 'INR', 999, 179.82, 99.9, 1178.82, 10, 'succeeded') RETURNING id
	`, orgID, offerID, learnerID).Scan(&orderID))

	require.NoError(t, admin.QueryRow(ctx, `
		INSERT INTO payments (org_id, order_id, razorpay_payment_id, status)
		VALUES ($1, $2, $3, 'succeeded') RETURNING id
	`, orgID, orderID, "pay_"+uuid.NewString()).Scan(&paymentID))

	require.NoError(t, admin.QueryRow(ctx, `
		INSERT INTO entitlements (org_id, order_id, learner_id, course_id, status)
		VALUES ($1, $2, $3, $4, 'active') RETURNING id
	`, orgID, orderID, learnerID, courseID).Scan(&entitlementID))

	return offerID, courseID, orderID, paymentID, entitlementID, learnerID
}

// TestRLS_CommerceIsolation proves org A's session can never see, update,
// or delete org B's offers/orders/payments/entitlements rows — mirroring
// TestRLS_CourseDomainIsolation's structure exactly (same file's sibling
// test), spanning the four core commerce tables per
// task-11-tests.md gap 6. Runs raw SQL directly against a
// dbctx.Begin-scoped transaction (never through a repo/handler wrapper),
// so this is testing the Postgres RLS policy itself, not application-layer
// filtering.
func TestRLS_CommerceIsolation(t *testing.T) {
	pool := testutil.DB(t)
	admin := testutil.AdminDB(t)
	ctx := context.Background()

	userA := seedUser(t, admin, uuid.NewString()+"@example.com")
	userB := seedUser(t, admin, uuid.NewString()+"@example.com")
	orgA := seedOrgWithOwner(t, admin, userA, "commerce-org-a-"+uuid.NewString())
	orgB := seedOrgWithOwner(t, admin, userB, "commerce-org-b-"+uuid.NewString())

	offerB, _, orderB, paymentB, entitlementB, _ := seedCommerceIsolationFixture(t, admin, orgB, userB)
	_ = orgA

	txA, err := dbctx.Begin(ctx, pool, userA, orgA, "owner")
	require.NoError(t, err)
	defer txA.Rollback(ctx)

	cases := []struct {
		table string
		id    string
	}{
		{"offers", offerB},
		{"orders", orderB},
		{"payments", paymentB},
		{"entitlements", entitlementB},
	}

	for _, tc := range cases {
		var count int
		require.NoError(t, txA.Tx.QueryRow(ctx, `SELECT count(*) FROM `+tc.table+` WHERE id = $1`, tc.id).Scan(&count))
		require.Equal(t, 0, count, "org A must not see org B's %s row via SELECT", tc.table)

		var updateErr error
		var tag interface{ RowsAffected() int64 }
		switch tc.table {
		case "offers":
			t2, e := txA.Tx.Exec(ctx, `UPDATE offers SET status = 'archived' WHERE id = $1`, tc.id)
			tag, updateErr = t2, e
		case "orders":
			t2, e := txA.Tx.Exec(ctx, `UPDATE orders SET status = 'failed' WHERE id = $1`, tc.id)
			tag, updateErr = t2, e
		case "payments":
			t2, e := txA.Tx.Exec(ctx, `UPDATE payments SET status = 'failed' WHERE id = $1`, tc.id)
			tag, updateErr = t2, e
		case "entitlements":
			t2, e := txA.Tx.Exec(ctx, `UPDATE entitlements SET status = 'revoked' WHERE id = $1`, tc.id)
			tag, updateErr = t2, e
		}
		require.NoError(t, updateErr)
		require.Equal(t, int64(0), tag.RowsAffected(), "cross-org UPDATE on %s must affect zero rows", tc.table)

		delTag, err := txA.Tx.Exec(ctx, `DELETE FROM `+tc.table+` WHERE id = $1`, tc.id)
		require.NoError(t, err)
		require.Equal(t, int64(0), delTag.RowsAffected(), "cross-org DELETE on %s must affect zero rows", tc.table)
	}
}
