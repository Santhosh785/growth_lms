package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/payments/paymentstest"
	"growth-lms/internal/testutil"
	"growth-lms/internal/worker"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// newRazorpayWebhookTestEngine builds a minimal Gin engine exposing only
// POST /api/webhooks/razorpay, wired against a real Postgres (mirroring
// how the real server.go connects d.Pool — see webhook_events'
// migration comment: this handler runs against the app's admin
// connection directly, with no RLS session context, exactly like
// testutil.AdminDB) and a real (but ephemeral, in-process) Redis via
// miniredis, so worker.EnqueueRazorpayWebhook's asynq.Client.Enqueue
// call actually succeeds without requiring a live Redis server in CI.
func newRazorpayWebhookTestEngine(t *testing.T) (*gin.Engine, *AuthDeps, *asynq.Inspector) {
	t.Helper()

	testutil.RequireDB(t)
	pool := testutil.AdminDB(t)

	mr := miniredis.RunT(t)
	redisOpt := asynq.RedisClientOpt{Addr: mr.Addr()}
	asyncClient := asynq.NewClient(redisOpt)
	t.Cleanup(func() { _ = asyncClient.Close() })
	inspector := asynq.NewInspector(redisOpt)
	t.Cleanup(func() { _ = inspector.Close() })

	d := &AuthDeps{
		Pool:          pool,
		Payments:      &paymentstest.FakeProvider{WebhookSecret: "whsec_test"},
		WebhookEvents: models.NewWebhookEventRepo(),
		AsyncQueue:    asyncClient,
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/webhooks/razorpay", RazorpayWebhook(d))
	return engine, d, inspector
}

func samplePaymentCapturedBody(eventType, paymentID string, createdAt int64) []byte {
	body, _ := json.Marshal(map[string]any{
		"entity":     "event",
		"account_id": "acc_test",
		"event":      eventType,
		"contains":   []string{"payment"},
		"payload": map[string]any{
			"payment": map[string]any{
				"entity": map[string]any{
					"id":     paymentID,
					"amount": 50000,
				},
			},
		},
		"created_at": createdAt,
	})
	return body
}

func TestRazorpayWebhook_InvalidSignature_Rejected(t *testing.T) {
	engine, d, inspector := newRazorpayWebhookTestEngine(t)
	ctx := context.Background()

	eventID := "evt_" + uuid.NewString()
	paymentID := "pay_" + uuid.NewString()
	body := samplePaymentCapturedBody("payment.captured", paymentID, 1700000000)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(body))
	req.Header.Set("X-Razorpay-Signature", "tampered-signature")
	req.Header.Set("x-razorpay-event-id", eventID)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	// Scoped to this test's own event ID, not a wildcard 'evt_%' match —
	// other tests in this file legitimately create their own evt_-prefixed
	// rows in this shared (uncleaned, per testutil.AdminDB's convention)
	// test database, so a table-wide LIKE count would false-fail once any
	// prior test run left rows behind.
	var count int
	err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE razorpay_event_id = $1`, eventID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "invalid signature must not write any webhook_events row")

	qi, err := inspector.GetQueueInfo(worker.QueueDefault)
	if err == nil {
		require.Equal(t, 0, qi.Pending, "invalid signature must not enqueue anything")
	}
}

// TestRazorpayWebhook_WrongButWellFormedSignature_Rejected is the
// companion to TestRazorpayWebhook_InvalidSignature_Rejected demanded by
// task-11-tests.md gap 1: a signature that is syntactically well-formed
// (looks exactly like a real hex-encoded HMAC-SHA256 digest — 64 lowercase
// hex characters) but is cryptographically wrong must be rejected the
// same way a garbage/missing header is, proving this is a real signature
// check and not merely "is some header present". The real HMAC math
// itself (a well-formed-but-wrong-secret digest genuinely failing
// hmac.Equal) is unit-tested directly against RazorpayProvider in
// internal/payments/razorpay_test.go's "wrong secret" case; this test
// proves the HTTP handler rejects such a signature end to end, all the
// way down to "zero rows written".
func TestRazorpayWebhook_WrongButWellFormedSignature_Rejected(t *testing.T) {
	engine, d, inspector := newRazorpayWebhookTestEngine(t)
	ctx := context.Background()

	eventID := "evt_" + uuid.NewString()
	paymentID := "pay_" + uuid.NewString()
	body := samplePaymentCapturedBody("payment.captured", paymentID, 1700000000)

	// A syntactically well-formed 64-hex-char signature (exactly what a
	// real HMAC-SHA256 hex digest looks like) that is nonetheless the
	// wrong value — distinct in shape from the plainly-garbage
	// "tampered-signature" string the sibling test above uses.
	wellFormedWrongSignature := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"[:64]
	require.Len(t, wellFormedWrongSignature, 64)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(body))
	req.Header.Set("X-Razorpay-Signature", wellFormedWrongSignature)
	req.Header.Set("x-razorpay-event-id", eventID)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	var count int
	err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE razorpay_event_id = $1`, eventID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "a well-formed but wrong signature must not write any webhook_events row")

	var orderCount, paymentCount, entitlementCount int
	require.NoError(t, d.Pool.QueryRow(ctx, `SELECT count(*) FROM orders`).Scan(&orderCount))
	_ = orderCount // no orders table side effect is expected from this handler either way; recorded for completeness
	require.NoError(t, d.Pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE razorpay_payment_id = $1`, paymentID).Scan(&paymentCount))
	require.Equal(t, 0, paymentCount)
	require.NoError(t, d.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlements`).Scan(&entitlementCount))

	qi, err := inspector.GetQueueInfo(worker.QueueDefault)
	if err == nil {
		require.Equal(t, 0, qi.Pending, "a well-formed but wrong signature must not enqueue anything")
	}
}

