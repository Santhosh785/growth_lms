package models

import (
	"context"
	"fmt"
)

// PaymentAuditEvent is a single payment/order state transition to record.
// OrderID/PaymentID/UserID are nullable because not every transition has
// all three (e.g. a chargeback-opened event may have no acting user).
type PaymentAuditEvent struct {
	OrgID     string
	EventType string
	OrderID   *string
	PaymentID *string
	OldState  *string
	NewState  string
	Reason    *string
	UserID    *string
}

type PaymentAuditRepo struct{}

func NewPaymentAuditRepo() *PaymentAuditRepo { return &PaymentAuditRepo{} }

// Record inserts a payment_audit_trail row. Callers pass the same
// Querier (typically dbctx.RequestTx, or the worker's transaction when
// called from webhook processing) used for the state-changing mutation
// being audited — same "insert inside the same transaction as the
// mutation" rule as AuditRepo.Record, and for the same reason: a failure
// to log should roll back the mutation too. This table is append-only;
// there are deliberately no Get/List methods on this repo for MVP (Task
// 9 can add a read method against this same table later if it needs
// one).
func (r *PaymentAuditRepo) Record(ctx context.Context, q Querier, e PaymentAuditEvent) error {
	_, err := q.Exec(ctx, `
		INSERT INTO payment_audit_trail (org_id, event_type, order_id, payment_id, old_state, new_state, reason, user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, e.OrgID, e.EventType, e.OrderID, e.PaymentID, e.OldState, e.NewState, e.Reason, e.UserID)
	if err != nil {
		return fmt.Errorf("models: record payment audit event: %w", err)
	}
	return nil
}
