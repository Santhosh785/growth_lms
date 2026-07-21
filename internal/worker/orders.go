package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
)

// abandonOrdersSweepInterval mirrors publishSweepInterval's precedent in
// worker.go: a plain time.Ticker-driven goroutine, not asynq's periodic-
// task machinery. Named independently of publishSweepInterval (even
// though the value matches) so it can be tuned separately later.
const abandonOrdersSweepInterval = time.Minute

// staleOrderCutoff is how old a still-pending (or payment-initiated)
// order must be before this sweep abandons it. Per config.go's comment
// near RazorpayConfig, this is a hardcoded const rather than a config
// field, following publishSweepInterval's own precedent.
const staleOrderCutoff = 30 * time.Minute

// sweepAbandonedOrders flips every order still 'pending' or
// 'payment_initiated' whose created_at is older than staleOrderCutoff to
// 'abandoned'. No entitlement/revenue side effects — an abandoned order
// never had a successful payment, so there is nothing to revoke. Each
// order is processed independently so one failure doesn't block the rest,
// matching sweepScheduledPublishes' per-course error handling in
// publish.go.
func sweepAbandonedOrders(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	orders := models.NewOrderRepo()
	paymentAudit := models.NewPaymentAuditRepo()

	stale, err := orders.ListPendingOlderThan(ctx, pool, staleOrderCutoff)
	if err != nil {
		return err
	}

	for _, o := range stale {
		if err := abandonOneOrder(ctx, pool, orders, paymentAudit, o, logger); err != nil {
			logger.Error("sweep: failed to abandon stale order", "order_id", o.ID, "error", err)
		}
	}
	return nil
}

func abandonOneOrder(ctx context.Context, pool *pgxpool.Pool, orders *models.OrderRepo, paymentAudit *models.PaymentAuditRepo, o *models.Order, logger *slog.Logger) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Re-check status inside the transaction: the order may have
	// succeeded (via the webhook job) between the outer query and this
	// point — never abandon an order that has moved on since.
	current, err := orders.Get(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	if current.Status != models.OrderStatusPending && current.Status != models.OrderStatusPaymentInitiated {
		return nil
	}

	oldState := current.Status
	if _, err := orders.UpdateStatus(ctx, tx, o.ID, models.OrderStatusAbandoned); err != nil {
		return err
	}

	if err := paymentAudit.Record(ctx, tx, models.PaymentAuditEvent{
		OrgID:     current.OrgID,
		EventType: "order.abandoned",
		OrderID:   &o.ID,
		OldState:  &oldState,
		NewState:  models.OrderStatusAbandoned,
		UserID:    &current.LearnerID,
	}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// runAbandonOrdersSweepLoop enqueues a sweep once per interval until ctx
// is canceled. Started alongside the asynq server in Run(), mirroring
// runPublishSweepLoop's exact shape in publish.go.
func runAbandonOrdersSweepLoop(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sweepAbandonedOrders(ctx, pool, logger); err != nil {
				logger.Error("abandon orders sweep failed", "error", err)
			}
		}
	}
}
