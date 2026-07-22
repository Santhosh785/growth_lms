package worker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/testutil"
)

// TestSweepAbandonedOrders proves the abandon-sweep correctly identifies
// stale pending orders (older than staleOrderCutoff) and flips them to
// 'abandoned', while never touching a fresh pending order or one that
// already reached a terminal status.
func TestSweepAbandonedOrders(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()
	logger := discardLogger()

	staleFixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)
	freshFixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)
	succeededFixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)

	// Backdate the "stale" order's created_at past the cutoff.
	_, err := pool.Exec(ctx, `UPDATE orders SET created_at = now() - interval '31 minutes' WHERE id = $1`, staleFixture.orderID)
	require.NoError(t, err)

	// The "succeeded" order is also old, but already reached a terminal
	// status — the sweep must never touch it.
	_, err = pool.Exec(ctx, `UPDATE orders SET created_at = now() - interval '31 minutes', status = 'succeeded' WHERE id = $1`, succeededFixture.orderID)
	require.NoError(t, err)

	require.NoError(t, sweepAbandonedOrders(ctx, pool, logger))

	orders := models.NewOrderRepo()

	stale, err := orders.Get(ctx, pool, staleFixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.OrderStatusAbandoned, stale.Status)

	fresh, err := orders.Get(ctx, pool, freshFixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.OrderStatusPending, fresh.Status, "a fresh pending order must never be swept")

	succeeded, err := orders.Get(ctx, pool, succeededFixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.OrderStatusSucceeded, succeeded.Status, "a succeeded order must never be swept back to abandoned, regardless of age")

	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM payment_audit_trail WHERE order_id = $1 AND event_type = 'order.abandoned'`, staleFixture.orderID))

	// Idempotent: running the sweep again does nothing further.
	require.NoError(t, sweepAbandonedOrders(ctx, pool, logger))
	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM payment_audit_trail WHERE order_id = $1 AND event_type = 'order.abandoned'`, staleFixture.orderID))
}
