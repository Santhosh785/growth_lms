// Task 8 (worker-jobs): the Razorpay webhook-processing job. Consumes
// TypeRazorpayWebhook, which internal/httpserver/handlers/webhooks.go's
// RazorpayWebhook enqueues after HMAC-verifying the delivery and
// recording it in webhook_events via WebhookEventRepo.TryRecord. This
// file is the ONLY place in this codebase that is ever allowed to write a
// succeeded payment or mutate an entitlement's status as a result of a
// payment-provider event — the HTTP handler has no HTTP-level trust
// decision left to make once it hands off here, matching the
// bunny_webhook.go precedent. Per this repo's CLAUDE.md: payment/
// enrollment access is only ever granted after a verified provider
// webhook event, never from a browser return URL.
//
// Runs with the pool's own admin-level privileges, same trust boundary as
// every other worker task (see worker.go's Run comment and
// bunny_webhook.go) — there is no per-request caller to scope RLS session
// variables to for a background job.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/models"
)

// razorpayEntityWrapper matches the {"entity": {...}} shape every
// Razorpay webhook payload.<name> value wraps its actual fields in — same
// convention internal/httpserver/handlers/webhooks.go's
// razorpayEntityPayload documents.
type razorpayEntityWrapper struct {
	Entity json.RawMessage `json:"entity"`
}

// razorpayWebhookBody is just enough of the envelope to reach into
// payload.<payment|refund|dispute>.entity — RazorpayWebhookPayload.Payload
// carries the full raw body verbatim, so this re-parses it here rather
// than trusting any pre-parsed shape from the HTTP layer (there is none;
// the HTTP handler forwards raw bytes precisely so it never has to
// anticipate every event shape up front).
type razorpayWebhookBody struct {
	Payload map[string]json.RawMessage `json:"payload"`
}

// razorpayPaymentEntity is the subset of a Razorpay payment entity's
// fields this job needs. Amount is in the smallest currency unit (paise
// for INR, cents for USD), matching internal/payments/razorpay.go's
// minor-unit convention throughout.
type razorpayPaymentEntity struct {
	ID      string `json:"id"`
	OrderID string `json:"order_id"`
	Amount  int64  `json:"amount"`
}

// razorpayRefundEntity is the subset of a Razorpay refund entity's fields
// this job needs. PaymentID is the Razorpay payment ID the refund was
// issued against (not this app's internal payments.id).
type razorpayRefundEntity struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
}

// razorpayDisputeEntity is the subset of a Razorpay dispute entity's
// fields this job needs.
//
// FLAG FOR VERIFICATION (per task-8-worker-jobs.md — no live Razorpay
// account was available while implementing this task): this codebase has
// not confirmed the exact field names Razorpay's dispute-webhook entity
// payload uses. `payment_id`/`amount`/`reason_code` are Razorpay's
// documented shape for the Disputes API as of general knowledge at
// implementation time, but MUST be checked against a live webhook
// delivery or current https://razorpay.com/docs/webhooks/payloads/disputes/
// before this branch is trusted in production.
type razorpayDisputeEntity struct {
	ID         string `json:"id"`
	PaymentID  string `json:"payment_id"`
	Amount     int64  `json:"amount"`
	ReasonCode string `json:"reason_code"`
}

// Event type strings this job branches on.
//
// FLAG FOR VERIFICATION (per task-8-worker-jobs.md): payment.captured and
// payment.failed are confirmed against Razorpay's standard Payments
// webhook documentation. refund.processed/refund.failed match Razorpay's
// Refunds webhook naming as documented. The three dispute event names
// below are a BEST-GUESS placeholder only — no live Razorpay
// account/dashboard was available while writing this task, so
// "payment.dispute.created"/"payment.dispute.won"/"payment.dispute.lost"
// must be verified against live Razorpay documentation
// (https://razorpay.com/docs/webhooks/payloads/disputes/) or a real
// webhook delivery before this branch is trusted in production. If the
// real names differ, only the switch cases below need updating — the
// chargeback-processing logic itself does not depend on the exact
// strings.
const (
	razorpayEventPaymentCaptured = "payment.captured"
	razorpayEventPaymentFailed   = "payment.failed"
	razorpayEventRefundProcessed = "refund.processed"
	razorpayEventRefundFailed    = "refund.failed"
	razorpayEventDisputeCreated  = "payment.dispute.created" // UNVERIFIED, see comment above
	razorpayEventDisputeWon      = "payment.dispute.won"     // UNVERIFIED, see comment above
	razorpayEventDisputeLost     = "payment.dispute.lost"    // UNVERIFIED, see comment above
)

