package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DiscountCode is a per-offer discount coupon
// (db/migrations/000006_commerce.up.sql). It has no Status/UpdatedAt
// field — Task 1's schema has no such columns. A code is deactivated by
// the handler layer setting ExpiresAt to a past timestamp rather than a
// separate status flag.
type DiscountCode struct {
	ID      string
	OrgID   string
	OfferID string
	Code    string
	// DiscountType is "percent" or "fixed".
	DiscountType string
	// Value is NUMERIC(12,2): a money amount if DiscountType == "fixed", a
	// percent (0-100) if DiscountType == "percent".
	Value           float64
	ExpiresAt       *time.Time
	MaxRedemptions  *int
	RedemptionCount int
	CreatedBy       string
	CreatedAt       time.Time
}

// Discount type values, matching the CHECK constraint in
// db/migrations/000006_commerce.up.sql.
const (
	DiscountTypePercent = "percent"
	DiscountTypeFixed   = "fixed"
)

// ErrDiscountCodeExhausted is returned by IncrementRedemption when a
// code's redemption cap has already been reached (or the row vanished
// mid-request) — distinct from ErrNotFound because callers are expected
// to have already resolved the code via GetByCode earlier in the same
// request, so a miss here specifically means the cap was hit.
var ErrDiscountCodeExhausted = errors.New("models: discount code redemption cap reached")

type DiscountCodeRepo struct{}

func NewDiscountCodeRepo() *DiscountCodeRepo { return &DiscountCodeRepo{} }

const discountCodeColumns = `id, org_id, offer_id, code, discount_type, value, expires_at, max_redemptions, redemption_count, created_by, created_at`

// Create inserts a new discount code.
func (r *DiscountCodeRepo) Create(ctx context.Context, q Querier, d DiscountCode) (*DiscountCode, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO discount_codes (org_id, offer_id, code, discount_type, value, expires_at, max_redemptions, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+discountCodeColumns,
		d.OrgID, d.OfferID, d.Code, d.DiscountType, d.Value, d.ExpiresAt, d.MaxRedemptions, d.CreatedBy)
	dc, err := scanDiscountCode(row)
	if err != nil {
		return nil, fmt.Errorf("models: create discount code: %w", err)
	}
	return dc, nil
}

// Get returns a single discount code by ID, or ErrNotFound. Added by
// Task 6 (commerce-handlers) for the deactivate endpoint, which is handed
// a :discountId path param rather than an offer+code pair.
func (r *DiscountCodeRepo) Get(ctx context.Context, q Querier, id string) (*DiscountCode, error) {
	row := q.QueryRow(ctx, `SELECT `+discountCodeColumns+` FROM discount_codes WHERE id = $1`, id)
	dc, err := scanDiscountCode(row)
	if err != nil {
		return nil, fmt.Errorf("models: get discount code: %w", err)
	}
	return dc, nil
}

// ListByOffer returns every discount code for an offer, most recently
// created first. Added by Task 6 (commerce-handlers) for the
// GET .../offers/:offerId/discounts listing endpoint.
func (r *DiscountCodeRepo) ListByOffer(ctx context.Context, q Querier, offerID string) ([]*DiscountCode, error) {
	rows, err := q.Query(ctx, `SELECT `+discountCodeColumns+` FROM discount_codes WHERE offer_id = $1 ORDER BY created_at DESC`, offerID)
	if err != nil {
		return nil, fmt.Errorf("models: list discount codes by offer: %w", err)
	}
	defer rows.Close()

	var out []*DiscountCode
	for rows.Next() {
		dc, err := scanDiscountCodeRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	return out, rows.Err()
}

// Deactivate stops future redemptions of a code by setting expires_at to
// now() (in the past the instant this returns), per this struct's doc
// comment: there is no separate status column, so "deactivated" is
// represented as "already expired". Does not affect orders that already
// redeemed it (RedemptionCount/prior redemptions are untouched). Added by
// Task 6 (commerce-handlers).
func (r *DiscountCodeRepo) Deactivate(ctx context.Context, q Querier, id string) (*DiscountCode, error) {
	row := q.QueryRow(ctx, `
		UPDATE discount_codes SET expires_at = now()
		WHERE id = $1
		RETURNING `+discountCodeColumns, id)
	dc, err := scanDiscountCode(row)
	if err != nil {
		return nil, fmt.Errorf("models: deactivate discount code: %w", err)
	}
	return dc, nil
}

// GetByCode looks up a discount code scoped to a single offer — a code
// string is only unique within its offer, so callers must always know
// which offer they're checking out. Returns ErrNotFound if no row
// matches. Does not itself validate expires_at/redemption_count — the
// checkout handler decides what to do with an expired/exhausted code it
// got back, since those are presentation decisions, not existence.
func (r *DiscountCodeRepo) GetByCode(ctx context.Context, q Querier, offerID, code string) (*DiscountCode, error) {
	row := q.QueryRow(ctx, `SELECT `+discountCodeColumns+` FROM discount_codes WHERE offer_id = $1 AND code = $2`, offerID, code)
	dc, err := scanDiscountCode(row)
	if err != nil {
		return nil, fmt.Errorf("models: get discount code by code: %w", err)
	}
	return dc, nil
}

// IncrementRedemption atomically checks-and-increments redemption_count,
// closing the race between two concurrent checkouts both reading
// redemption_count < max_redemptions as true and both incrementing.
// max_redemptions IS NULL means unlimited redemptions — without that
// clause a NULL cap would make the comparison evaluate to unknown/false
// and incorrectly block every redemption of an uncapped code. Call this
// only after the payment is confirmed (inside the same webhook-processing
// transaction that creates the payments/entitlements rows), never at
// checkout-order creation time, so an abandoned/failed order never
// permanently burns a redemption.
func (r *DiscountCodeRepo) IncrementRedemption(ctx context.Context, q Querier, id string) (*DiscountCode, error) {
	row := q.QueryRow(ctx, `
		UPDATE discount_codes
		SET redemption_count = redemption_count + 1
		WHERE id = $1 AND (max_redemptions IS NULL OR redemption_count < max_redemptions)
		RETURNING `+discountCodeColumns, id)

	var d DiscountCode
	if err := row.Scan(&d.ID, &d.OrgID, &d.OfferID, &d.Code, &d.DiscountType, &d.Value, &d.ExpiresAt,
		&d.MaxRedemptions, &d.RedemptionCount, &d.CreatedBy, &d.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDiscountCodeExhausted
		}
		return nil, fmt.Errorf("models: increment discount code redemption: %w", err)
	}
	return &d, nil
}

func scanDiscountCode(row pgx.Row) (*DiscountCode, error) {
	var d DiscountCode
	if err := row.Scan(&d.ID, &d.OrgID, &d.OfferID, &d.Code, &d.DiscountType, &d.Value, &d.ExpiresAt,
		&d.MaxRedemptions, &d.RedemptionCount, &d.CreatedBy, &d.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan discount code: %w", err)
	}
	return &d, nil
}

func scanDiscountCodeRows(rows pgx.Rows) (*DiscountCode, error) {
	var d DiscountCode
	if err := rows.Scan(&d.ID, &d.OrgID, &d.OfferID, &d.Code, &d.DiscountType, &d.Value, &d.ExpiresAt,
		&d.MaxRedemptions, &d.RedemptionCount, &d.CreatedBy, &d.CreatedAt); err != nil {
		return nil, fmt.Errorf("models: scan discount code: %w", err)
	}
	return &d, nil
}
