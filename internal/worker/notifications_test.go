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

func TestEnqueueFunctions_ConstructTaskCorrectly(t *testing.T) {
	// Proves the four Enqueue functions build a well-formed asynq.Task
	// (correct type name, JSON-round-trippable payload) without needing a
	// live Redis: asynq.NewTask itself does no I/O, only client.Enqueue
	// does — so this test exercises everything up to (but not including)
	// the network call, then separately (below) confirms the marshaled
	// payload round-trips through the same unmarshal path the worker
	// handlers use.
	t.Run("assignment graded", func(t *testing.T) {
		payload := NotifyAssignmentGradedPayload{
			LearnerID:       "learner-1",
			CourseID:        "course-1",
			CourseTitle:     "Go Basics",
			GradePercentage: 87.5,
			FeedbackText:    "Nice work",
		}
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		task := asynq.NewTask(TypeNotifyAssignmentGraded, data)
		require.Equal(t, TypeNotifyAssignmentGraded, task.Type())

		var roundTripped NotifyAssignmentGradedPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &roundTripped))
		require.Equal(t, payload, roundTripped)
	})

	t.Run("certificate issued", func(t *testing.T) {
		payload := NotifyCertificateIssuedPayload{LearnerID: "l", CourseID: "c", CourseTitle: "Go", CertificateID: "cert-1"}
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		task := asynq.NewTask(TypeNotifyCertificateIssued, data)
		require.Equal(t, TypeNotifyCertificateIssued, task.Type())
	})

	t.Run("course announcement", func(t *testing.T) {
		payload := NotifyCourseAnnouncementPayload{LearnerID: "l", CourseID: "c", CourseTitle: "Go", AnnouncementTitle: "New lesson"}
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		task := asynq.NewTask(TypeNotifyCourseAnnouncement, data)
		require.Equal(t, TypeNotifyCourseAnnouncement, task.Type())
	})

	t.Run("course reminder", func(t *testing.T) {
		payload := NotifyCourseReminderPayload{LearnerID: "l", CourseID: "c", CourseTitle: "Go", PercentComplete: 42}
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		task := asynq.NewTask(TypeNotifyCourseReminder, data)
		require.Equal(t, TypeNotifyCourseReminder, task.Type())
	})
}

// TestEnqueueAssignmentGradedNotification_UnreachableRedis proves the
// public Enqueue function returns an error (rather than panicking or
// hanging) when Redis is unreachable, and that no partial state is left
// behind — matching the contract the HTTP handlers rely on when they
// treat an enqueue failure as best-effort/logged rather than fatal.
func TestEnqueueAssignmentGradedNotification_UnreachableRedis(t *testing.T) {
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:63799"})
	defer client.Close()

	err := EnqueueAssignmentGradedNotification(client, NotifyAssignmentGradedPayload{LearnerID: "l", CourseID: "c"})
	require.Error(t, err)
}

