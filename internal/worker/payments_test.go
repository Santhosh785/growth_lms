package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/testutil"
)

// discardLogger is a *slog.Logger that writes nowhere, for tests that
// need to satisfy this package's *slog.Logger parameters without
// polluting `go test -v` output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// unreachableAsyncClient is an asynq.Client pointed at a port nothing
// listens on. handlePaymentCaptured's receipt-email enqueue is a
// best-effort, logged-not-fatal post-commit side effect (see its doc
// comment), so tests can use this to prove the payment/entitlement
// transaction itself succeeds without needing a live Redis.
func unreachableAsyncClient(t *testing.T) *asynq.Client {
	t.Helper()
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:63799"})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func seedWorkerTestUser(t *testing.T, admin *pgxpool.Pool, email string) string {
	t.Helper()
	id := uuid.NewString()
	_, err := admin.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1, $2)`, id, email)
	require.NoError(t, err)
	return id
}

func seedWorkerTestOrg(t *testing.T, admin *pgxpool.Pool, ownerUserID, slug string) string {
	t.Helper()
	orgID := uuid.NewString()
	ctx := context.Background()
	_, err := admin.Exec(ctx, `INSERT INTO organizations (id, slug, name, created_by_user_id) VALUES ($1, $2, $3, $4)`, orgID, slug, slug, ownerUserID)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'owner')`, ownerUserID, orgID)
	require.NoError(t, err)
	return orgID
}

// paymentCapturedBody builds a synthetic Razorpay payment.captured
// webhook body: {"event": "payment.captured", "payload": {"payment":
// {"entity": {...}}}}. amountMinorUnits is in paise/cents.
func paymentCapturedBody(t *testing.T, razorpayPaymentID, razorpayOrderID string, amountMinorUnits int64) []byte {
	t.Helper()
	body := map[string]any{
		"event": razorpayEventPaymentCaptured,
		"payload": map[string]any{
			"payment": map[string]any{
				"entity": map[string]any{
					"id":       razorpayPaymentID,
					"order_id": razorpayOrderID,
					"amount":   amountMinorUnits,
				},
			},
		},
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)
	return data
}

// commerceFixture is everything TestHandleRazorpayWebhook_PaymentCaptured
// needs: an org, a paid offer on a course, a learner, and a pending order
// tagged with a Razorpay order ID.
type commerceFixture struct {
	orgID           string
	learnerID       string
	courseID        string
	offerID         string
	orderID         string
	razorpayOrderID string
}

