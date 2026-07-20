package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/worker"
)

// assignmentBlockInLesson loads :blockId, verifies it belongs to the given
// lesson (which lessonInCourse has already verified belongs to the
// course), and that it is actually an assignment block — writing a 404
// response and returning ok=false otherwise. Mirrors quizBlockInLesson's
// don't-trust-client-supplied-IDs pattern.
func assignmentBlockInLesson(c *gin.Context, d *AuthDeps, lessonID string) (*models.Block, *models.AssignmentBlockContent, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	block, err := d.Blocks.Get(c.Request.Context(), tx, c.Param("blockId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "assignment not found"})
			return nil, nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}
	if block.LessonID != lessonID || block.Type != models.BlockTypeAssignment {
		c.JSON(http.StatusNotFound, gin.H{"error": "assignment not found"})
		return nil, nil, false
	}

	var content models.AssignmentBlockContent
	if err := json.Unmarshal(block.Content, &content); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}
	return block, &content, true
}

// computeDueDateStatus compares now against a possibly-nil due date and
// returns models.DueDateStatusOnTime/DueDateStatusLate. A nil due date
// (assignment has no deadline) is always on_time. Extracted as a pure
// function so it's cheaply unit-testable without any HTTP/DB scaffolding.
func computeDueDateStatus(now time.Time, dueDate *time.Time) string {
	if dueDate == nil {
		return models.DueDateStatusOnTime
	}
	if now.After(*dueDate) {
		return models.DueDateStatusLate
	}
	return models.DueDateStatusOnTime
}

// UploadAssignmentSubmission returns a signed Supabase Storage upload URL
// for the learner to PUT their submission file to, at path
// org/{org_id}/courses/{course_id}/submissions/{learner_id}/{assignment_block_id}/{filename}.
// Unlike Task 4's media upload flow, NO assets row is created here — a
// learner submission file is not course content, it's tracked only via
// learner_assignment_submission.file_path once /submit confirms the
// upload. Gated by RequireEntitlement.
func UploadAssignmentSubmission(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Filename string `json:"filename" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}
		block, _, ok := assignmentBlockInLesson(c, d, lesson.ID)
		if !ok {
			return
		}

		storageKey := "org/" + course.OrgID + "/courses/" + course.ID + "/submissions/" + ac.UserID + "/" + block.ID + "/" + req.Filename

		uploadURL, expiresAt, err := d.Storage.CreateSignedUploadURL(ctx, d.Config.Supabase.StorageBucket, storageKey)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create upload url"})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"storage_key": storageKey,
			"upload_url":  uploadURL,
			"expires_at":  expiresAt,
		})
	}
}

// SubmitAssignment confirms a previously PUT-uploaded submission file via a
// server-side HEAD check against Supabase Storage (never trusting the
// client-reported completion alone, same pattern as Task 4's
// UploadFileComplete), computes the next submission_number and
// due_date_status, and creates the learner_assignment_submission row.
//
// Resubmission rule (grilling-record.md Q1's spec text plus main-plan.md's
// Stage 5 simplification): if the assignment's allow_resubmission is true,
// a new submission is always allowed (submission_number+1); if false, a
// second submission of any kind is rejected with 409 once one already
// exists. Gated by RequireEntitlement.
func SubmitAssignment(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			StorageKey string `json:"storage_key" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}
		block, content, ok := assignmentBlockInLesson(c, d, lesson.ID)
		if !ok {
			return
		}

		// The storage key must actually belong to this learner+assignment —
		// a caller can't confirm an upload under someone else's path (or a
		// different assignment's path) by supplying an arbitrary key.
		expectedPrefix := "org/" + course.OrgID + "/courses/" + course.ID + "/submissions/" + ac.UserID + "/" + block.ID + "/"
		if len(req.StorageKey) < len(expectedPrefix) || req.StorageKey[:len(expectedPrefix)] != expectedPrefix {
			c.JSON(http.StatusBadRequest, gin.H{"error": "storage_key does not match this learner/assignment"})
			return
		}

		existingCount, err := d.AssignmentSubmissions.CountByLearnerAndBlock(ctx, tx, ac.UserID, block.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if existingCount > 0 && !content.AllowResubmission {
			c.JSON(http.StatusConflict, gin.H{"error": "resubmission is not allowed for this assignment"})
			return
		}

		_, exists, err := d.Storage.HeadObject(ctx, d.Config.Supabase.StorageBucket, req.StorageKey)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to verify upload"})
			return
		}
		if !exists {
			c.JSON(http.StatusConflict, gin.H{"error": "object was not found in storage; upload did not complete"})
			return
		}

		submissionNumber := existingCount + 1
		dueDateStatus := computeDueDateStatus(time.Now(), content.DueDate)
		submissionStatus := models.SubmissionStatusSubmitted
		if existingCount > 0 {
			submissionStatus = models.SubmissionStatusResubmitted
		}

		submission, err := d.AssignmentSubmissions.Create(ctx, tx, course.OrgID, ac.UserID, block.ID, submissionNumber, req.StorageKey, submissionStatus, dueDateStatus)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		// An assignment block being submitted is one of the four ways a
		// lesson completes per spec (video threshold / non-video first-view
		// / quiz pass / assignment submitted) — there's no pass/fail for an
		// assignment, submission itself is what completes the lesson. This
		// was a gap in Stage 5 (assignment.go never called MarkComplete);
		// fixed here in Stage 6 alongside the other three call sites so all
		// four uniformly feed into certificate evaluation.
		if _, err := d.LearnerProgress.MarkComplete(ctx, tx, course.OrgID, ac.UserID, lesson.ID, course.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if err := evaluateAndIssueCertificateIfComplete(ctx, tx, d, course.ID, ac.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusCreated, assignmentSubmissionResponse(submission, nil))
	}
}

