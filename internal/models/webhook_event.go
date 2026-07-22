package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// webhook_events has no org_id and is not RLS-scoped the way every other
// Task 6 table is. The organization a webhook event belongs to isn't
// knowable until its payload is parsed and the referenced order is
// looked up — TryRecord must run before any org context exists, so
// callers pass the worker/pool Querier here, not a request-scoped RLS
// transaction.

// WebhookEvent is a raw, deduplicated Razorpay webhook delivery
// (db/migrations/000006_commerce.up.sql).
type WebhookEvent struct {
	ID              string
	RazorpayEventID string
	EventType       string
	// Payload is NOT NULL JSONB: the raw webhook body, for
	// reprocessing/debugging.
	Payload     []byte
	ProcessedAt *time.Time
	CreatedAt   time.Time
}

type WebhookEventRepo struct{}

func NewWebhookEventRepo() *WebhookEventRepo { return &WebhookEventRepo{} }

// TryRecord is the idempotency gate the webhook HTTP handler calls
// before enqueueing any processing job. isNew is true iff this call's
// INSERT actually added a row (rows affected == 1); false means this
// event ID was already recorded (a Razorpay retry) and the handler must
// skip enqueueing. Returns a wrapped error only on an actual query
// failure, never for the "already exists" case (that's the normal false
// path, not an error).
func (r *WebhookEventRepo) TryRecord(ctx context.Context, q Querier, razorpayEventID, eventType string, payload []byte) (bool, error) {
	tag, err := q.Exec(ctx, `
		INSERT INTO webhook_events (razorpay_event_id, event_type, payload)
		VALUES ($1, $2, $3)
		ON CONFLICT (razorpay_event_id) DO NOTHING
	`, razorpayEventID, eventType, payload)
	if err != nil {
		return false, fmt.Errorf("models: try record webhook event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GetByPayload looks up the webhook_events row whose payload bytes match
// exactly. This is the lookup the Task 8 worker job uses for its
// idempotency check: worker.RazorpayWebhookPayload (deliberately) carries
// only EventType and the raw Payload bytes, not the razorpay_event_id
// TryRecord computed at the HTTP layer (the real x-razorpay-event-id
// header never reaches the worker — only the request body does), so the
// worker cannot recompute the same dedup key TryRecord used. What it CAN
// do is look up the exact same row by the exact same raw bytes it was
// handed, since RazorpayWebhook forwards the identical `body` it already
// passed to TryRecord. JSONB equality compares the parsed structure (not
// raw text), so this is robust to any whitespace/key-order differences a
// re-marshal might introduce, and reliable because both sides originate
// from the same byte slice. Returns ErrNotFound if no row matches (should
// not normally happen, since the HTTP handler always calls TryRecord
// before enqueueing) — most-recent match wins in the pathological case of
// two structurally-identical payloads.
func (r *WebhookEventRepo) GetByPayload(ctx context.Context, q Querier, payload []byte) (*WebhookEvent, error) {
	row := q.QueryRow(ctx, `
		SELECT id, razorpay_event_id, event_type, payload, processed_at, created_at
		FROM webhook_events
		WHERE payload = $1::jsonb
		ORDER BY created_at DESC LIMIT 1
	`, payload)
	var w WebhookEvent
	if err := row.Scan(&w.ID, &w.RazorpayEventID, &w.EventType, &w.Payload, &w.ProcessedAt, &w.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get webhook event by payload: %w", err)
	}
	return &w, nil
}

// MarkProcessed sets processed_at = now() for a recorded webhook event,
// called by the asynq job handler (Task 8) after it finishes processing
// the event, so a future support query can distinguish "recorded but
// never finished processing" (e.g. the job crashed) from "recorded and
// processed".
func (r *WebhookEventRepo) MarkProcessed(ctx context.Context, q Querier, razorpayEventID string) error {
	tag, err := q.Exec(ctx, `UPDATE webhook_events SET processed_at = now() WHERE razorpay_event_id = $1`, razorpayEventID)
	if err != nil {
		return fmt.Errorf("models: mark webhook event processed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
