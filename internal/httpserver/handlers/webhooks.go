package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

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