// razorpaySecretTestValue is a recognizable, non-guessable literal used by
// TestRazorpayWebhook_SecretsNeverLeaked so the require.NotContains
// assertion below is non-trivial (not just checking against an empty
// string).
const razorpaySecretTestValue = "whsec_do_not_leak_zzq93mx7v2"

// TestRazorpayWebhook_SecretsNeverLeaked covers task-11-tests.md gap 5 for
// the webhook endpoint itself: neither a successfully-processed delivery
// nor a rejected-signature delivery's response body may ever contain the
// configured webhook secret, in any response — success (200) or rejection
// (401/400/500).
func TestRazorpayWebhook_SecretsNeverLeaked(t *testing.T) {
	testutil.RequireDB(t)
	pool := testutil.AdminDB(t)

	mr := miniredis.RunT(t)
	redisOpt := asynq.RedisClientOpt{Addr: mr.Addr()}
	asyncClient := asynq.NewClient(redisOpt)
	t.Cleanup(func() { _ = asyncClient.Close() })

	d := &AuthDeps{
		Pool:          pool,
		Payments:      &paymentstest.FakeProvider{WebhookSecret: razorpaySecretTestValue},
		WebhookEvents: models.NewWebhookEventRepo(),
		AsyncQueue:    asyncClient,
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/webhooks/razorpay", RazorpayWebhook(d))

	// Rejected-signature delivery.
	rejectedBody := samplePaymentCapturedBody("payment.captured", "pay_"+uuid.NewString(), 1700000000)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(rejectedBody))
	req.Header.Set("X-Razorpay-Signature", "tampered-signature")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.NotContains(t, rec.Body.String(), razorpaySecretTestValue)

	// Successfully-processed delivery.
	successBody := samplePaymentCapturedBody("payment.captured", "pay_"+uuid.NewString(), 1700000001)
	req = httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(successBody))
	req.Header.Set("X-Razorpay-Signature", "valid-signature")
	req.Header.Set("x-razorpay-event-id", "evt_"+uuid.NewString())
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), razorpaySecretTestValue)
}

