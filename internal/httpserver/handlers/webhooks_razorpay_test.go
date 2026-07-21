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
