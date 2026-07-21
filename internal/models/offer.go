package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Offer is a purchasable/enrollable variant of a course
// (db/migrations/000006_commerce.up.sql). It has no Title/Description
// field — Task 1's schema has no such columns; an offer's display copy
// comes from its course (join CourseID to courses when rendering).
type Offer struct {
	ID       string
	OrgID    string
	CourseID string
	Type     string
	// Price is NUMERIC(12,2); 0 for "free" offers. Currency states which
	// currency this amount is in.
	Price    float64
	Currency string
	// TaxRatePercent is NUMERIC(5,2); a rate (e.g. 18 for 18%), never a
	// money amount.
	TaxRatePercent float64
	// AccessDurationDays is non-NULL only for "subscription" (fixed-term
	// pass) offers.
	AccessDurationDays *int
	// MaxSeats/EnrollmentStartsAt/EnrollmentEndsAt are non-NULL only for
	// "cohort" offers.
	MaxSeats           *int
	EnrollmentStartsAt *time.Time
	EnrollmentEndsAt   *time.Time
	Status             string
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Offer type/status values, matching the CHECK constraints in
// db/migrations/000006_commerce.up.sql.
const (
	OfferTypeFree           = "free"
	OfferTypePaid           = "paid"
	OfferTypeSubscription   = "subscription"
	OfferTypeCohort         = "cohort"
	OfferTypeInvitationOnly = "invitation_only"

	OfferStatusActive   = "active"
	OfferStatusArchived = "archived"
)

type OfferRepo struct{}

func NewOfferRepo() *OfferRepo { return &OfferRepo{} }

const offerColumns = `id, org_id, course_id, type, price, currency, tax_rate_percent, access_duration_days, max_seats, enrollment_starts_at, enrollment_ends_at, status, created_by, created_at, updated_at`

// Create inserts a new offer. ID/CreatedAt/UpdatedAt are DB-generated/
// defaulted; every other field on o is persisted as given.
func (r *OfferRepo) Create(ctx context.Context, q Querier, o Offer) (*Offer, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO offers (org_id, course_id, type, price, currency, tax_rate_percent, access_duration_days, max_seats, enrollment_starts_at, enrollment_ends_at, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+offerColumns,
		o.OrgID, o.CourseID, o.Type, o.Price, o.Currency, o.TaxRatePercent, o.AccessDurationDays, o.MaxSeats, o.EnrollmentStartsAt, o.EnrollmentEndsAt, o.Status, o.CreatedBy)
	offer, err := scanOffer(row)
	if err != nil {
		return nil, fmt.Errorf("models: create offer: %w", err)
	}
	return offer, nil
}

// Get returns a single offer by ID, or ErrNotFound.
func (r *OfferRepo) Get(ctx context.Context, q Querier, id string) (*Offer, error) {
	row := q.QueryRow(ctx, `SELECT `+offerColumns+` FROM offers WHERE id = $1`, id)
	offer, err := scanOffer(row)
	if err != nil {
		return nil, fmt.Errorf("models: get offer: %w", err)
	}
	return offer, nil
}

// ListByCourse returns every offer (any status) for a course, ordered by
// creation time. The caller (checkout handler) filters by status/
// enrollment window as needed — this method does not itself decide which
// offers are currently purchasable.
func (r *OfferRepo) ListByCourse(ctx context.Context, q Querier, courseID string) ([]*Offer, error) {
	rows, err := q.Query(ctx, `SELECT `+offerColumns+` FROM offers WHERE course_id = $1 ORDER BY created_at`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list offers by course: %w", err)
	}
	defer rows.Close()

	var out []*Offer
	for rows.Next() {
		o, err := scanOfferRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Update updates the mutable pricing/tax/availability fields only; it
// never touches Status (use Archive for that transition).
func (r *OfferRepo) Update(ctx context.Context, q Querier, id string, o Offer) (*Offer, error) {
	row := q.QueryRow(ctx, `
		UPDATE offers
		SET price = $2, currency = $3, tax_rate_percent = $4, access_duration_days = $5, max_seats = $6, enrollment_starts_at = $7, enrollment_ends_at = $8, updated_at = now()
		WHERE id = $1
		RETURNING `+offerColumns,
		id, o.Price, o.Currency, o.TaxRatePercent, o.AccessDurationDays, o.MaxSeats, o.EnrollmentStartsAt, o.EnrollmentEndsAt)
	offer, err := scanOffer(row)
	if err != nil {
		return nil, fmt.Errorf("models: update offer: %w", err)
	}
	return offer, nil
}

// Archive sets status = 'archived'. An archived offer is excluded from
// new checkouts by the handler layer (this repo does not enforce that);
// ListByCourse still returns archived rows so existing purchasers/admin
// views can see them.
func (r *OfferRepo) Archive(ctx context.Context, q Querier, id string) (*Offer, error) {
	row := q.QueryRow(ctx, `
		UPDATE offers SET status = 'archived', updated_at = now()
		WHERE id = $1
		RETURNING `+offerColumns, id)
	offer, err := scanOffer(row)
	if err != nil {
		return nil, fmt.Errorf("models: archive offer: %w", err)
	}
	return offer, nil
}

func scanOffer(row pgx.Row) (*Offer, error) {
	var o Offer
	if err := row.Scan(&o.ID, &o.OrgID, &o.CourseID, &o.Type, &o.Price, &o.Currency, &o.TaxRatePercent,
		&o.AccessDurationDays, &o.MaxSeats, &o.EnrollmentStartsAt, &o.EnrollmentEndsAt, &o.Status,
		&o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan offer: %w", err)
	}
	return &o, nil
}

func scanOfferRows(rows pgx.Rows) (*Offer, error) {
	var o Offer
	if err := rows.Scan(&o.ID, &o.OrgID, &o.CourseID, &o.Type, &o.Price, &o.Currency, &o.TaxRatePercent,
		&o.AccessDurationDays, &o.MaxSeats, &o.EnrollmentStartsAt, &o.EnrollmentEndsAt, &o.Status,
		&o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan offer: %w", err)
	}
	return &o, nil
}
