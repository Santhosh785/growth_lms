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
	//
	// Columns are named explicitly (not `(o).*`) so a later ALTER TABLE ...
	// ADD COLUMN on organizations (e.g. Task 4's bunny_library_id) can't
	// silently shift this Scan out of sync with the row shape.
	row := q.QueryRow(ctx, `
		SELECT (o).id, (o).slug, (o).name, (o).created_by_user_id, (o).created_at, (o).updated_at
		FROM (SELECT create_organization($1, $2) AS o) s
	`, name, slug)

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

// GetBunnyLibraryID returns the org's Bunny Stream library ID, or "" if
// one hasn't been provisioned yet (spec: lazily provisioned on first
// video upload — orgs that never upload video never provision a
// library).
func (r *OrgRepo) GetBunnyLibraryID(ctx context.Context, q Querier, orgID string) (string, error) {
	var libraryID *string
	if err := q.QueryRow(ctx, `SELECT bunny_library_id FROM organizations WHERE id = $1`, orgID).Scan(&libraryID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("models: get bunny library id: %w", err)
	}
	if libraryID == nil {
		return "", nil
	}
	return *libraryID, nil
}

// ListAll returns every organization on the platform, no org_id filter —
// added by Task 9 (admin-dashboard) for the platform-owner cross-org
// dashboard. organizations_select's RLS policy already carries an
// `OR app_is_platform_owner()` clause (see
// db/migrations/000002_auth_tenancy.up.sql), so this plain SELECT is
// visible to a platform-owner-authorized session without any
// service-role bypass — the HTTP layer's middleware.RequirePlatformOwner
// gate is what makes calling this safe, not anything in this query
// itself. Callers MUST NOT call this from a non-platform-owner session.
func (r *OrgRepo) ListAll(ctx context.Context, q Querier) ([]*Organization, error) {
	rows, err := q.Query(ctx, `
		SELECT id, slug, name, created_by_user_id, created_at, updated_at
		FROM organizations
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("models: list all organizations: %w", err)
	}
	defer rows.Close()

	var out []*Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.CreatedByUserID, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("models: scan organization: %w", err)
		}
		out = append(out, &o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list all organizations: %w", err)
	}
	return out, nil
}

// SetBunnyLibraryID persists a newly provisioned Bunny Stream library ID
// for an org. Only ever called once per org (the first video upload finds
// GetBunnyLibraryID returning "" and calls this immediately after
// media.BunnyClient.CreateLibrary succeeds).
func (r *OrgRepo) SetBunnyLibraryID(ctx context.Context, q Querier, orgID, libraryID string) error {
	tag, err := q.Exec(ctx, `UPDATE organizations SET bunny_library_id = $2, updated_at = now() WHERE id = $1`, orgID, libraryID)
	if err != nil {
		return fmt.Errorf("models: set bunny library id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