func seedCommerceFixture(t *testing.T, admin *pgxpool.Pool, offerType string, accessDurationDays *int) commerceFixture {
	t.Helper()
	ctx := context.Background()

	ownerID := seedWorkerTestUser(t, admin, uuid.NewString()+"@example.com")
	learnerID := seedWorkerTestUser(t, admin, uuid.NewString()+"@example.com")
	orgID := seedWorkerTestOrg(t, admin, ownerID, "commerce-"+uuid.NewString())

	courses := models.NewCourseRepo()
	course, err := courses.Create(ctx, admin, orgID, ownerID, "Paid Course", "desc", nil)
	require.NoError(t, err)

	offers := models.NewOfferRepo()
	offer, err := offers.Create(ctx, admin, models.Offer{
		OrgID:              orgID,
		CourseID:           course.ID,
		Type:               offerType,
		Price:              999,
		Currency:           "INR",
		TaxRatePercent:     18,
		AccessDurationDays: accessDurationDays,
		Status:             models.OfferStatusActive,
		CreatedBy:          ownerID,
	})
	require.NoError(t, err)

	razorpayOrderID := "order_" + uuid.NewString()
	orders := models.NewOrderRepo()
	order, err := orders.Create(ctx, admin, models.Order{
		OrgID:                  orgID,
		OfferID:                offer.ID,
		LearnerID:              learnerID,
		Currency:               "INR",
		Subtotal:               999,
		DiscountAmount:         0,
		TaxAmount:              179.82,
		CommissionAmount:       99.9,
		Total:                  1178.82,
		CommissionRateSnapshot: 10,
		RazorpayOrderID:        &razorpayOrderID,
	})
	require.NoError(t, err)

	return commerceFixture{
		orgID:           orgID,
		learnerID:       learnerID,
		courseID:        course.ID,
		offerID:         offer.ID,
		orderID:         order.ID,
		razorpayOrderID: razorpayOrderID,
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

// TestHandleRazorpayWebhook_PaymentCaptured covers the core acceptance
// criteria for the payment.captured branch: payment succeeded, order
// succeeded, exactly one active entitlement, a learner_course_access row,
// one payment_audit_trail row, one audit_events row, and idempotency on
// redelivery.
func TestHandleRazorpayWebhook_PaymentCaptured(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()
	logger := discardLogger()

	fixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)

	// Simulate the HTTP handler's TryRecord + enqueue: record the
	// webhook_events row with the exact bytes the worker payload will
	// carry.
	body := paymentCapturedBody(t, "pay_"+uuid.NewString(), fixture.razorpayOrderID, 117882)
	webhookEvents := models.NewWebhookEventRepo()
	isNew, err := webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventPaymentCaptured, body)
	require.NoError(t, err)
	require.True(t, isNew)

	d := newRazorpayWebhookDeps(pool, unreachableAsyncClient(t), logger)

	payload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventPaymentCaptured, Payload: body})
	require.NoError(t, err)
	task := asynq.NewTask(TypeRazorpayWebhook, payload)

	require.NoError(t, d.handle(ctx, task))

	orders := models.NewOrderRepo()
	order, err := orders.Get(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.OrderStatusSucceeded, order.Status)

	payments := models.NewPaymentRepo()
	payment, err := payments.GetByOrderID(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.PaymentStatusSucceeded, payment.Status)

	entitlements := models.NewEntitlementRepo()
	entitlement, err := entitlements.GetByOrderID(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusActive, entitlement.Status)
	require.Nil(t, entitlement.ExpiresAt, "a non-subscription offer grants perpetual access")

	access := models.NewLearnerCourseAccessRepo()
	accessRow, err := access.Get(ctx, pool, fixture.learnerID, fixture.courseID)
	require.NoError(t, err)
	require.Equal(t, models.AccessStatusActive, accessRow.AccessStatus)
	require.NotNil(t, accessRow.EntitlementID)
	require.Equal(t, entitlement.ID, *accessRow.EntitlementID)

	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM payment_audit_trail WHERE order_id = $1`, fixture.orderID))
	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM audit_events WHERE resource_type = 'entitlement' AND resource_id = $1`, entitlement.ID))

	// --- Idempotency: process the exact same delivery again. ---
	require.NoError(t, d.handle(ctx, task))

	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM payments WHERE order_id = $1`, fixture.orderID), "no second payment row")
	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM entitlements WHERE order_id = $1`, fixture.orderID), "no second entitlement row")
	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM payment_audit_trail WHERE order_id = $1`, fixture.orderID), "no second payment_audit_trail row")
	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM audit_events WHERE resource_type = 'entitlement' AND resource_id = $1`, entitlement.ID), "no second audit_events row")
}

// TestHandleRazorpayWebhook_PaymentCaptured_SubscriptionOffer covers the
// fixed-term entitlement path: a subscription offer's
// access_duration_days must produce a non-nil expires_at.
func TestHandleRazorpayWebhook_PaymentCaptured_SubscriptionOffer(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	days := 30
	fixture := seedCommerceFixture(t, pool, models.OfferTypeSubscription, &days)

	body := paymentCapturedBody(t, "pay_"+uuid.NewString(), fixture.razorpayOrderID, 117882)
	webhookEvents := models.NewWebhookEventRepo()
	_, err := webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventPaymentCaptured, body)
	require.NoError(t, err)

	d := newRazorpayWebhookDeps(pool, unreachableAsyncClient(t), discardLogger())
	payload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventPaymentCaptured, Payload: body})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, payload)))

	entitlements := models.NewEntitlementRepo()
	entitlement, err := entitlements.GetByOrderID(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.NotNil(t, entitlement.ExpiresAt, "a subscription offer must set a fixed-term expires_at")
}

