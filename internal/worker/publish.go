package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
)

// sweepScheduledPublishes queries every course whose scheduled
// publish_date has arrived and publishes it (snapshot + published_at) in
// one transaction per course. A course reverted from 'scheduled' back to
// 'review' before this runs simply stops matching the WHERE clause — no
// cancellation bookkeeping needed, matching the same self-healing
// philosophy as the sort_order renormalization scheme.
//
// Runs with the pool's own (superuser/admin) privileges, same trust
// boundary as any other background job — RLS session variables aren't
// set because there's no per-request caller to scope them to; this is
// the backend acting on its own authority to fulfill a schedule the
// teacher already authorized when they set publish_date.
//
// Publish blocks on incomplete video processing: a course containing a
// non-ready video block is skipped (left 'scheduled' for a later sweep
// pass) rather than published with broken video, matching the same rule
// PublishCourse enforces for the interactive publish path.
func sweepScheduledPublishes(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	courses := models.NewCourseRepo()
	blocks := models.NewBlockRepo()
	assets := models.NewAssetRepo()
	versions := models.NewCourseVersionRepo()

	rows, err := pool.Query(ctx, `
		SELECT id FROM courses WHERE status = 'scheduled' AND publish_date <= now()
	`)
	if err != nil {
		return err
	}
	var dueIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		dueIDs = append(dueIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, courseID := range dueIDs {
		if err := publishOneScheduledCourse(ctx, pool, courses, blocks, assets, versions, courseID, logger); err != nil {
			logger.Error("sweep: failed to publish scheduled course", "course_id", courseID, "error", err)
		}
	}
	return nil
}

func publishOneScheduledCourse(ctx context.Context, pool *pgxpool.Pool, courses *models.CourseRepo, blocks *models.BlockRepo, assets *models.AssetRepo, versions *models.CourseVersionRepo, courseID string, logger *slog.Logger) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	course, err := courses.Get(ctx, tx, courseID)
	if err != nil {
		return err
	}
	// Re-check status inside the transaction: the row may have been
	// reverted to 'review' between the outer query and this point.
	if course.Status != models.CourseStatusScheduled {
		return nil
	}

	videoBlocks, err := blocks.ListVideoBlocksByCourse(ctx, tx, courseID)
	if err != nil {
		return err
	}
	for _, b := range videoBlocks {
		var content models.VideoBlockContent
		if err := json.Unmarshal(b.Content, &content); err != nil {
			continue
		}
		asset, err := assets.Get(ctx, tx, content.AssetID)
		if err != nil || asset.ProcessingStatus != models.ProcessingStatusReady {
			logger.Info("sweep: skipping course, video not ready", "course_id", courseID)
			return nil
		}
	}

	if _, err := courses.Publish(ctx, tx, courseID); err != nil {
		return err
	}
	if _, err := versions.Snapshot(ctx, tx, courseID, course.OrgID, course.CreatedBy); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// runPublishSweepLoop enqueues a sweep once per interval until ctx is
// canceled. Started alongside the asynq server in Run().
func runPublishSweepLoop(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sweepScheduledPublishes(ctx, pool, logger); err != nil {
				logger.Error("scheduled publish sweep failed", "error", err)
			}
		}
	}
}