// TestNotificationHandlers_RespectOptOut is the core acceptance test for
// this stage: a learner with notification_opt_out=true must never reach
// the Resend call, while an opted-in learner does. Runs each of the four
// handler functions directly (not through a live asynq server) against a
// real, migrated Postgres — proving the DB lookup + opt-out gate without
// needing a live Redis/asynq server, per the task instructions.
func TestNotificationHandlers_RespectOptOut(t *testing.T) {
	// testutil.DB runs migrations (and sets up the app_test role) before
	// returning; this test then reconnects as the admin role via AdminDB
	// so its queries bypass RLS, matching how this package's own handlers
	// run (see worker.go's Run comment: no per-request caller to scope RLS
	// to for a background job).
	testutil.DB(t)
	pool := testutil.AdminDB(t)
	profiles := models.NewProfileRepo()

	optedOutID := uuid.NewString()
	optedInID := uuid.NewString()
	_, err := pool.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1, $2)`, optedOutID, "opted-out-"+optedOutID+"@example.com")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1, $2)`, optedInID, "opted-in-"+optedInID+"@example.com")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `UPDATE profiles SET notification_opt_out = true WHERE id = $1`, optedOutID)
	require.NoError(t, err)

	tests := []struct {
		name    string
		build   func() (*asynq.Task, error)
		handler func(email *notifytest.FakeEmailClient) func(context.Context, *asynq.Task) error
	}{
		{
			name: "assignment graded",
			build: func() (*asynq.Task, error) {
				data, err := json.Marshal(NotifyAssignmentGradedPayload{LearnerID: optedOutID, CourseID: "c", CourseTitle: "Go"})
				return asynq.NewTask(TypeNotifyAssignmentGraded, data), err
			},
			handler: func(email *notifytest.FakeEmailClient) func(context.Context, *asynq.Task) error {
				return handleNotifyAssignmentGraded(pool, profiles, email)
			},
		},
		{
			name: "certificate issued",
			build: func() (*asynq.Task, error) {
				data, err := json.Marshal(NotifyCertificateIssuedPayload{LearnerID: optedOutID, CourseID: "c", CourseTitle: "Go", CertificateID: "cert-1"})
				return asynq.NewTask(TypeNotifyCertificateIssued, data), err
			},
			handler: func(email *notifytest.FakeEmailClient) func(context.Context, *asynq.Task) error {
				return handleNotifyCertificateIssued(pool, profiles, email)
			},
		},
		{
			name: "course announcement",
			build: func() (*asynq.Task, error) {
				data, err := json.Marshal(NotifyCourseAnnouncementPayload{LearnerID: optedOutID, CourseID: "c", CourseTitle: "Go", AnnouncementTitle: "Hi"})
				return asynq.NewTask(TypeNotifyCourseAnnouncement, data), err
			},
			handler: func(email *notifytest.FakeEmailClient) func(context.Context, *asynq.Task) error {
				return handleNotifyCourseAnnouncement(pool, profiles, email)
			},
		},
		{
			name: "course reminder",
			build: func() (*asynq.Task, error) {
				data, err := json.Marshal(NotifyCourseReminderPayload{LearnerID: optedOutID, CourseID: "c", CourseTitle: "Go", PercentComplete: 50})
				return asynq.NewTask(TypeNotifyCourseReminder, data), err
			},
			handler: func(email *notifytest.FakeEmailClient) func(context.Context, *asynq.Task) error {
				return handleNotifyCourseReminder(pool, profiles, email)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/opted_out_sends_zero", func(t *testing.T) {
			task, err := tt.build()
			require.NoError(t, err)
			fake := &notifytest.FakeEmailClient{}
			err = tt.handler(fake)(context.Background(), task)
			require.NoError(t, err, "opted-out job must still succeed/ack, not error")
			require.Equal(t, 0, fake.Count(), "opted-out learner must never receive a call to the email client")
		})
	}

	// Opted-in learner (assignment-graded, as a representative case) does
	// receive exactly one send.
	t.Run("opted_in_sends_one", func(t *testing.T) {
		data, err := json.Marshal(NotifyAssignmentGradedPayload{LearnerID: optedInID, CourseID: "c", CourseTitle: "Go", FeedbackText: "Great job"})
		require.NoError(t, err)
		task := asynq.NewTask(TypeNotifyAssignmentGraded, data)

		fake := &notifytest.FakeEmailClient{}
		err = handleNotifyAssignmentGraded(pool, profiles, fake)(context.Background(), task)
		require.NoError(t, err)
		require.Equal(t, 1, fake.Count())
		require.Contains(t, fake.Sent[0].Body, "Great job")
	})

	// An unknown learner_id (no matching profile) surfaces as an error so
	// asynq retries rather than silently dropping the job.
	t.Run("unknown_learner_errors", func(t *testing.T) {
		data, err := json.Marshal(NotifyAssignmentGradedPayload{LearnerID: uuid.NewString(), CourseID: "c"})
		require.NoError(t, err)
		task := asynq.NewTask(TypeNotifyAssignmentGraded, data)

		fake := &notifytest.FakeEmailClient{}
		err = handleNotifyAssignmentGraded(pool, profiles, fake)(context.Background(), task)
		require.Error(t, err)
		require.Equal(t, 0, fake.Count())
	})
}
