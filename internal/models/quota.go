package models

import (
	"context"
	"fmt"
)

// OrgUsage is an org's current consumption across the dimensions plans cap.
// These are point-in-time gauges (counts of live rows / summed bytes), not
// time-windowed meters — except AITokensMonth, which is the current calendar
// month's metered token total read from ai_usage_counters. Computed on demand
// (this is admin/enforcement tooling read occasionally), which keeps the
// numbers always-correct with no denormalized counter to drift.
type OrgUsage struct {
	Courses          int64
	PublishedCourses int64
	Members          int64
	StorageBytes     int64
	AITokensMonth    int64
}

type QuotaRepo struct{}

func NewQuotaRepo() *QuotaRepo { return &QuotaRepo{} }

// CurrentUsage computes an org's live row-count/storage usage. The AI token
// dimension (AITokensMonth) is left zero here — it is filled in by the quota
// service from the existing ai_usage_counters gauge, which is keyed by month.
// All queries run under the caller's RLS session, so the counts only ever
// cover the caller's own org.
func (r *QuotaRepo) CurrentUsage(ctx context.Context, q Querier, orgID string) (OrgUsage, error) {
	var u OrgUsage

	if err := q.QueryRow(ctx, `SELECT count(*) FROM courses WHERE org_id = $1`, orgID).Scan(&u.Courses); err != nil {
		return u, fmt.Errorf("models: count courses: %w", err)
	}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM courses WHERE org_id = $1 AND status = 'published'`, orgID).Scan(&u.PublishedCourses); err != nil {
		return u, fmt.Errorf("models: count published courses: %w", err)
	}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE org_id = $1`, orgID).Scan(&u.Members); err != nil {
		return u, fmt.Errorf("models: count members: %w", err)
	}
	// assets.size_bytes is nullable; COALESCE the SUM and NULL sizes to 0.
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(COALESCE(size_bytes, 0)), 0) FROM assets WHERE org_id = $1`, orgID).Scan(&u.StorageBytes); err != nil {
		return u, fmt.Errorf("models: sum storage bytes: %w", err)
	}
	return u, nil
}
