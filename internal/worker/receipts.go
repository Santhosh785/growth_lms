// Task 8 (worker-jobs): the payment-receipt email. Deliberately separate
// from notifications.go/notifications_handlers.go (Task 5's marketing/
// engagement notifications) because a receipt is a transactional message
// the learner is entitled to regardless of their notification
// preferences — it must NOT go through sendIfOptedIn's
// notification_opt_out gate the way every Task 5 handler does. See the
// handler's doc comment below for the full reasoning.
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
	"growth-lms/internal/notify"
)

// SendReceiptEmailPayload carries everything the receipt template needs
// to render a proof-of-purchase email. It deliberately has NO commission
// field of any kind: platform commission is an internal figure between
// the platform and the organization, never shown to the learner, so
// there's no way for a future template change to accidentally leak it —
// the field simply does not exist on this struct.
type SendReceiptEmailPayload struct {
	LearnerID   string  `json:"learner_id"`
	CourseID    string  `json:"course_id"`
	CourseTitle string  `json:"course_title"`
	Currency    string  `json:"currency"`
	Subtotal    float64 `json:"subtotal"`
	// DiscountAmount is 0 when no discount was applied. DiscountCode is
	// empty when no discount code was used (e.g. a subscription/cohort
	// offer with no code path, or no code was entered at checkout).
	DiscountAmount float64 `json:"discount_amount"`
	DiscountCode   string  `json:"discount_code,omitempty"`
	// TaxRatePercent/TaxAmount are the breakdown the learner sees on the
	// receipt; TaxAmount is what was actually charged (already computed
	// server-side at order-creation time), TaxRatePercent is shown for
	// context only.
	TaxRatePercent float64 `json:"tax_rate_percent"`
	TaxAmount      float64 `json:"tax_amount"`
	Total          float64 `json:"total"`
}

// EnqueueSendReceiptEmail follows the same enqueueNotification-style
// marshal-and-enqueue shape as notifications.go's Enqueue* functions.
func EnqueueSendReceiptEmail(client *asynq.Client, payload SendReceiptEmailPayload) error {
	return enqueueNotification(client, TypeSendReceiptEmail, payload)
}

// handleSendReceiptEmail resolves the learner's profile and sends
// unconditionally via email.SendEmail — NOT through sendIfOptedIn.
//
// Design note (per task-8-worker-jobs.md): Task 5 established
// notificationRecipient/sendIfOptedIn in notifications_handlers.go, which
// skips sending (but still acks the job) when profile.NotificationOptOut
// is true. That flag is meant for marketing/reminder-type notifications
// (course reminders, announcements), not proof-of-purchase. A payment
// receipt is a transactional message the learner is entitled to
// regardless of their marketing-notification preference — suppressing it
// via notification_opt_out would mean a learner who opted out of
// marketing emails never receives a receipt for money they spent, which
// is a support/compliance problem, not a courtesy. If a real
// transactional-vs-marketing preference field is ever added to profiles,
// receipts should respect that new field instead — but must never be
// gated by the existing marketing-only opt-out flag.
func handleSendReceiptEmail(pool *pgxpool.Pool, profiles *models.ProfileRepo, email notify.EmailClient) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload SendReceiptEmailPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("worker: unmarshal send-receipt-email payload: %w", err)
		}

		profile, err := notificationRecipient(ctx, pool, profiles, payload.LearnerID)
		if err != nil {
			return err
		}

		subject := fmt.Sprintf("Your receipt for %s", payload.CourseTitle)
		body := fmt.Sprintf(
			"<p>Thank you for your purchase of <strong>%s</strong>.</p>"+
				"<table>"+
				"<tr><td>Subtotal</td><td>%.2f %s</td></tr>"+
				"%s"+
				"<tr><td>Tax (%.2f%%)</td><td>%.2f %s</td></tr>"+
				"<tr><td><strong>Total paid</strong></td><td><strong>%.2f %s</strong></td></tr>"+
				"</table>",
			payload.CourseTitle,
			payload.Subtotal, payload.Currency,
			discountRow(payload),
			payload.TaxRatePercent, payload.TaxAmount, payload.Currency,
			payload.Total, payload.Currency,
		)

		// Unconditional send: see the handler's doc comment above. This is
		// the one email in this package that does NOT go through
		// sendIfOptedIn.
		return email.SendEmail(ctx, profile.Email, subject, body)
	}
}

func discountRow(p SendReceiptEmailPayload) string {
	if p.DiscountAmount <= 0 {
		return ""
	}
	if p.DiscountCode != "" {
		return fmt.Sprintf("<tr><td>Discount (%s)</td><td>-%.2f %s</td></tr>", p.DiscountCode, p.DiscountAmount, p.Currency)
	}
	return fmt.Sprintf("<tr><td>Discount</td><td>-%.2f %s</td></tr>", p.DiscountAmount, p.Currency)
}
