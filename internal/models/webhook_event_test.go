package models_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/models"
	"growth-lms/internal/testutil"
)

// TestWebhookEventRepo_TryRecord_Dedup is a focused unit-level test
// against WebhookEventRepo.TryRecord's ON CONFLICT (razorpay_event_id) DO
// NOTHING dedup behavior, directly (not via the HTTP handler), per
// task-11-tests.md gap 2: the first call for a given event ID must insert
// a new row and report isNew=true; the second call with the exact same
// event ID must report isNew=false, and exactly one row must exist for
// that event ID afterward.
func TestWebhookEventRepo_TryRecord_Dedup(t *testing.T) {
	pool := testutil.AdminDB(t)
	ctx := context.Background()

	repo := models.NewWebhookEventRepo()
	eventID := "evt_" + uuid.NewString()
	payload := []byte(`{"event":"payment.captured"}`)

	isNew, err := repo.TryRecord(ctx, pool, eventID, "payment.captured", payload)
	require.NoError(t, err)
	require.True(t, isNew, "first TryRecord call for a new event ID must report isNew=true")

	isNew, err = repo.TryRecord(ctx, pool, eventID, "payment.captured", payload)
	require.NoError(t, err)
	require.False(t, isNew, "second TryRecord call for the same event ID must report isNew=false")

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE razorpay_event_id = $1`, eventID).Scan(&count))
	require.Equal(t, 1, count, "exactly one webhook_events row must exist for this event ID after two TryRecord calls")
}