// minorToMajor converts a Razorpay minor-unit amount (paise/cents) to the
// major-unit decimal amount this codebase's NUMERIC(12,2) columns store.
func minorToMajor(amount int64) float64 {
	return float64(amount) / 100
}

// razorpayWebhookDeps bundles the repos handleRazorpayWebhook needs. A
// struct (rather than a long handler-constructor parameter list) because
// this handler touches far more tables than any other task in this
// package.
type razorpayWebhookDeps struct {
	pool          *pgxpool.Pool
	asyncClient   *asynq.Client
	logger        *slog.Logger
	webhookEvents *models.WebhookEventRepo
	orders        *models.OrderRepo
	payments      *models.PaymentRepo
	refunds       *models.RefundRepo
	chargebacks   *models.ChargebackRepo
	entitlements  *models.EntitlementRepo
	offers        *models.OfferRepo
	courses       *models.CourseRepo
	access        *models.LearnerCourseAccessRepo
	discountCodes *models.DiscountCodeRepo
	paymentAudit  *models.PaymentAuditRepo
	audit         *models.AuditRepo
}

func newRazorpayWebhookDeps(pool *pgxpool.Pool, asyncClient *asynq.Client, logger *slog.Logger) *razorpayWebhookDeps {
	return &razorpayWebhookDeps{
		pool:          pool,
		asyncClient:   asyncClient,
		logger:        logger,
		webhookEvents: models.NewWebhookEventRepo(),
		orders:        models.NewOrderRepo(),
		payments:      models.NewPaymentRepo(),
		refunds:       models.NewRefundRepo(),
		chargebacks:   models.NewChargebackRepo(),
		entitlements:  models.NewEntitlementRepo(),
		offers:        models.NewOfferRepo(),
		courses:       models.NewCourseRepo(),
		access:        models.NewLearnerCourseAccessRepo(),
		discountCodes: models.NewDiscountCodeRepo(),
		paymentAudit:  models.NewPaymentAuditRepo(),
		audit:         models.NewAuditRepo(),
	}
}

// handleRazorpayWebhook is registered on the mux for TypeRazorpayWebhook.
func handleRazorpayWebhook(pool *pgxpool.Pool, asyncClient *asynq.Client, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	d := newRazorpayWebhookDeps(pool, asyncClient, logger)
	return d.handle
}

