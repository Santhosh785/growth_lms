package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AnalyticsEvent is one row of the append-only event stream (plan.md Task
// 8): course views, enrollments, lesson starts/completions, searches,
// purchases, refunds, and certificate issuance. Dashboards read
// analytics_daily_rollups, not this table, so writes here never need to
// be fast-path-optimized beyond a single INSERT.
type AnalyticsEvent struct {
	ID          string
	OrgID       string
	EventType   string
	ActorUserID *string
	CourseID    *string
	Metadata    json.RawMessage
	CreatedAt   time.Time
}

const (
	EventCourseView        = "course_view"
	EventEnrollment        = "enrollment"
	EventLessonStart       = "lesson_start"
	EventLessonCompletion  = "lesson_completion"
	EventSearch            = "search"
	EventPurchase          = "purchase"
	EventRefund            = "refund"
	EventCertificateIssued = "certificate_issued"
)

type AnalyticsEventRepo struct{}

func NewAnalyticsEventRepo() *AnalyticsEventRepo { return &AnalyticsEventRepo{} }

// Record inserts one event. actorUserID/courseID may be "" (stored as
// NULL); metadata may be nil (stored as '{}').
func (r *AnalyticsEventRepo) Record(ctx context.Context, q Querier, orgID, eventType, actorUserID, courseID string, metadata json.RawMessage) error {
	if metadata == nil {
		metadata = json.RawMessage(`{}`)
	}
	_, err := q.Exec(ctx, `
		INSERT INTO analytics_events (org_id, event_type, actor_user_id, course_id, metadata)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5)
	`, orgID, eventType, actorUserID, courseID, metadata)
	if err != nil {
		return fmt.Errorf("models: record analytics event: %w", err)
	}
	return nil
}

// CountByTypeSince returns, for an org, the count of events of the given
// type in [since, now) — used by the worker's daily rollup as the raw
// aggregation source query.
func (r *AnalyticsEventRepo) CountByTypeSince(ctx context.Context, q Querier, orgID, eventType string, since, until time.Time) (int64, error) {
	var count int64
	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM analytics_events
		WHERE org_id = $1 AND event_type = $2 AND created_at >= $3 AND created_at < $4
	`, orgID, eventType, since, until).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("models: count analytics events: %w", err)
	}
	return count, nil
}

// CourseCountByTypeSince mirrors CountByTypeSince but grouped by
// course_id, for per-course rollups (enrollment/completion/revenue per
// course on the creator analytics dashboard).
type CourseEventCount struct {
	CourseID string
	Count    int64
}

func (r *AnalyticsEventRepo) CourseCountByTypeSince(ctx context.Context, q Querier, orgID, eventType string, since, until time.Time) ([]CourseEventCount, error) {
	rows, err := q.Query(ctx, `
		SELECT course_id, COUNT(*) FROM analytics_events
		WHERE org_id = $1 AND event_type = $2 AND course_id IS NOT NULL
		  AND created_at >= $3 AND created_at < $4
		GROUP BY course_id
	`, orgID, eventType, since, until)
	if err != nil {
		return nil, fmt.Errorf("models: course count analytics events: %w", err)
	}
	defer rows.Close()

	var out []CourseEventCount
	for rows.Next() {
		var c CourseEventCount
		if err := rows.Scan(&c.CourseID, &c.Count); err != nil {
			return nil, fmt.Errorf("models: scan course event count: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: course count analytics events: %w", err)
	}
	return out, nil
}

// DistinctOrgIDsWithEventsSince returns every org that recorded at least
// one event in the window — the worker's rollup sweep only aggregates
// orgs with actual activity instead of iterating every org on the
// platform.
func (r *AnalyticsEventRepo) DistinctOrgIDsWithEventsSince(ctx context.Context, q Querier, since, until time.Time) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT org_id FROM analytics_events WHERE created_at >= $1 AND created_at < $2
	`, since, until)
	if err != nil {
		return nil, fmt.Errorf("models: distinct org ids with analytics events: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("models: scan org id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: distinct org ids with analytics events: %w", err)
	}
	return out, nil
}
