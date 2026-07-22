package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/notify/notifytest"
	"growth-lms/internal/testutil"
)

// TestHandleSendReceiptEmail_SendsRegardlessOfOptOut is the key contract
// this handler must uphold, unlike every Task 5 notification handler: a
// learner who opted out of marketing notifications must still receive a
// payment receipt, since it's transactional, not marketing.
func TestHandleSendReceiptEmail_SendsRegardlessOfOptOut(t *testing.T) {
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	ctx := context.Background()
	profiles := models.NewProfileRepo()

	optedOutID := seedWorkerTestUser(t, pool, "opted-out-receipt-"+uuid.NewString()+"@example.com")
	_, err := pool.Exec(ctx, `UPDATE profiles SET notification_opt_out = true WHERE id = $1`, optedOutID)
	require.NoError(t, err)

	payload := SendReceiptEmailPayload{
		LearnerID:      optedOutID,
		CourseID:       "course-1",
		CourseTitle:    "Go Basics",
		Currency:       "INR",
		Subtotal:       999,
		DiscountAmount: 100,
		DiscountCode:   "WELCOME10",
		TaxRatePercent: 18,
		TaxAmount:      161.82,
		Total:          1060.82,
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	task := asynq.NewTask(TypeSendReceiptEmail, data)

	fake := &notifytest.FakeEmailClient{}
	err = handleSendReceiptEmail(pool, profiles, fake)(context.Background(), task)
	require.NoError(t, err)
	require.Equal(t, 1, fake.Count(), "a receipt email must be sent even to an opted-out learner")
	require.Contains(t, fake.Sent[0].Body, "Go Basics")
	require.NotContains(t, fake.Sent[0].Body, "commission", "the receipt body must never mention platform commission")
}
