package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/models"
	"growth-lms/internal/worker"
)

type bunnyWebhookPayload struct {
	VideoGUID    string `json:"VideoGuid"`
	Status       int    `json:"Status"`
	Duration     int    `json:"Duration"`
	ThumbnailURL string `json:"ThumbnailUrl"`
}

// bunnyTranscodeSuccessStatus is Bunny Stream's numeric status code for
// "encoding finished successfully" in its webhook payload.
const bunnyTranscodeSuccessStatus = 4

// BunnyWebhook verifies the incoming call's HMAC signature before doing
// anything else — never trusted from an unverified caller, matching the
// "verified provider webhook only" rule the spec applies to payment
// webhooks too. Once verified, it only enqueues the DB-update task; it
// never updates Postgres directly from the HTTP request path.
func BunnyWebhook(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		signature := c.GetHeader("X-Bunny-Signature")
		if !d.Bunny.VerifyWebhookSignature(body, signature) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		var payload bunnyWebhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
			return
		}

		status := "failed"
		if payload.Status == bunnyTranscodeSuccessStatus {
			status = "ready"
		}

		if err := worker.EnqueueBunnyTranscodeComplete(d.AsyncQueue, worker.BunnyTranscodeCompletePayload{
			VideoID:      payload.VideoGUID,
			Status:       status,
			Duration:     payload.Duration,
			ThumbnailURL: payload.ThumbnailURL,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue"})
			return
		}
		c.Status(http.StatusOK)
	}
}

// razorpayWebhookEnvelope captures just enough of Razorpay's webhook
// envelope to extract the event type and a dedup key. The full raw body
// is what actually gets forwarded to the worker (see RazorpayWebhook
// below) — this struct is only used to read fields out of it, never
// re-marshaled.
type razorpayWebhookEnvelope struct {
	Event     string                     `json:"event"`
	CreatedAt int64                      `json:"created_at"`
	Payload   map[string]json.RawMessage `json:"payload"`
}

// razorpayEntityPayload is the shape of each value in a
// razorpayWebhookEnvelope's Payload map (e.g. payload.payment,
// payload.refund) — every Razorpay webhook entity is wrapped the same
// way: {"entity": {...fields including "id"...}}.
type razorpayEntityPayload struct {
	Entity struct {
		ID string `json:"id"`
	} `json:"entity"`
}

// RazorpayWebhook is the payments-domain analog of BunnyWebhook above:
// verify the caller's HMAC signature before touching the body's
// contents in any way, idempotency-gate the delivery, and hand off to
// the worker. This handler's ONLY database write, in any code path, is
// the WebhookEventRepo.TryRecord idempotency insert below — it never
// touches orders/payments/refunds/chargebacks/entitlements or grants/
// revokes learner access. That interpretation belongs entirely to the
// Task 8 worker job that consumes TypeRazorpayWebhook after this handler
// has already returned 200. See this repo's CLAUDE.md: payment/
// entitlement access is only ever granted from a verified async webhook
// event, never synchronously from an HTTP request path.
func RazorpayWebhook(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		signature := c.GetHeader("X-Razorpay-Signature")
		if !d.Payments.VerifyWebhookSignature(body, signature) {
			// A payment webhook that fails signature verification is either a
			// forged/misdirected call or a rotated-secret misconfiguration —
			// both warrant an operator's attention.
			d.recordAlert(c.Request.Context(), models.AlertSeverityWarning, models.AlertCategoryWebhook,
				"razorpay_webhook", "payment webhook rejected: invalid signature",
				map[string]any{"client_ip": c.ClientIP()})
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		var envelope razorpayWebhookEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil || envelope.Event == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
			return
		}

		// Dedup key: Razorpay's webhook documentation (checked at
		// implementation time — see
		// https://razorpay.com/docs/webhooks/validate-test/) confirms a
		// delivery-level unique ID IS sent as the `x-razorpay-event-id`
		// HTTP header ("You can identify the duplicate webhooks using the
		// x-razorpay-event-id header. The value for this header is unique
		// per event."), so that header is the primary dedup key — it is
		// the cleanest option since it needs no payload parsing. As a
		// defensive fallback only (in case a delivery ever arrives without
		// it, e.g. a future Razorpay change or a misconfigured sender), we
		// derive a composite key from the event type + the first entity's
		// provider ID + created_at, which is stable across retries of the
		// same delivery but distinct across genuinely different events
		// (so payment.captured and a later refund.processed for the same
		// payment ID never collide).
		eventID := c.GetHeader("x-razorpay-event-id")
		if eventID == "" {
			var entityID string
			for _, raw := range envelope.Payload {
				var entity razorpayEntityPayload
				if err := json.Unmarshal(raw, &entity); err == nil && entity.Entity.ID != "" {
					entityID = entity.Entity.ID
					break
				}
			}
			if entityID == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
				return
			}
			eventID = fmt.Sprintf("%s:%s:%d", envelope.Event, entityID, envelope.CreatedAt)
		}

		isNew, err := d.WebhookEvents.TryRecord(c.Request.Context(), d.Pool, eventID, envelope.Event, body)
		if err != nil {
			// A verified payment event we cannot even record risks a dropped
			// payment/entitlement — critical.
			d.recordAlert(c.Request.Context(), models.AlertSeverityCritical, models.AlertCategoryWebhook,
				"razorpay_webhook", "failed to record verified payment webhook: "+err.Error(),
				map[string]any{"event": envelope.Event, "event_id": eventID})
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record webhook event"})
			return
		}
		if !isNew {
			// Already recorded — a Razorpay retry of a delivery this
			// handler (or a prior instance of it) already accepted.
			// Silently accept-and-ignore: do not enqueue a second time.
			c.Status(http.StatusOK)
			return
		}

		if err := worker.EnqueueRazorpayWebhook(d.AsyncQueue, worker.RazorpayWebhookPayload{
			EventType: envelope.Event,
			Payload:   body,
		}); err != nil {
			// Recorded but not enqueued: the idempotency row now exists, so a
			// Razorpay retry would be accept-and-ignored — the event would
			// never be processed. Critical, and needs manual re-drive.
			d.recordAlert(c.Request.Context(), models.AlertSeverityCritical, models.AlertCategoryWebhook,
				"razorpay_webhook", "verified payment webhook recorded but failed to enqueue: "+err.Error(),
				map[string]any{"event": envelope.Event, "event_id": eventID})
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue"})
			return
		}
		c.Status(http.StatusOK)
	}
}
