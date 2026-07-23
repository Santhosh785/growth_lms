package handlers

import (
	"context"

	"growth-lms/internal/models"
)

// recordAlert is a best-effort helper for emitting an operational
// system_alert from a request handler (Task 10 alerting: payment webhooks,
// storage, and authentication errors). It writes through d.Pool —
// record_system_alert() is SECURITY DEFINER, so it needs no RLS/org context —
// and swallows any error: an alert is a diagnostic signal, and failing to
// record one must never break the request that tried to emit it. Callers that
// already hold a request transaction may pass it instead of the pool via
// recordAlertTx; most alert sites (webhook signature failures, storage errors)
// fire on a path that has no committing transaction, so the pool is correct.
func (d *AuthDeps) recordAlert(ctx context.Context, severity, category, source, message string, details map[string]any) {
	_, _ = d.Alerts.Record(ctx, d.Pool, models.SystemAlert{
		Severity: severity,
		Category: category,
		Source:   source,
		Message:  message,
		Details:  details,
	})
}
