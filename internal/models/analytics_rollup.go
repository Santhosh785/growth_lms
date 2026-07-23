package models

import (
	"context"
	"fmt"
	"time"
)

// AnalyticsRollup is one aggregated (org, day, course|org-wide, metric)
// data point. This is the read path for analytics dashboards; the write
// path is exclusively the worker's daily sweep (see internal/worker).
type AnalyticsRollup struct {
	Day      time.Time
	CourseID *string
	Metric   string
	Value    int64
}

type AnalyticsRollupRepo struct{}

func NewAnalyticsRollupRepo() *AnalyticsRollupRepo { return &AnalyticsRollupRepo{} }

// Upsert writes (or overwrites) one rollup row. Called only from the
// worker's admin-pool connection — courseID may be "" for an org-wide
// metric (stored as NULL).
func (r *AnalyticsRollupRepo) Upsert(ctx context.Context, q Querier, orgID string, day time.Time, courseID, metric string, value int64) error {
	_, err := q.Exec(ctx, `
		INSERT INTO analytics_daily_rollups (org_id, day, course_id, metric, value, updated_at)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, now())
		ON CONFLICT (org_id, day, course_id, metric)
		DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, orgID, day, courseID, metric, value)
	if err != nil {
		return fmt.Errorf("models: upsert analytics rollup: %w", err)
	}
	return nil
}

// OrgSeries returns an org-wide (course_id IS NULL) metric's daily values
// over [since, until), ordered by day ascending, for the org analytics
// dashboard's trend charts.
func (r *AnalyticsRollupRepo) OrgSeries(ctx context.Context, q Querier, orgID, metric string, since, until time.Time) ([]AnalyticsRollup, error) {
	rows, err := q.Query(ctx, `
		SELECT day, course_id, metric, value FROM analytics_daily_rollups
		WHERE org_id = $1 AND metric = $2 AND course_id IS NULL AND day >= $3 AND day < $4
		ORDER BY day ASC
	`, orgID, metric, since, until)
	if err != nil {
		return nil, fmt.Errorf("models: analytics org series: %w", err)
	}
	defer rows.Close()
	return scanRollups(rows)
}

// CourseTotals returns, for each course with data in the window, the sum
// of a metric — the creator dashboard's per-course leaderboard (e.g.
// total enrollments per course this month).
type CourseMetricTotal struct {
	CourseID string
	Total    int64
}

func (r *AnalyticsRollupRepo) CourseTotals(ctx context.Context, q Querier, orgID, metric string, since, until time.Time) ([]CourseMetricTotal, error) {
	rows, err := q.Query(ctx, `
		SELECT course_id, SUM(value) FROM analytics_daily_rollups
		WHERE org_id = $1 AND metric = $2 AND course_id IS NOT NULL AND day >= $3 AND day < $4
		GROUP BY course_id
		ORDER BY SUM(value) DESC
	`, orgID, metric, since, until)
	if err != nil {
		return nil, fmt.Errorf("models: analytics course totals: %w", err)
	}
	defer rows.Close()

	var out []CourseMetricTotal
	for rows.Next() {
		var c CourseMetricTotal
		if err := rows.Scan(&c.CourseID, &c.Total); err != nil {
			return nil, fmt.Errorf("models: scan course metric total: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: analytics course totals: %w", err)
	}
	return out, nil
}

func scanRollups(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]AnalyticsRollup, error) {
	var out []AnalyticsRollup
	for rows.Next() {
		var rr AnalyticsRollup
		if err := rows.Scan(&rr.Day, &rr.CourseID, &rr.Metric, &rr.Value); err != nil {
			return nil, fmt.Errorf("models: scan analytics rollup: %w", err)
		}
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: analytics rollup rows: %w", err)
	}
	return out, nil
}
