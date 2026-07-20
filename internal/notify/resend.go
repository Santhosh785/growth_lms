// Package notify wraps the Resend transactional email API, the delivery
// mechanism for every Task 5 learner notification (assignment graded,
// certificate issued, course announcement posted, course reminder — see
// grilling-record.md Q7: email-only via Resend + Redis, no in-app
// notification table). Exposed as a small interface, mirroring
// internal/media's BunnyClient/StorageClient pattern (a thin hand-rolled
// wrapper over the provider's documented REST API, not a vendored SDK), so
// the worker's notification handlers are unit-testable against a fake
// without live Resend credentials.
//
// This is the first real usage of Resend in this codebase: Task 2/3 wired
// LMS_RESEND_API_KEY (and, as of this stage, LMS_RESEND_FROM_EMAIL) into
// config but never called the API. The real implementation is best-effort
// against Resend's documented REST API and has not been exercised against
// a live account in this session — treat it as a starting point to
// validate against a real sandbox before relying on it in production,
// same caveat internal/media/bunny.go documents for its own real client.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"growth-lms/internal/config"
)

// EmailClient is everything the notification worker needs to send an
// email. Implementations must never be called synchronously from an HTTP
// request handler — only from asynq task handlers (see
// internal/worker/notifications.go's package comment).
type EmailClient interface {
	// SendEmail sends a single plain-text/minimal-HTML email to one
	// recipient. body is treated as the email's HTML content.
	SendEmail(ctx context.Context, to, subject, body string) error
}

// RealEmailClient talks to Resend's REST API directly. The API key never
// leaves the server.
type RealEmailClient struct {
	apiKey string
	from   string
	http   *http.Client
}

func NewResendClient(cfg config.ResendConfig) *RealEmailClient {
	return &RealEmailClient{
		apiKey: cfg.APIKey,
		from:   cfg.FromEmail,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

var _ EmailClient = (*RealEmailClient)(nil)

const resendAPIBase = "https://api.resend.com"

// SendEmail calls Resend's POST /emails endpoint.
func (c *RealEmailClient) SendEmail(ctx context.Context, to, subject, body string) error {
	payload := map[string]any{
		"from":    c.from,
		"to":      []string{to},
		"subject": subject,
		"html":    body,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal resend request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIBase+"/emails", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("notify: build resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send email via resend: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: resend returned status %d", resp.StatusCode)
	}
	return nil
}
