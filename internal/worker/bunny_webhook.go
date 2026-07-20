package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
)

// handleBunnyTranscodeComplete updates the matching assets row once a
// Bunny Stream transcode finishes (or fails). This task is only ever
// enqueued by the HTTP webhook handler AFTER it has HMAC-verified the
// incoming call — this code has no HTTP-level trust decision to make,
// only the DB update, matching the "verified provider webhook only" rule
// the spec applies (same as payment webhooks).
func handleBunnyTranscodeComplete(pool *pgxpool.Pool) func(context.Context, *asynq.Task) error {
	assets := models.NewAssetRepo()
	return func(ctx context.Context, t *asynq.Task) error {
		var payload BunnyTranscodeCompletePayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("worker: unmarshal bunny transcode payload: %w", err)
		}

		status := models.ProcessingStatusFailed
		if payload.Status == "ready" {
			status = models.ProcessingStatusReady
		}

		var assetID string
		if err := pool.QueryRow(ctx, `SELECT id FROM assets WHERE storage_key = $1 AND storage_provider = 'bunny'`, payload.VideoID).Scan(&assetID); err != nil {
			return fmt.Errorf("worker: find asset for bunny video %s: %w", payload.VideoID, err)
		}

		var duration *int
		var thumbnailURL *string
		if status == models.ProcessingStatusReady {
			duration = &payload.Duration
			thumbnailURL = &payload.ThumbnailURL
		}

		if _, err := assets.SetProcessingStatus(ctx, pool, assetID, status, duration, thumbnailURL); err != nil {
			return fmt.Errorf("worker: update asset processing status: %w", err)
		}
		return nil
	}
}
