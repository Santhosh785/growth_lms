package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Entitlement statuses, matching the CHECK constraint in
// db/migrations/000006_commerce.up.sql.
const (
	EntitlementStatusActive  = "active"
	EntitlementStatusRevoked = "revoked"
	EntitlementStatusExpired = "expired"
)

// Entitlement is the actual "does this learner have access to this
// course" grant record that LearnerCourseAccess.EntitlementID points at
// once real commerce exists (db/migrations/000006_commerce.up.sql). It
// has no OfferID, Source, or RevokedAt field — Task 1's schema has no
// such columns. Whether a row is purchase-driven or an admin grant is
// derived from GrantedBy == nil (purchase-driven) vs. GrantedBy != nil
// (admin grant), not a separate enum; the offer, if needed, is derivable
// via OrderID -> orders.offer_id when OrderID is set; the revocation
// timestamp is UpdatedAt at the moment Status becomes "revoked".
//
// Creating a row here does not by itself grant the learner course
// access — the handler/webhook-processing code (Task 6 later subtasks)
// is responsible for also creating/updating the corresponding
// learner_course_access row via LearnerCourseAccessRepo.Create, in the
// same transaction.
type Entitlement struct {
	ID    string
	OrgID string
	// OrderID is NULL for admin-grant rows, set for purchase-driven rows.
	OrderID   *string
	LearnerID string
	CourseID  string
	Status    string
	// ExpiresAt is non-NULL only for fixed-term
	// (subscription-offer-sourced) entitlements.
	ExpiresAt *time.Time
	// GrantedBy is NULL for purchase-driven entitlements, set (granting
	// user) for admin grants.
	GrantedBy *string
	// GrantReason is required (handler-enforced) when GrantedBy is set.
	GrantReason *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type EntitlementRepo struct{}

func NewEntitlementRepo() *EntitlementRepo { return &EntitlementRepo{} }

const entitlementColumns = `id, org_id, order_id, learner_id, course_id, status, expires_at, granted_by, grant_reason, created_at, updated_at`

// Create inserts an entitlement row. The only two legitimate call sites
// are (a) verified-webhook payment-success processing (OrderID set,
// GrantedBy/GrantReason nil) and (b) an admin-grant handler (OrderID nil,
// GrantedBy set to the granting user's ID, GrantReason required
// non-empty — the handler validates GrantReason != "" before calling
// this, this repo does not re-validate it). Never call this from a
// browser-return-URL handler, only from verified-webhook processing or
// an explicit audit-logged admin action, per CLAUDE.md's non-negotiable
// rule that payment/enrollment access must only ever be granted after
// verified provider webhook events, never from browser return URLs.
func (r *EntitlementRepo) Create(ctx context.Context, q Querier, e Entitlement) (*Entitlement, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO entitlements (org_id, order_id, learner_id, course_id, status, expires_at, granted_by, grant_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+entitlementColumns,
		e.OrgID, e.OrderID, e.LearnerID, e.CourseID, e.Status, e.ExpiresAt, e.GrantedBy, e.GrantReason)
	out, err := scanEntitlement(row)
	if err != nil {
		return nil, fmt.Errorf("models: create entitlement: %w", err)
	}
	return out, nil
}

// Get returns a single entitlement by ID, or ErrNotFound.
func (r *EntitlementRepo) Get(ctx context.Context, q Querier, id string) (*Entitlement, error) {
	row := q.QueryRow(ctx, `SELECT `+entitlementColumns+` FROM entitlements WHERE id = $1`, id)
	out, err := scanEntitlement(row)
	if err != nil {
		return nil, fmt.Errorf("models: get entitlement: %w", err)
	}
	return out, nil
}

// GetByOrderID returns the entitlement created for a given order, or
// ErrNotFound if the order never succeeded (or predates entitlements
// entirely). Used by the Task 8 worker's refund/dispute-lost processing
// to find the entitlement to revoke — payment.captured processing creates
// at most one entitlement per order, so this is a single-row lookup, not
// a list.
func (r *EntitlementRepo) GetByOrderID(ctx context.Context, q Querier, orderID string) (*Entitlement, error) {
	row := q.QueryRow(ctx, `SELECT `+entitlementColumns+` FROM entitlements WHERE order_id = $1`, orderID)
	out, err := scanEntitlement(row)
	if err != nil {
		return nil, fmt.Errorf("models: get entitlement by order id: %w", err)
	}
	return out, nil
}

// ListByLearner returns every entitlement a learner holds, most recently
// created first.
func (r *EntitlementRepo) ListByLearner(ctx context.Context, q Querier, learnerID string) ([]*Entitlement, error) {
	rows, err := q.Query(ctx, `
		SELECT `+entitlementColumns+` FROM entitlements
		WHERE learner_id = $1
		ORDER BY created_at DESC
	`, learnerID)
	if err != nil {
		return nil, fmt.Errorf("models: list entitlements by learner: %w", err)
	}
	defer rows.Close()

	var out []*Entitlement
	for rows.Next() {
		e, err := scanEntitlementRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Revoke sets status = 'revoked', updated_at = now(). Called on a
// verified refund/chargeback webhook, or an equivalent admin action;
// does not itself touch learner_course_access — the caller updates that
// row too, in the same transaction, mirroring the relationship
// documented on the entitlements table above.
func (r *EntitlementRepo) Revoke(ctx context.Context, q Querier, id string) (*Entitlement, error) {
	row := q.QueryRow(ctx, `
		UPDATE entitlements SET status = 'revoked', updated_at = now()
		WHERE id = $1
		RETURNING `+entitlementColumns, id)
	out, err := scanEntitlement(row)
	if err != nil {
		return nil, fmt.Errorf("models: revoke entitlement: %w", err)
	}
	return out, nil
}

// Expire sets status = 'expired', updated_at = now(). Called by the
// expire-entitlements sweep worker job (Task 8) when a fixed-term
// entitlement's expires_at has passed; does not itself touch
// learner_course_access — the caller updates that row too, in the same
// transaction, same relationship documented on Revoke above.
func (r *EntitlementRepo) Expire(ctx context.Context, q Querier, id string) (*Entitlement, error) {
	row := q.QueryRow(ctx, `
		UPDATE entitlements SET status = 'expired', updated_at = now()
		WHERE id = $1
		RETURNING `+entitlementColumns, id)
	out, err := scanEntitlement(row)
	if err != nil {
		return nil, fmt.Errorf("models: expire entitlement: %w", err)
	}
	return out, nil
}

// ListExpiringBefore returns every active, fixed-term entitlement whose
// expires_at is before cutoff — used by the expiry-sweep worker job
// (Task 8) to find fixed-term passes that need to flip to 'expired'.
func (r *EntitlementRepo) ListExpiringBefore(ctx context.Context, q Querier, cutoff time.Time) ([]*Entitlement, error) {
	rows, err := q.Query(ctx, `
		SELECT `+entitlementColumns+` FROM entitlements
		WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at < $1
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("models: list entitlements expiring before cutoff: %w", err)
	}
	defer rows.Close()

	var out []*Entitlement
	for rows.Next() {
		e, err := scanEntitlementRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEntitlement(row pgx.Row) (*Entitlement, error) {
	var e Entitlement
	if err := row.Scan(&e.ID, &e.OrgID, &e.OrderID, &e.LearnerID, &e.CourseID, &e.Status, &e.ExpiresAt,
		&e.GrantedBy, &e.GrantReason, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan entitlement: %w", err)
	}
	return &e, nil
}

func scanEntitlementRows(rows pgx.Rows) (*Entitlement, error) {
	var e Entitlement
	if err := rows.Scan(&e.ID, &e.OrgID, &e.OrderID, &e.LearnerID, &e.CourseID, &e.Status, &e.ExpiresAt,
		&e.GrantedBy, &e.GrantReason, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan entitlement: %w", err)
	}
	return &e, nil
}
