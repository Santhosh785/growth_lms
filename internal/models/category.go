package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Category is a curated, org-owner-managed taxonomy entry (spec: a small,
// deliberate set — unlike Tag's freeform get-or-create).
type Category struct {
	ID        string
	OrgID     string
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CategoryRepo struct{}

func NewCategoryRepo() *CategoryRepo { return &CategoryRepo{} }

func (r *CategoryRepo) Create(ctx context.Context, q Querier, orgID, name, slug string) (*Category, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO categories (org_id, name, slug)
		VALUES ($1, $2, $3)
		RETURNING id, org_id, name, slug, created_at, updated_at
	`, orgID, name, slug)
	return scanCategory(row)
}

func (r *CategoryRepo) List(ctx context.Context, q Querier, orgID string) ([]*Category, error) {
	rows, err := q.Query(ctx, `
		SELECT id, org_id, name, slug, created_at, updated_at
		FROM categories WHERE org_id = $1 ORDER BY name
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list categories: %w", err)
	}
	defer rows.Close()

	var out []*Category
	for rows.Next() {
		c, err := scanCategoryRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *CategoryRepo) Update(ctx context.Context, q Querier, id, name, slug string) (*Category, error) {
	row := q.QueryRow(ctx, `
		UPDATE categories SET name = $2, slug = $3, updated_at = now()
		WHERE id = $1
		RETURNING id, org_id, name, slug, created_at, updated_at
	`, id, name, slug)
	return scanCategory(row)
}

func (r *CategoryRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete category: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCategory(row pgx.Row) (*Category, error) {
	var c Category
	if err := row.Scan(&c.ID, &c.OrgID, &c.Name, &c.Slug, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan category: %w", err)
	}
	return &c, nil
}

func scanCategoryRows(rows pgx.Rows) (*Category, error) {
	var c Category
	if err := rows.Scan(&c.ID, &c.OrgID, &c.Name, &c.Slug, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan category: %w", err)
	}
	return &c, nil
}
