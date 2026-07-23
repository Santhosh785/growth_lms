package models

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Plan is one row of the platform-wide plans catalog (Task 10 plan limits).
// The limit fields are pointers because NULL means "unlimited" for that
// dimension — distinct from a zero limit, which would mean "none allowed".
// internal/quota treats a nil or non-positive limit as no cap.
type Plan struct {
	ID                  string
	Code                string
	Name                string
	Description         string
	MaxCourses          *int64
	MaxPublishedCourses *int64
	MaxMembers          *int64
	MaxStorageBytes     *int64
	MaxAITokensMonth    *int64
	PriceCents          int64
	Currency            string
	IsDefault           bool
	IsActive            bool
}

type PlanRepo struct{}

func NewPlanRepo() *PlanRepo { return &PlanRepo{} }

const planColumns = `id, code, name, description, max_courses, max_published_courses,
	max_members, max_storage_bytes, max_ai_tokens_month, price_cents, currency,
	is_default, is_active`

// planColumnsP is planColumns projected from a `p` alias, for the JOIN in
// ResolveForOrg.
const planColumnsP = `p.id, p.code, p.name, p.description, p.max_courses, p.max_published_courses,
	p.max_members, p.max_storage_bytes, p.max_ai_tokens_month, p.price_cents, p.currency,
	p.is_default, p.is_active`

func scanPlan(row pgx.Row) (*Plan, error) {
	var p Plan
	if err := row.Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.MaxCourses,
		&p.MaxPublishedCourses, &p.MaxMembers, &p.MaxStorageBytes, &p.MaxAITokensMonth,
		&p.PriceCents, &p.Currency, &p.IsDefault, &p.IsActive); err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns the full plan catalog, active plans first then by code.
func (r *PlanRepo) List(ctx context.Context, q Querier) ([]*Plan, error) {
	rows, err := q.Query(ctx, `SELECT `+planColumns+` FROM plans ORDER BY is_active DESC, price_cents ASC, code ASC`)
	if err != nil {
		return nil, fmt.Errorf("models: list plans: %w", err)
	}
	defer rows.Close()

	var out []*Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("models: scan plan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list plans: %w", err)
	}
	return out, nil
}

// Get returns a plan by id.
func (r *PlanRepo) Get(ctx context.Context, q Querier, id string) (*Plan, error) {
	p, err := scanPlan(q.QueryRow(ctx, `SELECT `+planColumns+` FROM plans WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get plan: %w", err)
	}
	return p, nil
}

// GetDefault returns the single is_default plan. If no plan is marked default
// (a seed/migration bug) this surfaces as ErrNotFound rather than a silent
// zero-limit plan.
func (r *PlanRepo) GetDefault(ctx context.Context, q Querier) (*Plan, error) {
	p, err := scanPlan(q.QueryRow(ctx, `SELECT `+planColumns+` FROM plans WHERE is_default LIMIT 1`))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get default plan: %w", err)
	}
	return p, nil
}

// ResolveForOrg returns the plan assigned to an org, falling back to the
// default plan when the org's plan_id is NULL or points at a deleted plan.
// This is the read path internal/quota uses, so an org that was never
// explicitly assigned a plan still has working limits.
func (r *PlanRepo) ResolveForOrg(ctx context.Context, q Querier, orgID string) (*Plan, error) {
	p, err := scanPlan(q.QueryRow(ctx, `
		SELECT `+planColumnsP+`
		FROM organizations o
		JOIN plans p ON p.id = o.plan_id
		WHERE o.id = $1`, orgID))
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("models: resolve org plan: %w", err)
	}
	// org has NULL plan_id (or FK was SET NULL) — fall back to the default.
	return r.GetDefault(ctx, q)
}

// Create inserts a plan. A unique-violation on code (or on the single-default
// partial index) is reported to the caller via IsUniqueViolation.
func (r *PlanRepo) Create(ctx context.Context, q Querier, p Plan) (*Plan, error) {
	created, err := scanPlan(q.QueryRow(ctx, `
		INSERT INTO plans (code, name, description, max_courses, max_published_courses,
			max_members, max_storage_bytes, max_ai_tokens_month, price_cents, currency,
			is_default, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+planColumns,
		p.Code, p.Name, p.Description, p.MaxCourses, p.MaxPublishedCourses, p.MaxMembers,
		p.MaxStorageBytes, p.MaxAITokensMonth, p.PriceCents, p.Currency, p.IsDefault, p.IsActive))
	if err != nil {
		return nil, fmt.Errorf("models: create plan: %w", err)
	}
	return created, nil
}

// Update overwrites a plan's editable fields (code is immutable — it is the
// stable identifier admin/seed code refers to).
func (r *PlanRepo) Update(ctx context.Context, q Querier, p Plan) (*Plan, error) {
	updated, err := scanPlan(q.QueryRow(ctx, `
		UPDATE plans SET
			name = $2, description = $3, max_courses = $4, max_published_courses = $5,
			max_members = $6, max_storage_bytes = $7, max_ai_tokens_month = $8,
			price_cents = $9, currency = $10, is_default = $11, is_active = $12,
			updated_at = now()
		WHERE id = $1
		RETURNING `+planColumns,
		p.ID, p.Name, p.Description, p.MaxCourses, p.MaxPublishedCourses, p.MaxMembers,
		p.MaxStorageBytes, p.MaxAITokensMonth, p.PriceCents, p.Currency, p.IsDefault, p.IsActive))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update plan: %w", err)
	}
	return updated, nil
}

// AssignToOrg sets an org's plan_id. planID may be "" to clear the assignment
// (the org then resolves to the default plan). Callers pass a validated plan
// id; the FK enforces existence.
func (r *PlanRepo) AssignToOrg(ctx context.Context, q Querier, orgID, planID string) error {
	var arg any
	if planID != "" {
		arg = planID
	}
	tag, err := q.Exec(ctx, `UPDATE organizations SET plan_id = $2, updated_at = now() WHERE id = $1`, orgID, arg)
	if err != nil {
		return fmt.Errorf("models: assign org plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
