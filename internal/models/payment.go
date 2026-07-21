package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Payment status constants, matching the CHECK constraint in
// db/migrations/000006_commerce.up.sql.
const (
	PaymentStatusPending    = "pending"
	PaymentStatusProcessing = "processing"
	PaymentStatusSucceeded  = "succeeded"
	PaymentStatusFailed     = "failed"
)

// Payment is a single payment attempt against an order
// (db/migrations/000006_commerce.up.sql). It has no Amount/Currency/
// Method/FailureReason field — Task 1's schema has no such columns. The
// payment's amount/currency are the order's (OrderRepo.Get(orderID).Total/
// .Currency); a failure reason, if Razorpay provides one, lives inside
// RawProviderData.
type Payment struct {
	ID                string
	OrgID             string
	OrderID           string
	RazorpayPaymentID *string
	Status            string
	// RawProviderData is nullable JSONB holding the raw Razorpay payment
	// payload, for audit/debugging.
	RawProviderData []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type PaymentRepo struct{}

func NewPaymentRepo() *PaymentRepo { return &PaymentRepo{} }

const paymentColumns = `id, org_id, order_id, razorpay_payment_id, status, raw_provider_data, created_at, updated_at`

// Create inserts a new payment attempt row.
func (r *PaymentRepo) Create(ctx context.Context, q Querier, p Payment) (*Payment, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO payments (org_id, order_id, razorpay_payment_id, status, raw_provider_data)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+paymentColumns,
		p.OrgID, p.OrderID, p.RazorpayPaymentID, p.Status, p.RawProviderData)
	payment, err := scanPayment(row)
	if err != nil {
		return nil, fmt.Errorf("models: create payment: %w", err)
	}
	return payment, nil
}

// UpdateStatus sets status/updated_at and conditionally
// razorpay_payment_id/raw_provider_data when the corresponding argument
// is non-nil (COALESCE leaves the existing value untouched rather than
// nulling it out when nil is passed).
func (r *PaymentRepo) UpdateStatus(ctx context.Context, q Querier, id, status string, razorpayPaymentID *string, rawProviderData []byte) (*Payment, error) {
	row := q.QueryRow(ctx, `
		UPDATE payments
		SET status = $2, updated_at = now(),
		    razorpay_payment_id = COALESCE($3, razorpay_payment_id),
		    raw_provider_data = COALESCE($4, raw_provider_data)
		WHERE id = $1
		RETURNING `+paymentColumns, id, status, razorpayPaymentID, rawProviderData)
	payment, err := scanPayment(row)
	if err != nil {
		return nil, fmt.Errorf("models: update payment status: %w", err)
	}
	return payment, nil
}

// GetByOrderID returns the most recently created payment for an order —
// an order has at most one payment attempt that matters for MVP (retries
// create a new payments row rather than reusing one); if multiple rows
// exist, the most recent wins.
func (r *PaymentRepo) GetByOrderID(ctx context.Context, q Querier, orderID string) (*Payment, error) {
	row := q.QueryRow(ctx, `
		SELECT `+paymentColumns+` FROM payments
		WHERE order_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, orderID)
	payment, err := scanPayment(row)
	if err != nil {
		return nil, fmt.Errorf("models: get payment by order id: %w", err)
	}
	return payment, nil
}

// GetByRazorpayPaymentID looks up the payment row matching a Razorpay
// payment-entity ID — used by the Task 8 worker's refund/dispute
// processing, which is handed a payment_id (not an order_id) in those
// webhook payloads. Returns ErrNotFound if no payment row has this
// Razorpay payment ID recorded yet.
func (r *PaymentRepo) GetByRazorpayPaymentID(ctx context.Context, q Querier, razorpayPaymentID string) (*Payment, error) {
	row := q.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payments WHERE razorpay_payment_id = $1`, razorpayPaymentID)
	payment, err := scanPayment(row)
	if err != nil {
		return nil, fmt.Errorf("models: get payment by razorpay payment id: %w", err)
	}
	return payment, nil
}

func scanPayment(row pgx.Row) (*Payment, error) {
	var p Payment
	if err := row.Scan(&p.ID, &p.OrgID, &p.OrderID, &p.RazorpayPaymentID, &p.Status, &p.RawProviderData,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan payment: %w", err)
	}
	return &p, nil
}
