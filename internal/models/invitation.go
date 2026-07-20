package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Invitation struct {
	ID              string
	OrgID           string
	Email           string
	Role            string
	InvitedByUserID string
	Status          string
	Token           string
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

type InvitationRepo struct{}

func NewInvitationRepo() *InvitationRepo { return &InvitationRepo{} }

func (r *InvitationRepo) Create(ctx context.Context, q Querier, orgID, email, role, invitedByUserID, token string, expiresAt time.Time) (*Invitation, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO invitations (org_id, email, role, invited_by_user_id, token, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, org_id, email, role, invited_by_user_id, status, token, created_at, expires_at
	`, orgID, email, role, invitedByUserID, token, expiresAt)

	var inv Invitation
	if err := row.Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedByUserID, &inv.Status, &inv.Token, &inv.CreatedAt, &inv.ExpiresAt); err != nil {
		return nil, fmt.Errorf("models: create invitation: %w", err)
	}
	return &inv, nil
}

func (r *InvitationRepo) ListPendingByOrg(ctx context.Context, q Querier, orgID string) ([]Invitation, error) {
	rows, err := q.Query(ctx, `
		SELECT id, org_id, email, role, invited_by_user_id, status, token, created_at, expires_at
		FROM invitations
		WHERE org_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list invitations: %w", err)
	}
	defer rows.Close()

	var out []Invitation
	for rows.Next() {
		var inv Invitation
		if err := rows.Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedByUserID, &inv.Status, &inv.Token, &inv.CreatedAt, &inv.ExpiresAt); err != nil {
			return nil, fmt.Errorf("models: scan invitation: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list invitations: %w", err)
	}
	return out, nil
}

func (r *InvitationRepo) Revoke(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM invitations WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("models: revoke invitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Accept calls the accept_invitation() SECURITY DEFINER SQL function: the
// caller isn't a member of the target org yet, so an ordinary
// memberships INSERT policy would reject them — the function validates
// the token/expiry/email match and creates the membership atomically.
func (r *InvitationRepo) Accept(ctx context.Context, q Querier, token string) (*Membership, error) {
	// Wrapped in a derived table for the same reason as OrgRepo.Create:
	// `SELECT (accept_invitation($1)).*` directly can invoke the function
	// once per output column, double-running its side effects.
	row := q.QueryRow(ctx, `SELECT (m).* FROM (SELECT accept_invitation($1) AS m) s`, token)

	var m Membership
	if err := row.Scan(&m.ID, &m.UserID, &m.OrgID, &m.Role, &m.JoinedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: accept invitation: %w", err)
	}
	return &m, nil
}

// Decline calls the decline_invitation() SECURITY DEFINER SQL function
// for the same reason Accept does: the decliner never becomes a member,
// so ordinary RLS on invitations would hide the row from them.
func (r *InvitationRepo) Decline(ctx context.Context, q Querier, token string) (*Invitation, error) {
	row := q.QueryRow(ctx, `SELECT (i).* FROM (SELECT decline_invitation($1) AS i) s`, token)

	var inv Invitation
	if err := row.Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedByUserID, &inv.Status, &inv.Token, &inv.CreatedAt, &inv.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: decline invitation: %w", err)
	}
	return &inv, nil
}
