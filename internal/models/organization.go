package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Organization struct {
	ID              string
	Slug            string
	Name            string
	CreatedByUserID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type OrgRepo struct{}

func NewOrgRepo() *OrgRepo { return &OrgRepo{} }

// Create calls the create_organization() SECURITY DEFINER SQL function,
// which atomically inserts the organization and its first owner
// membership — a plain INSERT policy can't do both in one statement, and
// without the membership row the org would be invisible to its own
// creator under RLS.
func (r *OrgRepo) Create(ctx context.Context, q Querier, name, slug string) (*Organization, error) {
	// The subquery wrapper matters: `SELECT (create_organization(...)).* `
	// directly can invoke the function once per output column under
	// Postgres's evaluation of function-returning-composite expansion,
	// silently double-inserting. Evaluating it once in a derived table and
	// projecting from that avoids the re-evaluation entirely.
	row := q.QueryRow(ctx, `SELECT (o).* FROM (SELECT create_organization($1, $2) AS o) s`, name, slug)

	var o Organization
	if err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedByUserID, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: create organization: %w", err)
	}
	return &o, nil
}

func (r *OrgRepo) GetBySlug(ctx context.Context, q Querier, slug string) (*Organization, error) {
	row := q.QueryRow(ctx, `
		SELECT id, slug, name, created_by_user_id, created_at, updated_at
		FROM organizations WHERE slug = $1
	`, slug)

	var o Organization
	if err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedByUserID, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get organization: %w", err)
	}
	return &o, nil
}

func (r *OrgRepo) Update(ctx context.Context, q Querier, id, name string) (*Organization, error) {
	row := q.QueryRow(ctx, `
		UPDATE organizations SET name = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, slug, name, created_by_user_id, created_at, updated_at
	`, id, name)

	var o Organization
	if err := row.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedByUserID, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update organization: %w", err)
	}
	return &o, nil
}

func (r *OrgRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete organization: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