// TestHandleRazorpayWebhook_PaymentFailed covers the payment.failed
// branch: payments/orders both move to failed, no entitlement is ever
// created.
func TestHandleRazorpayWebhook_PaymentFailed(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	fixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)

	body, err := json.Marshal(map[string]any{
		"event": razorpayEventPaymentFailed,
		"payload": map[string]any{
			"payment": map[string]any{
				"entity": map[string]any{
					"id":       "pay_" + uuid.NewString(),
					"order_id": fixture.razorpayOrderID,
					"amount":   117882,
				},
			},
		},
	})
	require.NoError(t, err)

	webhookEvents := models.NewWebhookEventRepo()
	_, err = webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventPaymentFailed, body)
	require.NoError(t, err)

	d := newRazorpayWebhookDeps(pool, unreachableAsyncClient(t), discardLogger())
	payload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventPaymentFailed, Payload: body})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, payload)))

	orders := models.NewOrderRepo()
	order, err := orders.Get(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.OrderStatusFailed, order.Status)

	require.Equal(t, 0, countRows(t, pool, `SELECT count(*) FROM entitlements WHERE order_id = $1`, fixture.orderID))
	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM payment_audit_trail WHERE order_id = $1`, fixture.orderID))
}

// TestHandleRazorpayWebhook_RefundProcessed_RevokesEntitlement covers the
// refund-success branch end to end: capture a payment (granting access),
// then process a refund.processed event and confirm the entitlement is
// revoked and learner_course_access reflects it.
func TestHandleRazorpayWebhook_RefundProcessed_RevokesEntitlement(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	fixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)
	razorpayPaymentID := "pay_" + uuid.NewString()

	captureBody := paymentCapturedBody(t, razorpayPaymentID, fixture.razorpayOrderID, 117882)
	webhookEvents := models.NewWebhookEventRepo()
	_, err := webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventPaymentCaptured, captureBody)
	require.NoError(t, err)

	d := newRazorpayWebhookDeps(pool, unreachableAsyncClient(t), discardLogger())
	capturePayload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventPaymentCaptured, Payload: captureBody})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, capturePayload)))

	entitlements := models.NewEntitlementRepo()
	entitlement, err := entitlements.GetByOrderID(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusActive, entitlement.Status)

	refundBody, err := json.Marshal(map[string]any{
		"event": razorpayEventRefundProcessed,
		"payload": map[string]any{
			"refund": map[string]any{
				"entity": map[string]any{
					"id":         "rfnd_" + uuid.NewString(),
					"payment_id": razorpayPaymentID,
					"amount":     117882,
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventRefundProcessed, refundBody)
	require.NoError(t, err)

	refundPayload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventRefundProcessed, Payload: refundBody})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, refundPayload)))

	entitlement, err = entitlements.Get(ctx, pool, entitlement.ID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusRevoked, entitlement.Status)

	access := models.NewLearnerCourseAccessRepo()
	accessRow, err := access.Get(ctx, pool, fixture.learnerID, fixture.courseID)
	require.NoError(t, err)
	require.Equal(t, models.AccessStatusRevoked, accessRow.AccessStatus)

	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM audit_events WHERE action = 'entitlement.revoked' AND resource_id = $1`, entitlement.ID))

	refunds := models.NewRefundRepo()
	list, err := refunds.GetByPaymentID(ctx, pool, mustGetPaymentID(t, ctx, pool, fixture.orderID))
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, models.RefundStatusSucceeded, list[0].Status)
}

