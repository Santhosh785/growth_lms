package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Alert severities and categories. Kept in lockstep with the CHECK
// constraints on system_alerts (000016_admin_plans_flags_alerts.up.sql).
const (
	AlertSeverityInfo     = "info"
	AlertSeverityWarning  = "warning"
	AlertSeverityCritical = "critical"

	AlertCategoryJob      = "job"
	AlertCategoryWebhook  = "webhook"
	AlertCategoryStorage  = "storage"
	AlertCategoryDatabase = "database"
	AlertCategoryAuth     = "auth"
	AlertCategoryOther    = "other"
)

// SystemAlert is one operational alert (Task 10 observability & alerting).
type SystemAlert struct {
	ID         string
	OrgID      *string
	Severity   string
	Category   string
	Source     string
	Message    string
	Details    map[string]any
	ResolvedAt *time.Time
	ResolvedBy *string
	CreatedAt  time.Time
}

type AlertRepo struct{}

func NewAlertRepo() *AlertRepo { return &AlertRepo{} }

const alertColumns = `id, org_id, severity, category, source, message, details, resolved_at, resolved_by, created_at`

func scanAlert(row pgx.Row) (*SystemAlert, error) {
	var (
		a       SystemAlert
		details []byte
	)
	if err := row.Scan(&a.ID, &a.OrgID, &a.Severity, &a.Category, &a.Source, &a.Message,
		&details, &a.ResolvedAt, &a.ResolvedBy, &a.CreatedAt); err != nil {
		return nil, err
	}
	if len(details) > 0 {
		_ = json.Unmarshal(details, &a.Details)
	}
	return &a, nil
}

// Record inserts an alert through the SECURITY DEFINER record_system_alert()
// function — the single sanctioned write path (system_alerts has no INSERT
// policy). Callers pass their own Querier; a nil orgID records a
// platform-level alert with no owning org. A missing severity/category is
// normalized to warning/other so a best-effort emitter can't produce a CHECK
// violation.
func (r *AlertRepo) Record(ctx context.Context, q Querier, a SystemAlert) (*SystemAlert, error) {
	if a.Severity == "" {
		a.Severity = AlertSeverityWarning
	}
	if a.Category == "" {
		a.Category = AlertCategoryOther
	}
	var details []byte
	if a.Details != nil {
		b, err := json.Marshal(a.Details)
		if err != nil {
			return nil, fmt.Errorf("models: marshal alert details: %w", err)
		}
		details = b
	}
	created, err := scanAlert(q.QueryRow(ctx, `
		SELECT `+alertColumns+`
		FROM record_system_alert($1, $2, $3, $4, $5, $6)`,
		a.OrgID, a.Severity, a.Category, a.Source, a.Message, details))
	if err != nil {
		return nil, fmt.Errorf("models: record system alert: %w", err)
	}
	return created, nil
}

// AlertFilter narrows a list query. Zero values mean "no filter on this
// dimension"; OpenOnly restricts to unresolved alerts.
type AlertFilter struct {
	OrgID    string
	Category string
	Severity string
	OpenOnly bool
	Limit    int
}

// List returns alerts newest-first, subject to filter and RLS (a platform
// owner sees all; an org owner sees only their org's rows).
func (r *AlertRepo) List(ctx context.Context, q Querier, f AlertFilter) ([]*SystemAlert, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Build a small dynamic WHERE with positional args. $1 is always the limit
	// placeholder position, computed last.
	where := "TRUE"
	args := []any{}
	add := func(cond string, val any) {
		args = append(args, val)
		where += fmt.Sprintf(" AND %s $%d", cond, len(args))
	}
	if f.OrgID != "" {
		add("org_id =", f.OrgID)
	}
	if f.Category != "" {
		add("category =", f.Category)
	}
	if f.Severity != "" {
		add("severity =", f.Severity)
	}
	if f.OpenOnly {
		where += " AND resolved_at IS NULL"
	}
	args = append(args, limit)
	sql := `SELECT ` + alertColumns + ` FROM system_alerts WHERE ` + where +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("models: list system alerts: %w", err)
	}
	defer rows.Close()

	var out []*SystemAlert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("models: scan system alert: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list system alerts: %w", err)
	}
	return out, nil
}

// CountOpen returns the number of unresolved alerts visible to the caller
// under RLS — the badge count for the admin console.
func (r *AlertRepo) CountOpen(ctx context.Context, q Querier) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM system_alerts WHERE resolved_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("models: count open alerts: %w", err)
	}
	return n, nil
}

// Resolve marks an alert resolved by a user. Resolving an already-resolved or
// non-existent (under RLS) alert reports ErrNotFound.
func (r *AlertRepo) Resolve(ctx context.Context, q Querier, id, resolvedBy string) error {
	tag, err := q.Exec(ctx, `
		UPDATE system_alerts
		SET resolved_at = now(), resolved_by = $2
		WHERE id = $1 AND resolved_at IS NULL`, id, resolvedBy)
	if err != nil {
		return fmt.Errorf("models: resolve system alert: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
