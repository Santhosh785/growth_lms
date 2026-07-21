package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Refund status constants, matching the CHECK constraint in
// db/migrations/000006_commerce.up.sql.
const (
	RefundStatusPending   = "pending"
	RefundStatusSucceeded = "succeeded"
	RefundStatusFailed    = "failed"
)

// Chargeback status constants, matching the CHECK constraint in
// db/migrations/000006_commerce.up.sql — note "pending", not "open".
const (
	ChargebackStatusPending = "pending"
	ChargebackStatusWon     = "won"
	ChargebackStatusLost    = "lost"
)

// Refund is a single refund attempt against a payment
// (db/migrations/000006_commerce.up.sql). It has no OrderID column —
// Task 1's schema doesn't denormalize it onto this table; join through
// PaymentID -> payments -> orders if the order is needed.
type Refund struct {
	ID               string
	OrgID            string
	PaymentID        string
	RazorpayRefundID *string
	Status           string
	Amount           float64 // NUMERIC(12,2); currency is the parent payment/order's
	Reason           *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Chargeback is a disputed-payment record (db/migrations/000006_commerce.up.sql).
// It has no OrderID column (join through PaymentID) and no
// RazorpayDisputeID column — if that ID needs to be tracked for MVP, it
// goes in Reason rather than inventing a column Task 1 didn't create.
type Chargeback struct {
	ID        string
	OrgID     string
	PaymentID string
	Status    string
	Amount    float64 // NUMERIC(12,2); currency is the parent payment/order's
	Reason    *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const refundColumns = `id, org_id, payment_id, razorpay_refund_id, status, amount, reason, created_at, updated_at`
const chargebackColumns = `id, org_id, payment_id, status, amount, reason, created_at, updated_at`

type RefundRepo struct{}

func NewRefundRepo() *RefundRepo { return &RefundRepo{} }

// Create inserts a refund row with status = 'pending' server-side (same
// rule as OrderRepo.Create), when the in-app "Refund" action calls the
// Razorpay Refund API; the row's status only moves to succeeded/failed
// once the refund webhook is verified and processed.
func (r *RefundRepo) Create(ctx context.Context, q Querier, refund Refund) (*Refund, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO refunds (org_id, payment_id, razorpay_refund_id, status, amount, reason)
		VALUES ($1, $2, $3, 'pending', $4, $5)
		RETURNING `+refundColumns,
		refund.OrgID, refund.PaymentID, refund.RazorpayRefundID, refund.Amount, refund.Reason)
	out, err := scanRefund(row)
	if err != nil {
		return nil, fmt.Errorf("models: create refund: %w", err)
	}
	return out, nil
}

// UpdateStatus sets status/razorpay_refund_id/updated_at.
func (r *RefundRepo) UpdateStatus(ctx context.Context, q Querier, id, status string, razorpayRefundID *string) (*Refund, error) {
	row := q.QueryRow(ctx, `
		UPDATE refunds SET status = $2, razorpay_refund_id = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+refundColumns, id, status, razorpayRefundID)
	out, err := scanRefund(row)
	if err != nil {
		return nil, fmt.Errorf("models: update refund status: %w", err)
	}
	return out, nil
}

// GetByPaymentID returns every refund attempt for a payment, most recent
// first — a payment can have more than one refund attempt (e.g. a failed
// retry).
func (r *RefundRepo) GetByPaymentID(ctx context.Context, q Querier, paymentID string) ([]*Refund, error) {
	rows, err := q.Query(ctx, `
		SELECT `+refundColumns+` FROM refunds
		WHERE payment_id = $1
		ORDER BY created_at DESC
	`, paymentID)
	if err != nil {
		return nil, fmt.Errorf("models: list refunds by payment id: %w", err)
	}
	defer rows.Close()

	var out []*Refund
	for rows.Next() {
		rf, err := scanRefundRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rf)
	}
	return out, rows.Err()
}

