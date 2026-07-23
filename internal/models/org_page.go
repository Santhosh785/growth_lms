package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OrgPage is one entry in an org's landing-page builder (plan.md Task
// 8's "landing-page builder or configurable public pages"). Slug "home"
// is the org's public landing page by convention; other slugs are
// additional public pages (e.g. "about", "pricing").
type OrgPage struct {
	ID          string
	OrgID       string
	Slug        string
	Title       string
	ContentHTML string
	IsPublished bool
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type OrgPageRepo struct{}

func NewOrgPageRepo() *OrgPageRepo { return &OrgPageRepo{} }

const orgPageColumns = `id, org_id, slug, title, content_html, is_published, created_by, created_at, updated_at`

func scanOrgPage(row pgx.Row) (*OrgPage, error) {
	var p OrgPage
	if err := row.Scan(&p.ID, &p.OrgID, &p.Slug, &p.Title, &p.ContentHTML, &p.IsPublished, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func scanOrgPageRows(rows pgx.Rows) (*OrgPage, error) {
	var p OrgPage
	if err := rows.Scan(&p.ID, &p.OrgID, &p.Slug, &p.Title, &p.ContentHTML, &p.IsPublished, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan org page: %w", err)
	}
	return &p, nil
}

func (r *OrgPageRepo) Upsert(ctx context.Context, q Querier, orgID, slug, title, contentHTML string, isPublished bool, createdBy string) (*OrgPage, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO org_pages (org_id, slug, title, content_html, is_published, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (org_id, slug) DO UPDATE
			SET title = EXCLUDED.title, content_html = EXCLUDED.content_html,
			    is_published = EXCLUDED.is_published, updated_at = now()
		RETURNING `+orgPageColumns, orgID, slug, title, contentHTML, isPublished, createdBy)
	p, err := scanOrgPage(row)
	if err != nil {
		return nil, fmt.Errorf("models: upsert org page: %w", err)
	}
	return p, nil
}

func (r *OrgPageRepo) GetBySlug(ctx context.Context, q Querier, orgID, slug string) (*OrgPage, error) {
	row := q.QueryRow(ctx, `SELECT `+orgPageColumns+` FROM org_pages WHERE org_id = $1 AND slug = $2`, orgID, slug)
	p, err := scanOrgPage(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get org page: %w", err)
	}
	return p, nil
}

func (r *OrgPageRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*OrgPage, error) {
	rows, err := q.Query(ctx, `SELECT `+orgPageColumns+` FROM org_pages WHERE org_id = $1 ORDER BY slug ASC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list org pages: %w", err)
	}
	defer rows.Close()

	var out []*OrgPage
	for rows.Next() {
		p, err := scanOrgPageRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list org pages: %w", err)
	}
	return out, nil
}

func (r *OrgPageRepo) Delete(ctx context.Context, q Querier, orgID, slug string) error {
	tag, err := q.Exec(ctx, `DELETE FROM org_pages WHERE org_id = $1 AND slug = $2`, orgID, slug)
	if err != nil {
		return fmt.Errorf("models: delete org page: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