// TestHandleRazorpayWebhook_DisputeLost_RevokesEntitlement covers the
// chargeback/dispute-lost branch (task-11-tests.md gap 4's second
// sub-test): capture a payment (granting access), then process a
// payment.dispute.created event followed by a payment.dispute.lost event,
// and confirm the same entitlement-revoked / access-revoked outcome a
// successful refund produces, plus a chargebacks row recording the lost
// dispute. Unlike the refund case, this codebase's RevenueReport
// (internal/httpserver/handlers/commerce_reports.go) only nets out
// succeeded refunds, not chargebacks — see this test's final assertions,
// which document rather than paper over that gap.
func TestHandleRazorpayWebhook_DisputeLost_RevokesEntitlement(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	fixture := seedCommerceFixture(t, pool, models.OfferTypePaid, nil)
	razorpayPaymentID := "pay_" + uuid.NewString()

	captureBody := paymentCapturedBody(t, razorpayPaymentID, fixture.razorpayOrderID, 117882)
	webhookEvents := models.NewWebhookEventRepo()
	_, err := webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventPaymentCaptured, captureBody)
	require.NoError(t, err)

	d := newRazorpayWebhookDeps(pool, unreachableAsyncClient(t), discardLogger())
	capturePayload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventPaymentCaptured, Payload: captureBody})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, capturePayload)))

	entitlements := models.NewEntitlementRepo()
	entitlement, err := entitlements.GetByOrderID(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusActive, entitlement.Status)

	disputeBody := func(reasonCode string) []byte {
		body, err := json.Marshal(map[string]any{
			"event": razorpayEventDisputeCreated,
			"payload": map[string]any{
				"dispute": map[string]any{
					"entity": map[string]any{
						"id":          "disp_" + uuid.NewString(),
						"payment_id":  razorpayPaymentID,
						"amount":      117882,
						"reason_code": reasonCode,
					},
				},
			},
		})
		require.NoError(t, err)
		return body
	}

	// payment.dispute.created: chargeback recorded as pending, entitlement
	// untouched (an open dispute is not yet a loss of access).
	createdBody := disputeBody("goods_not_received")
	_, err = webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventDisputeCreated, createdBody)
	require.NoError(t, err)
	createdPayload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventDisputeCreated, Payload: createdBody})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, createdPayload)))

	entitlement, err = entitlements.Get(ctx, pool, entitlement.ID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusActive, entitlement.Status, "an open dispute must not revoke access")

	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM chargebacks WHERE payment_id = $1`, mustGetPaymentID(t, ctx, pool, fixture.orderID)))

	// payment.dispute.lost: the actual chargeback/dispute-lost branch —
	// must revoke the entitlement and access exactly like a refund does.
	lostBody, err := json.Marshal(map[string]any{
		"event": razorpayEventDisputeLost,
		"payload": map[string]any{
			"dispute": map[string]any{
				"entity": map[string]any{
					"id":          "disp_" + uuid.NewString(),
					"payment_id":  razorpayPaymentID,
					"amount":      117882,
					"reason_code": "goods_not_received",
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = webhookEvents.TryRecord(ctx, pool, "evt_"+uuid.NewString(), razorpayEventDisputeLost, lostBody)
	require.NoError(t, err)
	lostPayload, err := json.Marshal(RazorpayWebhookPayload{EventType: razorpayEventDisputeLost, Payload: lostBody})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, lostPayload)))

	entitlement, err = entitlements.Get(ctx, pool, entitlement.ID)
	require.NoError(t, err)
	require.Equal(t, models.EntitlementStatusRevoked, entitlement.Status, "a lost dispute must revoke the entitlement")

	access := models.NewLearnerCourseAccessRepo()
	accessRow, err := access.Get(ctx, pool, fixture.learnerID, fixture.courseID)
	require.NoError(t, err)
	require.Equal(t, models.AccessStatusRevoked, accessRow.AccessStatus, "a lost dispute must revoke learner_course_access")

	require.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM audit_events WHERE action = 'entitlement.revoked' AND resource_id = $1`, entitlement.ID))

	chargebacks := models.NewChargebackRepo()
	cb, err := chargebacks.GetLatestByPaymentID(ctx, pool, mustGetPaymentID(t, ctx, pool, fixture.orderID))
	require.NoError(t, err)
	require.Equal(t, models.ChargebackStatusLost, cb.Status)
	require.InDelta(t, 1178.82, cb.Amount, 0.01, "chargeback amount is captured in major units")

	// Order/payment revenue bookkeeping: this codebase's order row is
	// intentionally never mutated by a later refund/chargeback (see
	// order.go's header comment) — order.Total/CommissionAmount stay
	// exactly as they were at order-creation time. The chargebacks row
	// itself is the durable record of the amount lost; that amount is
	// available for revenue reporting purposes via the chargebacks table.
	order, err := models.NewOrderRepo().Get(ctx, pool, fixture.orderID)
	require.NoError(t, err)
	require.InDelta(t, 1178.82, order.Total, 0.01, "orders.total is never retroactively mutated by a chargeback")
}

func mustGetPaymentID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID string) string {
	t.Helper()
	payments := models.NewPaymentRepo()
	p, err := payments.GetByOrderID(ctx, pool, orderID)
	require.NoError(t, err)
	return p.ID
}

// TestHandleRazorpayWebhook_UnknownEventType_AcksWithoutError proves an
// unrecognized event type is acked (nil error) rather than retried
// forever.
func TestHandleRazorpayWebhook_UnknownEventType_AcksWithoutError(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	body, err := json.Marshal(map[string]any{"event": "some.future.event", "payload": map[string]any{}})
	require.NoError(t, err)

	d := newRazorpayWebhookDeps(pool, unreachableAsyncClient(t), discardLogger())
	payload, err := json.Marshal(RazorpayWebhookPayload{EventType: "some.future.event", Payload: body})
	require.NoError(t, err)
	require.NoError(t, d.handle(ctx, asynq.NewTask(TypeRazorpayWebhook, payload)))
}