// GetByRazorpayRefundID looks up the refund row matching a Razorpay
// refund-entity ID — used by the Task 8 worker's refund.processed/
// refund.failed processing to find the refund row an in-app refund action
// already created (in 'pending' status). Returns ErrNotFound if none
// exists, which the caller treats as "this refund was initiated from the
// Razorpay dashboard, not this app" and creates a new row instead.
func (r *RefundRepo) GetByRazorpayRefundID(ctx context.Context, q Querier, razorpayRefundID string) (*Refund, error) {
	row := q.QueryRow(ctx, `SELECT `+refundColumns+` FROM refunds WHERE razorpay_refund_id = $1`, razorpayRefundID)
	out, err := scanRefund(row)
	if err != nil {
		return nil, fmt.Errorf("models: get refund by razorpay refund id: %w", err)
	}
	return out, nil
}

func scanRefund(row pgx.Row) (*Refund, error) {
	var rf Refund
	if err := row.Scan(&rf.ID, &rf.OrgID, &rf.PaymentID, &rf.RazorpayRefundID, &rf.Status, &rf.Amount,
		&rf.Reason, &rf.CreatedAt, &rf.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan refund: %w", err)
	}
	return &rf, nil
}

func scanRefundRows(rows pgx.Rows) (*Refund, error) {
	var rf Refund
	if err := rows.Scan(&rf.ID, &rf.OrgID, &rf.PaymentID, &rf.RazorpayRefundID, &rf.Status, &rf.Amount,
		&rf.Reason, &rf.CreatedAt, &rf.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan refund: %w", err)
	}
	return &rf, nil
}

type ChargebackRepo struct{}

func NewChargebackRepo() *ChargebackRepo { return &ChargebackRepo{} }

// Create inserts a chargeback row with status = 'pending', always
// created from a verified dispute-opened webhook, never from any in-app
// action (there is no "initiate a chargeback" button).
func (r *ChargebackRepo) Create(ctx context.Context, q Querier, c Chargeback) (*Chargeback, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO chargebacks (org_id, payment_id, status, amount, reason)
		VALUES ($1, $2, 'pending', $3, $4)
		RETURNING `+chargebackColumns,
		c.OrgID, c.PaymentID, c.Amount, c.Reason)
	out, err := scanChargeback(row)
	if err != nil {
		return nil, fmt.Errorf("models: create chargeback: %w", err)
	}
	return out, nil
}

// UpdateStatus sets status/updated_at (pending -> won/lost).
func (r *ChargebackRepo) UpdateStatus(ctx context.Context, q Querier, id, status string) (*Chargeback, error) {
	row := q.QueryRow(ctx, `
		UPDATE chargebacks SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+chargebackColumns, id, status)
	out, err := scanChargeback(row)
	if err != nil {
		return nil, fmt.Errorf("models: update chargeback status: %w", err)
	}
	return out, nil
}

// GetLatestByPaymentID returns the most recently created chargeback for a
// payment, or ErrNotFound if the payment has never been disputed — used
// by the Task 8 worker's dispute-event processing to find (or discover
// the absence of) an existing chargeback row before deciding whether to
// create one (dispute opened) or update one (dispute won/lost).
func (r *ChargebackRepo) GetLatestByPaymentID(ctx context.Context, q Querier, paymentID string) (*Chargeback, error) {
	row := q.QueryRow(ctx, `
		SELECT `+chargebackColumns+` FROM chargebacks
		WHERE payment_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, paymentID)
	out, err := scanChargeback(row)
	if err != nil {
		return nil, fmt.Errorf("models: get latest chargeback by payment id: %w", err)
	}
	return out, nil
}

func scanChargeback(row pgx.Row) (*Chargeback, error) {
	var c Chargeback
	if err := row.Scan(&c.ID, &c.OrgID, &c.PaymentID, &c.Status, &c.Amount, &c.Reason,
		&c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan chargeback: %w", err)
	}
	return &c, nil
}
