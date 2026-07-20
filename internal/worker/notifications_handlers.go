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

// notificationRecipient resolves a learner's send-to email and
// notification_opt_out flag. Runs with the pool's own admin privileges —
// same trust boundary as every other worker task (see worker.go's Run
// comment) — there's no per-request caller to scope RLS to for a
// background job.
func notificationRecipient(ctx context.Context, pool *pgxpool.Pool, profiles *models.ProfileRepo, learnerID string) (*models.Profile, error) {
	profile, err := profiles.GetByID(ctx, pool, learnerID)
	if err != nil {
		return nil, fmt.Errorf("worker: load learner profile %s: %w", learnerID, err)
	}
	return profile, nil
}

// sendIfOptedIn is the shared "skip send, still ack the job" gate every
// notification handler below uses: an opted-out learner's job succeeds
// (so asynq doesn't retry it forever) but never reaches the Resend call.
func sendIfOptedIn(ctx context.Context, email notify.EmailClient, profile *models.Profile, subject, body string) error {
	if profile.NotificationOptOut {
		return nil
	}
	return email.SendEmail(ctx, profile.Email, subject, body)
}

// handleNotifyAssignmentGraded sends "Your assignment has been graded.
// Feedback: ..." per task-5-learner-journey.md section 10.
func handleNotifyAssignmentGraded(pool *pgxpool.Pool, profiles *models.ProfileRepo, email notify.EmailClient) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload NotifyAssignmentGradedPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("worker: unmarshal assignment-graded payload: %w", err)
		}

		profile, err := notificationRecipient(ctx, pool, profiles, payload.LearnerID)
		if err != nil {
			return err
		}

		feedback := payload.FeedbackText
		if feedback == "" {
			feedback = "(no feedback provided)"
		}
		subject := fmt.Sprintf("Your assignment in %s has been graded", payload.CourseTitle)
		body := fmt.Sprintf("<p>Your assignment has been graded. Feedback: %s</p><p>Grade: %.1f%%</p>", feedback, payload.GradePercentage)

		return sendIfOptedIn(ctx, email, profile, subject, body)
	}
}

// handleNotifyCertificateIssued sends "Congratulations! You've completed
// [Course Name]. Your certificate is ready."
func handleNotifyCertificateIssued(pool *pgxpool.Pool, profiles *models.ProfileRepo, email notify.EmailClient) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload NotifyCertificateIssuedPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("worker: unmarshal certificate-issued payload: %w", err)
		}

		profile, err := notificationRecipient(ctx, pool, profiles, payload.LearnerID)
		if err != nil {
			return err
		}

		subject := fmt.Sprintf("Congratulations on completing %s!", payload.CourseTitle)
		body := fmt.Sprintf("<p>Congratulations! You've completed %s. Your certificate is ready (certificate ID: %s).</p>", payload.CourseTitle, payload.CertificateID)

		return sendIfOptedIn(ctx, email, profile, subject, body)
	}
}

// handleNotifyCourseAnnouncement sends "[Announcement title] posted in
// [Course Name]."
func handleNotifyCourseAnnouncement(pool *pgxpool.Pool, profiles *models.ProfileRepo, email notify.EmailClient) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload NotifyCourseAnnouncementPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("worker: unmarshal course-announcement payload: %w", err)
		}

		profile, err := notificationRecipient(ctx, pool, profiles, payload.LearnerID)
		if err != nil {
			return err
		}

		subject := fmt.Sprintf("New announcement in %s", payload.CourseTitle)
		body := fmt.Sprintf("<p>%s posted in %s.</p>", payload.AnnouncementTitle, payload.CourseTitle)

		return sendIfOptedIn(ctx, email, profile, subject, body)
	}
}

// handleNotifyCourseReminder sends "Continue learning [Course Name].
// You're [X%] complete." No code path enqueues this task yet in this
// stage (see notifications.go's package comment) — the handler exists so
// the mechanism is real and testable ahead of a future scheduler.
func handleNotifyCourseReminder(pool *pgxpool.Pool, profiles *models.ProfileRepo, email notify.EmailClient) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload NotifyCourseReminderPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("worker: unmarshal course-reminder payload: %w", err)
		}

		profile, err := notificationRecipient(ctx, pool, profiles, payload.LearnerID)
		if err != nil {
			return err
		}

		subject := fmt.Sprintf("Continue learning %s", payload.CourseTitle)
		body := fmt.Sprintf("<p>Continue learning %s. You're %.0f%% complete.</p>", payload.CourseTitle, payload.PercentComplete)

		return sendIfOptedIn(ctx, email, profile, subject, body)
	}
}