// GetAssignmentSubmissions returns the learner's own submission and grade
// history for an assignment block (via ListSubmissionsWithGrades), newest
// submission first, so they can see all previous feedback. Gated by
// RequireEntitlement.
func GetAssignmentSubmissions(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}
		block, _, ok := assignmentBlockInLesson(c, d, lesson.ID)
		if !ok {
			return
		}

		submissions, grades, err := d.AssignmentGrades.ListSubmissionsWithGrades(ctx, tx, ac.UserID, block.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]gin.H, len(submissions))
		for i, s := range submissions {
			out[i] = assignmentSubmissionResponse(s, grades[i])
		}
		c.JSON(http.StatusOK, gin.H{"submissions": out})
	}
}

// ListCourseSubmissions is the teacher grading queue for the whole course:
// every submission still awaiting grading (ListPendingByCourse), oldest
// first. Gated by the authoring role (owner/teacher), not RequireEntitlement.
func ListCourseSubmissions(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		submissions, err := d.AssignmentSubmissions.ListPendingByCourse(ctx, tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]gin.H, len(submissions))
		for i, s := range submissions {
			out[i] = assignmentSubmissionResponse(s, nil)
		}
		c.JSON(http.StatusOK, gin.H{"submissions": out})
	}
}

type gradeSubmissionRequest struct {
	GradePercentage float64 `json:"grade_percentage" binding:"required,min=0,max=100"`
	FeedbackText    string  `json:"feedback_text"`
}

// GradeSubmission records a teacher's grade for a submission and moves it
// to submission_status='graded'. It verifies the submission actually
// belongs to a block within this course (via the block's course_id),
// mirroring lessonInCourse's don't-trust-client-supplied-IDs pattern, so a
// teacher entitled to grade course A can't grade-queue-probe or grade a
// submission belonging to course B via a mismatched submissionId.
//
// Stage 7: after recording the grade, enqueues an assignment-graded
// notification for the learner (enqueue-only — see the enqueue call
// below for why a failure there doesn't fail this request).
func GradeSubmission(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req gradeSubmissionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		submission, err := d.AssignmentSubmissions.Get(ctx, tx, c.Param("submissionId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		block, err := d.Blocks.Get(ctx, tx, submission.AssignmentBlockID)
		if err != nil || block.CourseID != course.ID {
			c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
			return
		}

		var feedback *string
		if req.FeedbackText != "" {
			feedback = &req.FeedbackText
		}

		grade, err := d.AssignmentGrades.Create(ctx, tx, course.OrgID, submission.ID, req.GradePercentage, feedback, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		updated, err := d.AssignmentSubmissions.SetStatus(ctx, tx, submission.ID, models.SubmissionStatusGraded)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		// Stage 7: enqueue-only — never call Resend synchronously in the
		// request path (spec + acceptance criterion). A failure to enqueue
		// (e.g. Redis unreachable) is logged but does not fail the grading
		// request; grading has already succeeded and the notification is a
		// best-effort side effect.
		if err := worker.EnqueueAssignmentGradedNotification(d.AsyncQueue, worker.NotifyAssignmentGradedPayload{
			LearnerID:       submission.LearnerID,
			CourseID:        course.ID,
			CourseTitle:     course.Title,
			GradePercentage: req.GradePercentage,
			FeedbackText:    req.FeedbackText,
		}); err != nil {
			slog.Default().Error("handlers: failed to enqueue assignment-graded notification", "error", err, "learner_id", submission.LearnerID, "submission_id", submission.ID)
		}

		c.JSON(http.StatusCreated, assignmentSubmissionResponse(updated, grade))
	}
}

func assignmentSubmissionResponse(s *models.LearnerAssignmentSubmission, g *models.LearnerAssignmentGrade) gin.H {
	out := gin.H{
		"id":                  s.ID,
		"org_id":              s.OrgID,
		"learner_id":          s.LearnerID,
		"assignment_block_id": s.AssignmentBlockID,
		"submission_number":   s.SubmissionNumber,
		"file_path":           s.FilePath,
		"submitted_at":        s.SubmittedAt,
		"submission_status":   s.SubmissionStatus,
		"due_date_status":     s.DueDateStatus,
	}
	if g != nil {
		out["grade"] = gin.H{
			"id":                   g.ID,
			"grade_percentage":     g.GradePercentage,
			"feedback_text":        g.FeedbackText,
			"graded_by_teacher_id": g.GradedByTeacherID,
			"graded_at":            g.GradedAt,
		}
	} else {
		out["grade"] = nil
	}
	return out
}
