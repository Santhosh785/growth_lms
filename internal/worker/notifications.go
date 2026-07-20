// Task 5 Stage 7: async notification dispatch. Four notification types
// (grilling-record.md Q7 / task-5-learner-journey.md section 10):
// assignment-graded, certificate-issued, course-announcement-posted, and
// course-reminder. Every one of them is enqueued here and ONLY here —
// never sent synchronously in the request path — matching this package's
// existing bunny-transcode/scheduled-publish precedent (tasks.go,
// publish.go) and the spec's explicit acceptance criterion for this
// stage.
//
// course-reminder has no synchronous trigger point anywhere in this
// codebase: the spec describes it as a periodic "you're X% complete,
// keep going" nudge, which would need a cron-style sweep (like
// publish.go's runPublishSweepLoop) iterating every active enrollment on
// some schedule. That sweep is explicitly OUT OF SCOPE for this stage —
// matching Task 4's precedent of not over-building things nobody asked
// for yet. What IS built: the task type, payload, Enqueue function, and
// worker handler, so a future scheduler (or an admin/ops action) has a
// real mechanism to call. FLAGGED GAP: no cron/periodic scheduler enqueues
// TypeNotifyCourseReminder anywhere yet.
package worker

import (
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

const (
	TypeNotifyAssignmentGraded   = "notify:assignment_graded"
	TypeNotifyCertificateIssued  = "notify:certificate_issued"
	TypeNotifyCourseAnnouncement = "notify:course_announcement_posted"
	TypeNotifyCourseReminder     = "notify:course_reminder"
)

// NotifyAssignmentGradedPayload is enqueued by assignment.go's
// GradeSubmission handler once a teacher's grade has been persisted.
type NotifyAssignmentGradedPayload struct {
	LearnerID       string  `json:"learner_id"`
	CourseID        string  `json:"course_id"`
	CourseTitle     string  `json:"course_title"`
	GradePercentage float64 `json:"grade_percentage"`
	FeedbackText    string  `json:"feedback_text"`
}

// NotifyCertificateIssuedPayload is enqueued by completion.go's
// evaluateAndIssueCertificateIfComplete once a learner_certificate row has
// been created.
type NotifyCertificateIssuedPayload struct {
	LearnerID     string `json:"learner_id"`
	CourseID      string `json:"course_id"`
	CourseTitle   string `json:"course_title"`
	CertificateID string `json:"certificate_id"`
}

// NotifyCourseAnnouncementPayload is enqueued once per enrolled learner by
// the course-announcement creation handler.
type NotifyCourseAnnouncementPayload struct {
	LearnerID         string `json:"learner_id"`
	CourseID          string `json:"course_id"`
	CourseTitle       string `json:"course_title"`
	AnnouncementTitle string `json:"announcement_title"`
}

// NotifyCourseReminderPayload has no synchronous trigger point yet (see
// package comment) — EnqueueCourseReminderNotification exists so a future
// scheduler or manual/admin action has something real to call rather than
// nothing at all.
type NotifyCourseReminderPayload struct {
	LearnerID       string  `json:"learner_id"`
	CourseID        string  `json:"course_id"`
	CourseTitle     string  `json:"course_title"`
	PercentComplete float64 `json:"percent_complete"`
}

func EnqueueAssignmentGradedNotification(client *asynq.Client, payload NotifyAssignmentGradedPayload) error {
	return enqueueNotification(client, TypeNotifyAssignmentGraded, payload)
}

func EnqueueCertificateIssuedNotification(client *asynq.Client, payload NotifyCertificateIssuedPayload) error {
	return enqueueNotification(client, TypeNotifyCertificateIssued, payload)
}

func EnqueueCourseAnnouncementNotification(client *asynq.Client, payload NotifyCourseAnnouncementPayload) error {
	return enqueueNotification(client, TypeNotifyCourseAnnouncement, payload)
}

// EnqueueCourseReminderNotification is manually-triggerable only in this
// stage — no periodic scheduler calls it yet (see package comment).
func EnqueueCourseReminderNotification(client *asynq.Client, payload NotifyCourseReminderPayload) error {
	return enqueueNotification(client, TypeNotifyCourseReminder, payload)
}

func enqueueNotification(client *asynq.Client, taskType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("worker: marshal %s payload: %w", taskType, err)
	}
	if _, err := client.Enqueue(asynq.NewTask(taskType, data), asynq.Queue(QueueDefault)); err != nil {
		return fmt.Errorf("worker: enqueue %s task: %w", taskType, err)
	}
	return nil
}
