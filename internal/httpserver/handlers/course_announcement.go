package handlers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/worker"
)

// Task 5 Stage 7: course_announcement had a model (Stage 2, models/
// course_announcement.go) but no handler until this stage. Create is
// authoring-gated (owner/teacher); List needs only RequireEntitlement
// since any enrolled learner should be able to read a course's
// announcements.

type createAnnouncementRequest struct {
	Title string `json:"title" binding:"required"`
	Body  string `json:"body" binding:"required"`
}

// CreateAnnouncement creates a course_announcement row and enqueues one
// course-announcement-posted notification job per currently-enrolled
// (access_status='active') learner. Fan-out is one job per learner —
// fine for MVP course sizes; no batch/digest email system is built here
// (matching Task 4's "don't over-build" precedent).
func CreateAnnouncement(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createAnnouncementRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		announcement, err := d.Announcements.Create(ctx, tx, course.OrgID, course.ID, req.Title, req.Body, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		learnerIDs, err := d.LearnerCourseAccess.ListActiveLearnerIDsByCourse(ctx, tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		// Enqueue-only, never call Resend synchronously in the request
		// path. A failure to enqueue any single learner's job is logged
		// but does not fail the request or block enqueuing the rest — the
		// announcement itself has already been created and is the primary
		// action; per-learner notification is a best-effort side effect.
		for _, learnerID := range learnerIDs {
			if err := worker.EnqueueCourseAnnouncementNotification(d.AsyncQueue, worker.NotifyCourseAnnouncementPayload{
				LearnerID:         learnerID,
				CourseID:          course.ID,
				CourseTitle:       course.Title,
				AnnouncementTitle: announcement.Title,
			}); err != nil {
				slog.Default().Error("handlers: failed to enqueue course-announcement notification", "error", err, "learner_id", learnerID, "course_id", course.ID)
			}
		}

		c.JSON(http.StatusCreated, courseAnnouncementResponse(announcement))
	}
}

// ListAnnouncements returns a course's announcements, newest first.
// Gated by RequireEntitlement — any enrolled learner (or owner/teacher/
// platform owner) may read them.
func ListAnnouncements(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		announcements, err := d.Announcements.ListByCourse(ctx, tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]gin.H, len(announcements))
		for i, a := range announcements {
			out[i] = courseAnnouncementResponse(a)
		}
		c.JSON(http.StatusOK, gin.H{"announcements": out})
	}
}

func courseAnnouncementResponse(a *models.CourseAnnouncement) gin.H {
	return gin.H{
		"id":           a.ID,
		"course_id":    a.CourseID,
		"title":        a.Title,
		"body":         a.Body,
		"created_by":   a.CreatedBy,
		"published_at": a.PublishedAt,
	}
}