func (d *razorpayWebhookDeps) handle(ctx context.Context, t *asynq.Task) error {
	var payload RazorpayWebhookPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("worker: unmarshal razorpay webhook payload: %w", err)
	}

	// Idempotency gate (mandatory — see package/task doc comment).
	// RazorpayWebhookPayload carries only EventType + the raw body, not
	// the razorpay_event_id TryRecord computed at the HTTP layer (the
	// real x-razorpay-event-id header never reaches this worker), so this
	// looks up the same webhook_events row by its exact payload bytes
	// instead (see WebhookEventRepo.GetByPayload's doc comment).
	webhookEvent, err := d.webhookEvents.GetByPayload(ctx, d.pool, payload.Payload)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("worker: look up webhook event by payload: %w", err)
	}
	if err == nil && webhookEvent.ProcessedAt != nil {
		// Already processed — an asynq redelivery of a task this handler
		// (or a prior instance of it) already finished. No further writes,
		// no second receipt email.
		return nil
	}
	if errors.Is(err, models.ErrNotFound) {
		d.logger.Warn("razorpay webhook: no matching webhook_events row found by payload; processing without an idempotency record", "event_type", payload.EventType)
	}

	var body razorpayWebhookBody
	if err := json.Unmarshal(payload.Payload, &body); err != nil {
		return fmt.Errorf("worker: unmarshal razorpay webhook body: %w", err)
	}

	switch payload.EventType {
	case razorpayEventPaymentCaptured:
		err = d.handlePaymentCaptured(ctx, body)
	case razorpayEventPaymentFailed:
		err = d.handlePaymentFailed(ctx, body)
	case razorpayEventRefundProcessed:
		err = d.handleRefundProcessed(ctx, body)
	case razorpayEventRefundFailed:
		err = d.handleRefundFailed(ctx, body)
	case razorpayEventDisputeCreated:
		err = d.handleDisputeCreated(ctx, body)
	case razorpayEventDisputeWon:
		err = d.handleDisputeResolved(ctx, body, models.ChargebackStatusWon)
	case razorpayEventDisputeLost:
		err = d.handleDisputeResolved(ctx, body, models.ChargebackStatusLost)
	default:
		// Unrecognized event type: ack the job (return nil) rather than
		// erroring forever — asynq's retry-with-backoff is for transient
		// failures, not "we don't have a handler for this event type yet".
		d.logger.Info("razorpay webhook: unhandled event type, acking without processing", "event_type", payload.EventType)
		err = nil
	}
	if err != nil {
		return err
	}

	if webhookEvent != nil {
		if markErr := d.webhookEvents.MarkProcessed(ctx, d.pool, webhookEvent.RazorpayEventID); markErr != nil && !errors.Is(markErr, models.ErrNotFound) {
			return fmt.Errorf("worker: mark webhook event processed: %w", markErr)
		}
	}
	return nil
}

func extractEntity(body razorpayWebhookBody, key string) (json.RawMessage, bool) {
	raw, ok := body.Payload[key]
	if !ok {
		return nil, false
	}
	var wrapper razorpayEntityWrapper
	if err := json.Unmarshal(raw, &wrapper); err != nil || wrapper.Entity == nil {
		return nil, false
	}
	return wrapper.Entity, true
}

