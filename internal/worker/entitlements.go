package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
)

// expireEntitlementsSweepInterval mirrors publishSweepInterval's
// precedent in worker.go — a plain time.Ticker-driven goroutine, not
// asynq's periodic-task machinery. Named independently of
// abandonOrdersSweepInterval/publishSweepInterval (even though the value
// matches) so it can be tuned separately later.
const expireEntitlementsSweepInterval = time.Minute

// sweepExpiredEntitlements flips every 'active' fixed-term entitlement
// whose expires_at has passed to 'expired', updates the corresponding
// learner_course_access row, and writes one audit_events entry per
// expired entitlement — letting a fixed-term pass lapse is an
// access-REVOKING action, spec-mandated for audit_events the same way a
// refund revocation is. Each entitlement is processed independently so
// one failure doesn't block the others, matching sweepScheduledPublishes'
// per-course error handling in publish.go.
func sweepExpiredEntitlements(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	entitlements := models.NewEntitlementRepo()
	access := models.NewLearnerCourseAccessRepo()
	audit := models.NewAuditRepo()

	expired, err := entitlements.ListExpiringBefore(ctx, pool, time.Now())
	if err != nil {
		return err
	}

	for _, e := range expired {
		if err := expireOneEntitlement(ctx, pool, entitlements, access, audit, e); err != nil {
			logger.Error("sweep: failed to expire entitlement", "entitlement_id", e.ID, "error", err)
		}
	}
	return nil
}

func expireOneEntitlement(ctx context.Context, pool *pgxpool.Pool, entitlements *models.EntitlementRepo, access *models.LearnerCourseAccessRepo, audit *models.AuditRepo, e *models.Entitlement) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Re-check status inside the transaction: the entitlement may have
	// already been revoked (e.g. a refund processed concurrently) between
	// the outer query and this point.
	current, err := entitlements.Get(ctx, tx, e.ID)
	if err != nil {
		return err
	}
	if current.Status != models.EntitlementStatusActive || current.ExpiresAt == nil || current.ExpiresAt.After(time.Now()) {
		return nil
	}

	if _, err := entitlements.Expire(ctx, tx, current.ID); err != nil {
		return err
	}

	accessRow, err := access.Get(ctx, tx, current.LearnerID, current.CourseID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return err
	}
	if err == nil {
		if _, err := access.SetStatus(ctx, tx, accessRow.ID, models.AccessStatusExpired); err != nil {
			return err
		}
	}

	if err := audit.Record(ctx, tx, models.AuditEvent{
		OrgID:        &current.OrgID,
		UserID:       &current.LearnerID,
		Action:       "entitlement.expired",
		ResourceType: "entitlement",
		ResourceID:   &current.ID,
	}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// runExpireEntitlementsSweepLoop enqueues a sweep once per interval until
// ctx is canceled. Started alongside the asynq server in Run(), mirroring
// runPublishSweepLoop's exact shape in publish.go.
func runExpireEntitlementsSweepLoop(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sweepExpiredEntitlements(ctx, pool, logger); err != nil {
				logger.Error("expire entitlements sweep failed", "error", err)
			}
		}
	}
}
