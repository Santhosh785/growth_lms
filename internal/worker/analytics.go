package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
)

// analyticsRollupSweepInterval mirrors abandonOrdersSweepInterval's own
// precedent: a plain time.Ticker-driven goroutine, not asynq's periodic-
// task machinery. An hourly cadence (rather than once/day) means a
// dashboard viewed mid-day still reflects most of that day's activity,
// while still being cheap: each run only aggregates orgs that actually
// had events in the last 24h (see AnalyticsEventRepo.DistinctOrgIDsWithEventsSince).
const analyticsRollupSweepInterval = time.Hour

// rollupWindow is how far back each sweep re-aggregates. Wider than the
// sweep interval so a run that's late (or a day whose events straddle
// two runs) still gets a fully re-summed, idempotent rollup — Upsert
// overwrites rather than increments, so re-aggregating the same day
// twice is safe.
const rollupWindow = 25 * time.Hour

// analyticsMetrics is every event_type this sweep turns into a rollup
// row, both org-wide and per-course.
var analyticsMetrics = []string{
	models.EventCourseView,
	models.EventEnrollment,
	models.EventLessonStart,
	models.EventLessonCompletion,
	models.EventSearch,
	models.EventPurchase,
	models.EventRefund,
	models.EventCertificateIssued,
}

// sweepAnalyticsRollups aggregates the last rollupWindow of
// analytics_events into analytics_daily_rollups, one row per
// (org, day, course|org-wide, metric). Runs as the admin pool (no RLS
// session vars) since analytics_daily_rollups is written only by this
// sweep, never by a request handler.
func sweepAnalyticsRollups(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	events := models.NewAnalyticsEventRepo()
	rollups := models.NewAnalyticsRollupRepo()

	until := time.Now().UTC()
	since := until.Add(-rollupWindow)
	day := time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, time.UTC)

	orgIDs, err := events.DistinctOrgIDsWithEventsSince(ctx, pool, since, until)
	if err != nil {
		return err
	}

	for _, orgID := range orgIDs {
		for _, metric := range analyticsMetrics {
			total, err := events.CountByTypeSince(ctx, pool, orgID, metric, since, until)
			if err != nil {
				logger.Error("analytics rollup: count by type failed", "org_id", orgID, "metric", metric, "error", err)
				continue
			}
			if err := rollups.Upsert(ctx, pool, orgID, day, "", metric, total); err != nil {
				logger.Error("analytics rollup: org-wide upsert failed", "org_id", orgID, "metric", metric, "error", err)
			}

			perCourse, err := events.CourseCountByTypeSince(ctx, pool, orgID, metric, since, until)
			if err != nil {
				logger.Error("analytics rollup: per-course count failed", "org_id", orgID, "metric", metric, "error", err)
				continue
			}
			for _, cc := range perCourse {
				if err := rollups.Upsert(ctx, pool, orgID, day, cc.CourseID, metric, cc.Count); err != nil {
					logger.Error("analytics rollup: per-course upsert failed", "org_id", orgID, "course_id", cc.CourseID, "metric", metric, "error", err)
				}
			}
		}
	}
	return nil
}

// runAnalyticsRollupSweepLoop mirrors runAbandonOrdersSweepLoop's exact
// shape in orders.go.
func runAnalyticsRollupSweepLoop(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sweepAnalyticsRollups(ctx, pool, logger); err != nil {
				logger.Error("analytics rollup sweep failed", "error", err)
			}
		}
	}
}
