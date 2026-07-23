package worker

import (
	"context"
	"log/slog"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
)

// jobFailureAlertHandler returns an asynq.ErrorHandler that records a Task 10
// system_alert when a background task fails. To avoid alert spam on transient
// failures (a task that will simply be retried), it only records an alert on
// the FINAL failure — when the retry count has reached the task's max retry and
// asynq is about to archive the task. That archived task is exactly what the
// jobs dashboard surfaces, so the alert and the dashboard agree.
//
// The worker has no per-request RLS session (it runs at the pool's admin
// privileges), and alerts are written through the SECURITY DEFINER
// record_system_alert() function, so AlertRepo.Record works here without any
// org membership. Recording is best-effort: a failure to record an alert is
// logged and swallowed — it must never mask or replace the original task error.
func jobFailureAlertHandler(pool *pgxpool.Pool, alerts *models.AlertRepo, logger *slog.Logger) asynq.ErrorHandler {
	return asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		retried, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)
		final := retried >= maxRetry

		logger.Error("job_failed",
			"type", task.Type(),
			"retried", retried,
			"max_retry", maxRetry,
			"final", final,
			"error", err.Error(),
		)
		if !final {
			return // not exhausted yet — asynq will retry; don't alert on the way.
		}

		taskID, _ := asynq.GetTaskID(ctx)
		if _, aerr := alerts.Record(ctx, pool, models.SystemAlert{
			Severity: models.AlertSeverityCritical,
			Category: models.AlertCategoryJob,
			Source:   task.Type(),
			Message:  "background job failed after exhausting retries: " + err.Error(),
			Details: map[string]any{
				"task_id":   taskID,
				"task_type": task.Type(),
				"retried":   retried,
				"max_retry": maxRetry,
			},
		}); aerr != nil {
			logger.Error("job_failure_alert_record_failed", "error", aerr.Error())
		}
	})
}
