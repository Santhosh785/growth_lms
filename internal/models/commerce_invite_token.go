package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// InviteToken is a single-use invitation-only offer access token
// (db/migrations/000006_commerce.up.sql).
type InviteToken struct {
	ID      string
	OrgID   string
	OfferID string
	Token   string
	// BoundEmail is nullable; when set, only this email may redeem the
	// token. This repo does not check it — that requires the
	// authenticated learner's email, which this repo has no access to;
	// the checkout handler compares BoundEmail (if non-nil) against the
	// session's email itself.
	BoundEmail      *string
	UsedAt          *time.Time
	UsedByLearnerID *string
	UsedByOrderID   *string
	ExpiresAt       *time.Time
	CreatedBy       string
	CreatedAt       time.Time
}

// ErrInviteTokenUsed/ErrInviteTokenExpired are returned by GetByToken
// instead of the row itself when the token exists but isn't currently
// redeemable — distinct from ErrNotFound, which means the token string
// doesn't exist at all.
var (
	ErrInviteTokenUsed    = errors.New("models: invite token already used")
	ErrInviteTokenExpired = errors.New("models: invite token expired")
)

type InviteTokenRepo struct{}

func NewInviteTokenRepo() *InviteTokenRepo { return &InviteTokenRepo{} }

const inviteTokenColumns = `id, org_id, offer_id, token, bound_email, used_at, used_by_learner_id, used_by_order_id, expires_at, created_by, created_at`

// Create persists an invite token. Token string generation (random,
// unguessable) is the caller's responsibility; this method just persists
// it.
func (r *InviteTokenRepo) Create(ctx context.Context, q Querier, orgID, offerID, createdBy, token string, boundEmail *string, expiresAt *time.Time) (*InviteToken, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO commerce_invite_tokens (org_id, offer_id, token, bound_email, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+inviteTokenColumns,
		orgID, offerID, token, boundEmail, expiresAt, createdBy)
	it, err := scanInviteToken(row)
	if err != nil {
		return nil, fmt.Errorf("models: create invite token: %w", err)
	}
	return it, nil
}

// GetByToken looks up the row by token, returns ErrNotFound if no row
// matches, then validates in Go (not SQL) and returns the appropriate
// sentinel instead of the row when invalid: ErrInviteTokenUsed if
// used_at is set, ErrInviteTokenExpired if expires_at is non-nil and in
// the past. Returns (*InviteToken, nil) only when the token exists, is
// unused, and is unexpired — callers can treat a non-nil, non-error
// return as "this token is currently redeemable" without re-checking the
// fields themselves.
func (r *InviteTokenRepo) GetByToken(ctx context.Context, q Querier, token string) (*InviteToken, error) {
	row := q.QueryRow(ctx, `SELECT `+inviteTokenColumns+` FROM commerce_invite_tokens WHERE token = $1`, token)
	it, err := scanInviteToken(row)
	if err != nil {
		return nil, fmt.Errorf("models: get invite token by token: %w", err)
	}
	if it.UsedAt != nil {
		return nil, ErrInviteTokenUsed
	}
	if it.ExpiresAt != nil && it.ExpiresAt.Before(time.Now()) {
		return nil, ErrInviteTokenExpired
	}
	return it, nil
}

// MarkUsed sets used_at/used_by_learner_id/used_by_order_id. Unlike
// DiscountCodeRepo.IncrementRedemption, this is called at order-creation
// time for BOTH the free and paid checkout paths, not deferred to
// webhook-confirmed payment success — orders carries a discount_code_id
// column the webhook worker can look a discount code back up by post-
// payment, but Task 4's schema deliberately has no invite_token_id column
// on orders, so there is no way for the worker to resolve which token
// gated a given order after the fact. Marking it used here is therefore
// the only structurally possible point; a paid order that later fails or
// is abandoned still permanently burns the token, an accepted consequence
// of that schema choice.
func (r *InviteTokenRepo) MarkUsed(ctx context.Context, q Querier, id, learnerID, orderID string) (*InviteToken, error) {
	row := q.QueryRow(ctx, `
		UPDATE commerce_invite_tokens
		SET used_at = now(), used_by_learner_id = $2, used_by_order_id = $3
		WHERE id = $1
		RETURNING `+inviteTokenColumns, id, learnerID, orderID)
	it, err := scanInviteToken(row)
	if err != nil {
		return nil, fmt.Errorf("models: mark invite token used: %w", err)
	}
	return it, nil
}

// ListByOffer returns every invite token issued for an offer (used and
// unused), most recently created first. Added by Task 6
// (commerce-handlers) for the GET .../offers/:offerId/invite-tokens
// listing endpoint — callers must not re-expose the Token field for
// already-issued rows, per that endpoint's doc comment; this repo method
// returns the full row (including Token) purely because that's the
// simplest scan to write, the redaction is the handler's responsibility.
func (r *InviteTokenRepo) ListByOffer(ctx context.Context, q Querier, offerID string) ([]*InviteToken, error) {
	rows, err := q.Query(ctx, `SELECT `+inviteTokenColumns+` FROM commerce_invite_tokens WHERE offer_id = $1 ORDER BY created_at DESC`, offerID)
	if err != nil {
		return nil, fmt.Errorf("models: list invite tokens by offer: %w", err)
	}
	defer rows.Close()

	var out []*InviteToken
	for rows.Next() {
		var it InviteToken
		if err := rows.Scan(&it.ID, &it.OrgID, &it.OfferID, &it.Token, &it.BoundEmail, &it.UsedAt,
			&it.UsedByLearnerID, &it.UsedByOrderID, &it.ExpiresAt, &it.CreatedBy, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("models: scan invite token: %w", err)
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

func scanInviteToken(row pgx.Row) (*InviteToken, error) {
	var it InviteToken
	if err := row.Scan(&it.ID, &it.OrgID, &it.OfferID, &it.Token, &it.BoundEmail, &it.UsedAt,
		&it.UsedByLearnerID, &it.UsedByOrderID, &it.ExpiresAt, &it.CreatedBy, &it.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan invite token: %w", err)
	}
	return &it, nil
}