func TestRazorpayWebhook_ValidSignature_RecordsAndEnqueuesOnce(t *testing.T) {
	engine, d, inspector := newRazorpayWebhookTestEngine(t)
	ctx := context.Background()

	eventID := "evt_" + uuid.NewString()
	paymentID := "pay_" + uuid.NewString()
	body := samplePaymentCapturedBody("payment.captured", paymentID, 1700000000)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(body))
	req.Header.Set("X-Razorpay-Signature", "valid-signature")
	req.Header.Set("x-razorpay-event-id", eventID)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var count int
	err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE razorpay_event_id = $1`, eventID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "expected exactly one webhook_events row")

	qi, err := inspector.GetQueueInfo(worker.QueueDefault)
	require.NoError(t, err)
	require.Equal(t, 1, qi.Pending, "expected exactly one enqueued task")

	tasks, err := inspector.ListPendingTasks(worker.QueueDefault)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, worker.TypeRazorpayWebhook, tasks[0].Type)
}

func TestRazorpayWebhook_DuplicateDelivery_IsIdempotent(t *testing.T) {
	engine, d, inspector := newRazorpayWebhookTestEngine(t)
	ctx := context.Background()

	eventID := "evt_" + uuid.NewString()
	paymentID := "pay_" + uuid.NewString()
	body := samplePaymentCapturedBody("payment.captured", paymentID, 1700000000)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(body))
		req.Header.Set("X-Razorpay-Signature", "valid-signature")
		req.Header.Set("x-razorpay-event-id", eventID)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "delivery %d: %s", i+1, rec.Body.String())
	}

	var count int
	err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE razorpay_event_id = $1`, eventID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "retry must not create a second webhook_events row")

	qi, err := inspector.GetQueueInfo(worker.QueueDefault)
	require.NoError(t, err)
	require.Equal(t, 1, qi.Pending, "retry must not enqueue a second task")
}

func TestRazorpayWebhook_MissingEventIDHeader_FallsBackToCompositeKey(t *testing.T) {
	engine, d, inspector := newRazorpayWebhookTestEngine(t)
	ctx := context.Background()

	paymentID := "pay_" + uuid.NewString()
	body := samplePaymentCapturedBody("payment.captured", paymentID, 1700000000)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(body))
	req.Header.Set("X-Razorpay-Signature", "valid-signature")
	// Deliberately no x-razorpay-event-id header — exercises the
	// composite-key fallback path.
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	expectedKey := "payment.captured:" + paymentID + ":1700000000"
	var count int
	err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE razorpay_event_id = $1`, expectedKey).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	qi, err := inspector.GetQueueInfo(worker.QueueDefault)
	require.NoError(t, err)
	require.Equal(t, 1, qi.Pending)
}

func TestRazorpayWebhook_MalformedPayload_Returns400(t *testing.T) {
	engine, d, inspector := newRazorpayWebhookTestEngine(t)
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader([]byte("not json")))
	req.Header.Set("X-Razorpay-Signature", "valid-signature")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var count int
	err := d.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events`).Scan(&count)
	require.NoError(t, err)

	qi, err := inspector.GetQueueInfo(worker.QueueDefault)
	if err == nil {
		require.Equal(t, 0, qi.Pending)
	}
}

func TestRazorpayWebhook_EnqueueFailure_Returns500NotSilently200(t *testing.T) {
	testutil.RequireDB(t)
	pool := testutil.AdminDB(t)

	// A client pointed at an address nothing is listening on: TryRecord
	// will succeed (real DB), but the subsequent Enqueue call must fail,
	// and the handler must respond 500 — never 200 for an event that was
	// recorded as seen but never actually queued for processing.
	asyncClient := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = asyncClient.Close() })

	d := &AuthDeps{
		Pool:          pool,
		Payments:      &paymentstest.FakeProvider{WebhookSecret: "whsec_test"},
		WebhookEvents: models.NewWebhookEventRepo(),
		AsyncQueue:    asyncClient,
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/webhooks/razorpay", RazorpayWebhook(d))

	eventID := "evt_" + uuid.NewString()
	paymentID := "pay_" + uuid.NewString()
	body := samplePaymentCapturedBody("payment.captured", paymentID, 1700000000)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/razorpay", bytesReader(body))
	req.Header.Set("X-Razorpay-Signature", "valid-signature")
	req.Header.Set("x-razorpay-event-id", eventID)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}
