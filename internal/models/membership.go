package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Membership struct {
	ID       string
	UserID   string
	OrgID    string
	Role     string
	JoinedAt time.Time
}

// MembershipWithProfile is a membership row joined with the member's
// profile, for listing an organization's roster.
type MembershipWithProfile struct {
	Membership
	Email    string
	FullName *string
}

type MembershipRepo struct{}

func NewMembershipRepo() *MembershipRepo { return &MembershipRepo{} }

func (r *MembershipRepo) GetRole(ctx context.Context, q Querier, userID, orgID string) (string, error) {
	row := q.QueryRow(ctx, `SELECT role FROM memberships WHERE user_id = $1 AND org_id = $2`, userID, orgID)
	var role string
	if err := row.Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("models: get membership role: %w", err)
	}
	return role, nil
}

func (r *MembershipRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]MembershipWithProfile, error) {
	rows, err := q.Query(ctx, `
		SELECT m.id, m.user_id, m.org_id, m.role, m.joined_at, p.email, p.full_name
		FROM memberships m
		JOIN profiles p ON p.id = m.user_id
		WHERE m.org_id = $1
		ORDER BY m.joined_at ASC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list memberships: %w", err)
	}
	defer rows.Close()

	var out []MembershipWithProfile
	for rows.Next() {
		var m MembershipWithProfile
		if err := rows.Scan(&m.ID, &m.UserID, &m.OrgID, &m.Role, &m.JoinedAt, &m.Email, &m.FullName); err != nil {
			return nil, fmt.Errorf("models: scan membership: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list memberships: %w", err)
	}
	return out, nil
}

// OwnedOrg is one organization a user has the "owner" role in, for
// populating cross-page nav links to that org's admin dashboard.
type OwnedOrg struct {
	Slug string
	Name string
}

// ListOwnedByUser returns the organizations the given user is an "owner"
// of (slug + name only), for the shared nav's per-org admin links.
func (r *MembershipRepo) ListOwnedByUser(ctx context.Context, q Querier, userID string) ([]OwnedOrg, error) {
	rows, err := q.Query(ctx, `
		SELECT o.slug, o.name
		FROM memberships m
		JOIN organizations o ON o.id = m.org_id
		WHERE m.user_id = $1 AND m.role = 'owner'
		ORDER BY o.name ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("models: list owned orgs: %w", err)
	}
	defer rows.Close()

	var out []OwnedOrg
	for rows.Next() {
		var o OwnedOrg
		if err := rows.Scan(&o.Slug, &o.Name); err != nil {
			return nil, fmt.Errorf("models: scan owned org: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list owned orgs: %w", err)
	}
	return out, nil
}

// CountByOrg returns the number of members in an org — added by Task 9
// (admin-dashboard) for the platform-owner cross-org list, preferred over
// len(ListByOrg(...)) so the platform-wide dashboard doesn't have to load
// every org's full roster (with a profile join) just to count rows.
func (r *MembershipRepo) CountByOrg(ctx context.Context, q Querier, orgID string) (int, error) {
	var count int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE org_id = $1`, orgID).Scan(&count); err != nil {
		return 0, fmt.Errorf("models: count memberships by org: %w", err)
	}
	return count, nil
}

func (r *MembershipRepo) UpdateRole(ctx context.Context, q Querier, userID, orgID, role string) error {
	tag, err := q.Exec(ctx, `UPDATE memberships SET role = $3 WHERE user_id = $1 AND org_id = $2`, userID, orgID, role)
	if err != nil {
		return fmt.Errorf("models: update membership role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *MembershipRepo) Remove(ctx context.Context, q Querier, userID, orgID string) error {
	tag, err := q.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1 AND org_id = $2`, userID, orgID)
	if err != nil {
		return fmt.Errorf("models: remove membership: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
