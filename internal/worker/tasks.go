package worker

import (
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// Task type names for asynq. Registered against the mux in Run() and
// enqueued by internal/httpserver/handlers (via NewClient below) — never
// processed synchronously in the HTTP request path, so a webhook caller
// never blocks on (or directly triggers) the actual DB update.
const (
	TypeBunnyTranscodeComplete = "bunny:transcode_complete"
	TypeSweepScheduledPublish  = "course:sweep_scheduled_publish"
)

// BunnyTranscodeCompletePayload is enqueued by the (already
// signature-verified) HTTP webhook handler — this task's own code has no
// HTTP-level trust decision left to make, only the DB update.
type BunnyTranscodeCompletePayload struct {
	VideoID      string `json:"video_id"`
	Status       string `json:"status"` // "ready" or "failed"
	Duration     int    `json:"duration"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// NewClient builds an asynq.Client for enqueuing tasks from the HTTP
// server process (a separate concern from the worker process itself,
// which only consumes).
func NewClient(redisOpt asynq.RedisConnOpt) *asynq.Client {
	return asynq.NewClient(redisOpt)
}

// EnqueueBunnyTranscodeComplete enqueues the DB-update task for a
// signature-verified Bunny webhook payload.
func EnqueueBunnyTranscodeComplete(client *asynq.Client, payload BunnyTranscodeCompletePayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("worker: marshal bunny transcode payload: %w", err)
	}
	_, err = client.Enqueue(asynq.NewTask(TypeBunnyTranscodeComplete, data), asynq.Queue(QueueDefault))
	if err != nil {
		return fmt.Errorf("worker: enqueue bunny transcode task: %w", err)
	}
	return nil
}