// handlePaymentCaptured implements steps 1-9 of task-8-worker-jobs.md's
// payment.captured branch, all inside one DB transaction (except the
// receipt-email enqueue, which happens after commit — safe as a
// post-commit side effect per the task doc).
func (d *razorpayWebhookDeps) handlePaymentCaptured(ctx context.Context, body razorpayWebhookBody) error {
	entityRaw, ok := extractEntity(body, "payment")
	if !ok {
		return fmt.Errorf("worker: payment.captured event missing payload.payment.entity")
	}
	var entity razorpayPaymentEntity
	if err := json.Unmarshal(entityRaw, &entity); err != nil {
		return fmt.Errorf("worker: unmarshal payment.captured entity: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	order, err := d.orders.GetByRazorpayOrderID(ctx, tx, entity.OrderID)
	if err != nil {
		return fmt.Errorf("worker: resolve order for payment.captured (razorpay_order_id=%s): %w", entity.OrderID, err)
	}

	// Business-level idempotency guard, defense-in-depth alongside the
	// webhook_events check above: an order that already succeeded never
	// gets a second payment/entitlement/audit/receipt cycle.
	if order.Status == models.OrderStatusSucceeded {
		return tx.Commit(ctx)
	}

	oldOrderStatus := order.Status

	payment, err := d.payments.GetByOrderID(ctx, tx, order.ID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("worker: look up payment for order %s: %w", order.ID, err)
	}
	if errors.Is(err, models.ErrNotFound) {
		payment, err = d.payments.Create(ctx, tx, models.Payment{
			OrgID:             order.OrgID,
			OrderID:           order.ID,
			RazorpayPaymentID: &entity.ID,
			Status:            models.PaymentStatusSucceeded,
			RawProviderData:   entityRaw,
		})
		if err != nil {
			return fmt.Errorf("worker: create payment row for order %s: %w", order.ID, err)
		}
	} else {
		payment, err = d.payments.UpdateStatus(ctx, tx, payment.ID, models.PaymentStatusSucceeded, &entity.ID, entityRaw)
		if err != nil {
			return fmt.Errorf("worker: update payment row %s to succeeded: %w", payment.ID, err)
		}
	}

	if _, err := d.orders.UpdateStatus(ctx, tx, order.ID, models.OrderStatusSucceeded); err != nil {
		return fmt.Errorf("worker: update order %s to succeeded: %w", order.ID, err)
	}

	offer, err := d.offers.Get(ctx, tx, order.OfferID)
	if err != nil {
		return fmt.Errorf("worker: load offer %s for order %s: %w", order.OfferID, order.ID, err)
	}

	// expires_at is set only for fixed-term (subscription) offers, per
	// grilling-record.md Q2's "subscription = fixed-term pass" decision.
	var expiresAt *time.Time
	if offer.Type == models.OfferTypeSubscription && offer.AccessDurationDays != nil {
		t := time.Now().Add(time.Duration(*offer.AccessDurationDays) * 24 * time.Hour)
		expiresAt = &t
	}

	entitlement, err := d.entitlements.Create(ctx, tx, models.Entitlement{
		OrgID:     order.OrgID,
		OrderID:   &order.ID,
		LearnerID: order.LearnerID,
		CourseID:  offer.CourseID,
		Status:    models.EntitlementStatusActive,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return fmt.Errorf("worker: create entitlement for order %s: %w", order.ID, err)
	}

	existingAccess, err := d.access.Get(ctx, tx, order.LearnerID, offer.CourseID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("worker: look up learner_course_access for learner %s course %s: %w", order.LearnerID, offer.CourseID, err)
	}
	if errors.Is(err, models.ErrNotFound) {
		if _, err := d.access.Create(ctx, tx, order.OrgID, order.LearnerID, offer.CourseID, &entitlement.ID); err != nil {
			return fmt.Errorf("worker: create learner_course_access for learner %s course %s: %w", order.LearnerID, offer.CourseID, err)
		}
	} else {
		if _, err := d.access.SetEntitlementAndStatus(ctx, tx, existingAccess.ID, &entitlement.ID, models.AccessStatusActive); err != nil {
			return fmt.Errorf("worker: update learner_course_access %s: %w", existingAccess.ID, err)
		}
	}

	var discountCode string
	if order.DiscountCodeID != nil {
		dc, err := d.discountCodes.IncrementRedemption(ctx, tx, *order.DiscountCodeID)
		if err != nil {
			// A discount code's own redemption bookkeeping failing (e.g.
			// its cap was already hit by a racing checkout) must not undo
			// a payment that has already been captured by Razorpay — the
			// learner paid and gets access regardless. Log and move on;
			// the receipt still reflects the discount amount the order
			// itself already recorded at checkout time.
			d.logger.Warn("razorpay webhook: discount code redemption increment failed, continuing", "discount_code_id", *order.DiscountCodeID, "order_id", order.ID, "error", err)
		} else {
			discountCode = dc.Code
		}
	}

	if err := d.paymentAudit.Record(ctx, tx, models.PaymentAuditEvent{
		OrgID:     order.OrgID,
		EventType: razorpayEventPaymentCaptured,
		OrderID:   &order.ID,
		PaymentID: &payment.ID,
		OldState:  &oldOrderStatus,
		NewState:  models.OrderStatusSucceeded,
		UserID:    &order.LearnerID,
	}); err != nil {
		return fmt.Errorf("worker: record payment audit trail for order %s: %w", order.ID, err)
	}

	if err := d.audit.Record(ctx, tx, models.AuditEvent{
		OrgID:        &order.OrgID,
		UserID:       &order.LearnerID,
		Action:       "entitlement.granted",
		ResourceType: "entitlement",
		ResourceID:   &entitlement.ID,
	}); err != nil {
		return fmt.Errorf("worker: record audit event for entitlement %s: %w", entitlement.ID, err)
	}

	course, err := d.courses.Get(ctx, tx, offer.CourseID)
	if err != nil {
		return fmt.Errorf("worker: load course %s for receipt email: %w", offer.CourseID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("worker: commit payment.captured transaction: %w", err)
	}

	// Safe as a post-commit side effect (task doc): if this enqueue fails,
	// the payment/entitlement state has already durably committed; only
	// the receipt email is missed, which is a lesser failure than rolling
	// back a captured payment.
	if err := EnqueueSendReceiptEmail(d.asyncClient, SendReceiptEmailPayload{
		LearnerID:      order.LearnerID,
		CourseID:       offer.CourseID,
		CourseTitle:    course.Title,
		Currency:       order.Currency,
		Subtotal:       order.Subtotal,
		DiscountAmount: order.DiscountAmount,
		DiscountCode:   discountCode,
		TaxRatePercent: offer.TaxRatePercent,
		TaxAmount:      order.TaxAmount,
		Total:          order.Total,
	}); err != nil {
		d.logger.Error("razorpay webhook: failed to enqueue receipt email", "order_id", order.ID, "error", err)
	}

	return nil
}

// handlePaymentFailed implements the payment.failed branch: payments/
// orders both move to 'failed', a payment_audit_trail row is written, no
// entitlement is created or touched, and no audit_events row is written
// (only access-affecting actions are spec-mandated for that table).
func (d *razorpayWebhookDeps) handlePaymentFailed(ctx context.Context, body razorpayWebhookBody) error {
	entityRaw, ok := extractEntity(body, "payment")
	if !ok {
		return fmt.Errorf("worker: payment.failed event missing payload.payment.entity")
	}
	var entity razorpayPaymentEntity
	if err := json.Unmarshal(entityRaw, &entity); err != nil {
		return fmt.Errorf("worker: unmarshal payment.failed entity: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	order, err := d.orders.GetByRazorpayOrderID(ctx, tx, entity.OrderID)
	if err != nil {
		return fmt.Errorf("worker: resolve order for payment.failed (razorpay_order_id=%s): %w", entity.OrderID, err)
	}
	if order.Status == models.OrderStatusFailed {
		return tx.Commit(ctx)
	}
	oldOrderStatus := order.Status

	payment, err := d.payments.GetByOrderID(ctx, tx, order.ID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("worker: look up payment for order %s: %w", order.ID, err)
	}
	if errors.Is(err, models.ErrNotFound) {
		payment, err = d.payments.Create(ctx, tx, models.Payment{
			OrgID:             order.OrgID,
			OrderID:           order.ID,
			RazorpayPaymentID: &entity.ID,
			Status:            models.PaymentStatusFailed,
			RawProviderData:   entityRaw,
		})
		if err != nil {
			return fmt.Errorf("worker: create failed payment row for order %s: %w", order.ID, err)
		}
	} else {
		payment, err = d.payments.UpdateStatus(ctx, tx, payment.ID, models.PaymentStatusFailed, &entity.ID, entityRaw)
		if err != nil {
			return fmt.Errorf("worker: update payment row %s to failed: %w", payment.ID, err)
		}
	}

	if _, err := d.orders.UpdateStatus(ctx, tx, order.ID, models.OrderStatusFailed); err != nil {
		return fmt.Errorf("worker: update order %s to failed: %w", order.ID, err)
	}

	if err := d.paymentAudit.Record(ctx, tx, models.PaymentAuditEvent{
		OrgID:     order.OrgID,
		EventType: razorpayEventPaymentFailed,
		OrderID:   &order.ID,
		PaymentID: &payment.ID,
		OldState:  &oldOrderStatus,
		NewState:  models.OrderStatusFailed,
		UserID:    &order.LearnerID,
	}); err != nil {
		return fmt.Errorf("worker: record payment audit trail for order %s: %w", order.ID, err)
	}

	return tx.Commit(ctx)
}

// revokeEntitlementForPayment is the shared "take away access" sequence
// used by both a successful refund and a lost dispute: revoke the
// entitlement tied to the payment's order, reflect that on the
// learner_course_access row, and write both audit trails. Returns
// (false, nil) if the payment's order never had an entitlement (nothing
// to revoke) — not an error, just nothing to do.
func (d *razorpayWebhookDeps) revokeEntitlementForPayment(ctx context.Context, tx models.Querier, payment *models.Payment, eventType string) (bool, error) {
	entitlement, err := d.entitlements.GetByOrderID(ctx, tx, payment.OrderID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("worker: look up entitlement for order %s: %w", payment.OrderID, err)
	}
	if entitlement.Status != models.EntitlementStatusActive {
		// Already revoked/expired — nothing further to do (idempotent).
		return false, nil
	}

	if _, err := d.entitlements.Revoke(ctx, tx, entitlement.ID); err != nil {
		return false, fmt.Errorf("worker: revoke entitlement %s: %w", entitlement.ID, err)
	}

	access, err := d.access.Get(ctx, tx, entitlement.LearnerID, entitlement.CourseID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return false, fmt.Errorf("worker: look up learner_course_access for learner %s course %s: %w", entitlement.LearnerID, entitlement.CourseID, err)
	}
	if err == nil {
		if _, err := d.access.SetStatus(ctx, tx, access.ID, models.AccessStatusRevoked); err != nil {
			return false, fmt.Errorf("worker: revoke learner_course_access %s: %w", access.ID, err)
		}
	}

	if err := d.audit.Record(ctx, tx, models.AuditEvent{
		OrgID:        &entitlement.OrgID,
		UserID:       &entitlement.LearnerID,
		Action:       "entitlement.revoked",
		ResourceType: "entitlement",
		ResourceID:   &entitlement.ID,
		Details:      map[string]any{"reason": eventType},
	}); err != nil {
		return false, fmt.Errorf("worker: record audit event for revoked entitlement %s: %w", entitlement.ID, err)
	}

	return true, nil
}

// handleRefundProcessed implements the refund-success branch.
//
// Net-revenue adjustment note (task-8-worker-jobs.md step 5): this
// codebase's orders table has no running-total "net revenue" column —
// order.commission_amount/total are fixed at order-creation time and
// orders never mutate on a later refund (see order.go's header comment:
// "A refund or chargeback never changes an order's status"). Revenue
// reporting (Task 9) is expected to compute net-of-refund/net-of-
// commission figures on read, by joining orders with their refunds/
// chargebacks rows, rather than from a stored running total. Under that
// model, writing the refunds row itself (below) IS the full extent of
// this step — there is no additional order/organization column to adjust
// here.
func (d *razorpayWebhookDeps) handleRefundProcessed(ctx context.Context, body razorpayWebhookBody) error {
	entityRaw, ok := extractEntity(body, "refund")
	if !ok {
		return fmt.Errorf("worker: refund.processed event missing payload.refund.entity")
	}
	var entity razorpayRefundEntity
	if err := json.Unmarshal(entityRaw, &entity); err != nil {
		return fmt.Errorf("worker: unmarshal refund.processed entity: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	payment, err := d.payments.GetByRazorpayPaymentID(ctx, tx, entity.PaymentID)
	if err != nil {
		return fmt.Errorf("worker: resolve payment for refund.processed (razorpay_payment_id=%s): %w", entity.PaymentID, err)
	}

	refund, err := d.refunds.GetByRazorpayRefundID(ctx, tx, entity.ID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("worker: look up refund %s: %w", entity.ID, err)
	}
	if errors.Is(err, models.ErrNotFound) {
		// No app-side refund row exists yet — likely a Razorpay-dashboard-
		// initiated refund. Create one, then immediately mark it
		// succeeded below.
		refund, err = d.refunds.Create(ctx, tx, models.Refund{
			OrgID:            payment.OrgID,
			PaymentID:        payment.ID,
			RazorpayRefundID: &entity.ID,
			Amount:           minorToMajor(entity.Amount),
		})
		if err != nil {
			return fmt.Errorf("worker: create refund row for payment %s: %w", payment.ID, err)
		}
	}
	if refund.Status == models.RefundStatusSucceeded {
		// Already processed (defense-in-depth alongside the webhook_events
		// check).
		return tx.Commit(ctx)
	}

	if _, err := d.refunds.UpdateStatus(ctx, tx, refund.ID, models.RefundStatusSucceeded, &entity.ID); err != nil {
		return fmt.Errorf("worker: update refund %s to succeeded: %w", refund.ID, err)
	}

	if _, err := d.revokeEntitlementForPayment(ctx, tx, payment, razorpayEventRefundProcessed); err != nil {
		return err
	}

	oldState := models.RefundStatusPending
	if err := d.paymentAudit.Record(ctx, tx, models.PaymentAuditEvent{
		OrgID:     payment.OrgID,
		EventType: razorpayEventRefundProcessed,
		OrderID:   &payment.OrderID,
		PaymentID: &payment.ID,
		OldState:  &oldState,
		NewState:  models.RefundStatusSucceeded,
	}); err != nil {
		return fmt.Errorf("worker: record payment audit trail for refund %s: %w", refund.ID, err)
	}

	return tx.Commit(ctx)
}

// handleRefundFailed implements the refund-failure branch: refunds.status
// -> 'failed', a payment_audit_trail row, and the entitlement is left
// exactly as it was (a failed refund changes nothing about access).
func (d *razorpayWebhookDeps) handleRefundFailed(ctx context.Context, body razorpayWebhookBody) error {
	entityRaw, ok := extractEntity(body, "refund")
	if !ok {
		return fmt.Errorf("worker: refund.failed event missing payload.refund.entity")
	}
	var entity razorpayRefundEntity
	if err := json.Unmarshal(entityRaw, &entity); err != nil {
		return fmt.Errorf("worker: unmarshal refund.failed entity: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	refund, err := d.refunds.GetByRazorpayRefundID(ctx, tx, entity.ID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			d.logger.Warn("razorpay webhook: refund.failed for unknown refund, nothing to update", "razorpay_refund_id", entity.ID)
			return tx.Commit(ctx)
		}
		return fmt.Errorf("worker: look up refund %s: %w", entity.ID, err)
	}
	if refund.Status == models.RefundStatusFailed {
		return tx.Commit(ctx)
	}

	if _, err := d.refunds.UpdateStatus(ctx, tx, refund.ID, models.RefundStatusFailed, &entity.ID); err != nil {
		return fmt.Errorf("worker: update refund %s to failed: %w", refund.ID, err)
	}

	oldState := refund.Status
	if err := d.paymentAudit.Record(ctx, tx, models.PaymentAuditEvent{
		OrgID:     refund.OrgID,
		EventType: razorpayEventRefundFailed,
		PaymentID: &refund.PaymentID,
		OldState:  &oldState,
		NewState:  models.RefundStatusFailed,
	}); err != nil {
		return fmt.Errorf("worker: record payment audit trail for refund %s: %w", refund.ID, err)
	}

	return tx.Commit(ctx)
}

// handleDisputeCreated implements the dispute-opened branch: create or
// update a chargebacks row for the payment, write a payment_audit_trail
// row, do NOT touch the entitlement.
func (d *razorpayWebhookDeps) handleDisputeCreated(ctx context.Context, body razorpayWebhookBody) error {
	entityRaw, ok := extractEntity(body, "dispute")
	if !ok {
		return fmt.Errorf("worker: dispute-created event missing payload.dispute.entity")
	}
	var entity razorpayDisputeEntity
	if err := json.Unmarshal(entityRaw, &entity); err != nil {
		return fmt.Errorf("worker: unmarshal dispute-created entity: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	payment, err := d.payments.GetByRazorpayPaymentID(ctx, tx, entity.PaymentID)
	if err != nil {
		return fmt.Errorf("worker: resolve payment for dispute-created (razorpay_payment_id=%s): %w", entity.PaymentID, err)
	}

	existing, err := d.chargebacks.GetLatestByPaymentID(ctx, tx, payment.ID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("worker: look up chargeback for payment %s: %w", payment.ID, err)
	}
	if err == nil && existing.Status == models.ChargebackStatusPending {
		// Already recorded as open (dedup) — nothing more to do.
		return tx.Commit(ctx)
	}

	reason := entity.ReasonCode
	chargeback, err := d.chargebacks.Create(ctx, tx, models.Chargeback{
		OrgID:     payment.OrgID,
		PaymentID: payment.ID,
		Amount:    minorToMajor(entity.Amount),
		Reason:    &reason,
	})
	if err != nil {
		return fmt.Errorf("worker: create chargeback for payment %s: %w", payment.ID, err)
	}

	if err := d.paymentAudit.Record(ctx, tx, models.PaymentAuditEvent{
		OrgID:     payment.OrgID,
		EventType: razorpayEventDisputeCreated,
		OrderID:   &payment.OrderID,
		PaymentID: &payment.ID,
		NewState:  models.ChargebackStatusPending,
		Reason:    &reason,
	}); err != nil {
		return fmt.Errorf("worker: record payment audit trail for chargeback %s: %w", chargeback.ID, err)
	}

	return tx.Commit(ctx)
}

// handleDisputeResolved implements both the "won" and "lost" dispute
// branches: update the chargebacks row to the final status, and — only
// for a lost dispute — revoke the entitlement the same way a successful
// refund does.
func (d *razorpayWebhookDeps) handleDisputeResolved(ctx context.Context, body razorpayWebhookBody, finalStatus string) error {
	entityRaw, ok := extractEntity(body, "dispute")
	if !ok {
		return fmt.Errorf("worker: dispute-resolved event missing payload.dispute.entity")
	}
	var entity razorpayDisputeEntity
	if err := json.Unmarshal(entityRaw, &entity); err != nil {
		return fmt.Errorf("worker: unmarshal dispute-resolved entity: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	payment, err := d.payments.GetByRazorpayPaymentID(ctx, tx, entity.PaymentID)
	if err != nil {
		return fmt.Errorf("worker: resolve payment for dispute-resolved (razorpay_payment_id=%s): %w", entity.PaymentID, err)
	}

	chargeback, err := d.chargebacks.GetLatestByPaymentID(ctx, tx, payment.ID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("worker: look up chargeback for payment %s: %w", payment.ID, err)
	}
	if errors.Is(err, models.ErrNotFound) {
		// No prior "created" event was ever seen for this dispute (e.g. it
		// arrived out of order, or was missed) — create the chargeback row
		// now so the final status still lands somewhere.
		reason := entity.ReasonCode
		chargeback, err = d.chargebacks.Create(ctx, tx, models.Chargeback{
			OrgID:     payment.OrgID,
			PaymentID: payment.ID,
			Amount:    minorToMajor(entity.Amount),
			Reason:    &reason,
		})
		if err != nil {
			return fmt.Errorf("worker: create chargeback for payment %s: %w", payment.ID, err)
		}
	}
	if chargeback.Status == finalStatus {
		// Already resolved to this same terminal state (dedup).
		return tx.Commit(ctx)
	}

	if _, err := d.chargebacks.UpdateStatus(ctx, tx, chargeback.ID, finalStatus); err != nil {
		return fmt.Errorf("worker: update chargeback %s to %s: %w", chargeback.ID, finalStatus, err)
	}

	oldState := chargeback.Status
	if err := d.paymentAudit.Record(ctx, tx, models.PaymentAuditEvent{
		OrgID:     payment.OrgID,
		EventType: fmt.Sprintf("payment.dispute.%s", finalStatus),
		OrderID:   &payment.OrderID,
		PaymentID: &payment.ID,
		OldState:  &oldState,
		NewState:  finalStatus,
	}); err != nil {
		return fmt.Errorf("worker: record payment audit trail for chargeback %s: %w", chargeback.ID, err)
	}

	if finalStatus == models.ChargebackStatusLost {
		if _, err := d.revokeEntitlementForPayment(ctx, tx, payment, razorpayEventDisputeLost); err != nil {
			return err
		}
	}
	// A "won" dispute never touches the entitlement: access was never
	// revoked for an open dispute, so there is nothing to restore.

	return tx.Commit(ctx)
}
