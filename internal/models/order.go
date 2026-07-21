package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Order status constants, matching the CHECK constraint in
// db/migrations/000006_commerce.up.sql — exactly these five, no
// refunded/disputed. A refund or chargeback never changes an order's
// status: an order that was succeeded stays succeeded even after a later
// refund; the refund/chargeback's own effect on revenue is tracked via
// the refunds/chargebacks rows themselves and rolled up at query time by
// the reporting code, not by mutating the order.
const (
	OrderStatusPending          = "pending"
	OrderStatusPaymentInitiated = "payment_initiated"
	OrderStatusSucceeded        = "succeeded"
	OrderStatusFailed           = "failed"
	OrderStatusAbandoned        = "abandoned"
)

// Order is a single checkout attempt against an offer
// (db/migrations/000006_commerce.up.sql). It has no invite_token_id
// column — an invitation-only offer's gating is enforced entirely by
// commerce_invite_tokens.used_at before order creation is even allowed;
// the order itself doesn't need a back-reference.
type Order struct {
	ID        string
	OrgID     string
	OfferID   string
	LearnerID string
	Currency  string
	// Subtotal/DiscountAmount/TaxAmount/CommissionAmount/Total are all
	// NUMERIC(12,2) amounts in Currency.
	Subtotal         float64
	DiscountAmount   float64
	TaxAmount        float64
	CommissionAmount float64
	Total            float64
	// CommissionRateSnapshot is the platform_settings.commission_percent
	// value at order-creation time — commission is snapshotted per order,
	// not looked up live later, so a later change to the platform-wide
	// rate never retroactively alters historical orders' math.
	CommissionRateSnapshot float64
	Status                 string
	RazorpayOrderID        *string
	DiscountCodeID         *string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type OrderRepo struct{}

func NewOrderRepo() *OrderRepo { return &OrderRepo{} }

const orderColumns = `id, org_id, offer_id, learner_id, currency, subtotal, discount_amount, tax_amount, commission_amount, total, commission_rate_snapshot, status, razorpay_order_id, discount_code_id, created_at, updated_at`

// Create inserts a new order with status = 'pending' regardless of any
// Status field the caller set (forced server-side in the SQL below, the
// struct field is not trusted on insert). Every amount/currency/
// commission field must come from server-side computation the handler
// already performed (offer price + tax rate, discount code lookup,
// current platform_settings.commission_percent) — this repo does not
// itself compute pricing, it only persists numbers the caller computed,
// and per CLAUDE.md's non-negotiable rule it must never read amounts
// from client-controlled request bodies.
func (r *OrderRepo) Create(ctx context.Context, q Querier, o Order) (*Order, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO orders (org_id, offer_id, learner_id, currency, subtotal, discount_amount, tax_amount, commission_amount, total, commission_rate_snapshot, status, razorpay_order_id, discount_code_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', $11, $12)
		RETURNING `+orderColumns,
		o.OrgID, o.OfferID, o.LearnerID, o.Currency, o.Subtotal, o.DiscountAmount, o.TaxAmount, o.CommissionAmount, o.Total, o.CommissionRateSnapshot, o.RazorpayOrderID, o.DiscountCodeID)
	order, err := scanOrder(row)
	if err != nil {
		return nil, fmt.Errorf("models: create order: %w", err)
	}
	return order, nil
}

// Get returns a single order by ID, or ErrNotFound.
func (r *OrderRepo) Get(ctx context.Context, q Querier, id string) (*Order, error) {
	row := q.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1`, id)
	order, err := scanOrder(row)
	if err != nil {
		return nil, fmt.Errorf("models: get order: %w", err)
	}
	return order, nil
}

// UpdateStatus sets status/updated_at. No succeeded_at column exists —
// Task 1's schema tracks the success moment via updated_at at the point
// status becomes succeeded, not a dedicated timestamp column. No
// state-machine validation here (like CourseRepo, the valid-transition
// graph lives in the handler/webhook processing code, not the repo).
func (r *OrderRepo) UpdateStatus(ctx context.Context, q Querier, id, status string) (*Order, error) {
	row := q.QueryRow(ctx, `
		UPDATE orders SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+orderColumns, id, status)
	order, err := scanOrder(row)
	if err != nil {
		return nil, fmt.Errorf("models: update order status: %w", err)
	}
	return order, nil
}

// AttachRazorpayOrder sets razorpay_order_id and status (e.g.
// 'payment_initiated') in one UPDATE, for the paid-checkout path
// immediately after Provider.CreateOrder returns a Razorpay order ID.
// Added by Task 6 (commerce-handlers): OrderRepo.Create/UpdateStatus
// alone give no way to attach the Razorpay order id after the initial
// insert.
func (r *OrderRepo) AttachRazorpayOrder(ctx context.Context, q Querier, id, razorpayOrderID, status string) (*Order, error) {
	row := q.QueryRow(ctx, `
		UPDATE orders SET razorpay_order_id = $2, status = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+orderColumns, id, razorpayOrderID, status)
	order, err := scanOrder(row)
	if err != nil {
		return nil, fmt.Errorf("models: attach razorpay order: %w", err)
	}
	return order, nil
}

// CountByOfferAndStatus counts orders for an offer currently in status —
// used by the cohort-offer seat-cap check (create-order/checkout-page
// handlers) to compare against offers.max_seats. Added by Task 6
// (commerce-handlers); counts orders rather than entitlements because a
// cohort seat is considered claimed the moment a successful order exists
// for it, mirroring how this file already treats "succeeded" as the
// terminal payment-success state.
func (r *OrderRepo) CountByOfferAndStatus(ctx context.Context, q Querier, offerID, status string) (int, error) {
	var count int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM orders WHERE offer_id = $1 AND status = $2`, offerID, status).Scan(&count); err != nil {
		return 0, fmt.Errorf("models: count orders by offer and status: %w", err)
	}
	return count, nil
}

// GetByRazorpayOrderID looks up the order that matches a Razorpay
// order-entity ID from a payment webhook payload — the Task 8 worker's
// entry point for resolving which order a payment.captured/payment.failed
// event belongs to. Returns ErrNotFound if no order was ever created with
// this Razorpay order ID.
func (r *OrderRepo) GetByRazorpayOrderID(ctx context.Context, q Querier, razorpayOrderID string) (*Order, error) {
	row := q.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE razorpay_order_id = $1`, razorpayOrderID)
	order, err := scanOrder(row)
	if err != nil {
		return nil, fmt.Errorf("models: get order by razorpay order id: %w", err)
	}
	return order, nil
}

// ListPendingOlderThan returns every order still 'pending' or
// 'payment_initiated' whose created_at is older than now()-cutoff — used
// by the abandon-sweep worker job (Task 8) to find orders to flip to
// 'abandoned'. An order that already reached a terminal status
// (succeeded/failed/abandoned) never matches, regardless of age.
func (r *OrderRepo) ListPendingOlderThan(ctx context.Context, q Querier, cutoff time.Duration) ([]*Order, error) {
	rows, err := q.Query(ctx, `
		SELECT `+orderColumns+` FROM orders
		WHERE status IN ('pending', 'payment_initiated') AND created_at < now() - $1::interval
	`, fmt.Sprintf("%d seconds", int(cutoff.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("models: list pending orders older than cutoff: %w", err)
	}
	defer rows.Close()

	var out []*Order
	for rows.Next() {
		o, err := scanOrderRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListByOrg returns every order for an org created within [from, to),
// ordered most-recent-first — for the admin dashboard/revenue reporting
// (Task 9).
func (r *OrderRepo) ListByOrg(ctx context.Context, q Querier, orgID string, from, to time.Time) ([]*Order, error) {
	rows, err := q.Query(ctx, `
		SELECT `+orderColumns+` FROM orders
		WHERE org_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC
	`, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("models: list orders by org: %w", err)
	}
	defer rows.Close()

	var out []*Order
	for rows.Next() {
		o, err := scanOrderRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanOrder(row pgx.Row) (*Order, error) {
	var o Order
	if err := row.Scan(&o.ID, &o.OrgID, &o.OfferID, &o.LearnerID, &o.Currency, &o.Subtotal, &o.DiscountAmount,
		&o.TaxAmount, &o.CommissionAmount, &o.Total, &o.CommissionRateSnapshot, &o.Status, &o.RazorpayOrderID,
		&o.DiscountCodeID, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan order: %w", err)
	}
	return &o, nil
}

func scanOrderRows(rows pgx.Rows) (*Order, error) {
	var o Order
	if err := rows.Scan(&o.ID, &o.OrgID, &o.OfferID, &o.LearnerID, &o.Currency, &o.Subtotal, &o.DiscountAmount,
		&o.TaxAmount, &o.CommissionAmount, &o.Total, &o.CommissionRateSnapshot, &o.Status, &o.RazorpayOrderID,
		&o.DiscountCodeID, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan order: %w", err)
	}
	return &o, nil
}
