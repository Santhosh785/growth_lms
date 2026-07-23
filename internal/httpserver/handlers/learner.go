package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// defaultWatchThresholdPercent is applied when a lesson's
// watch_threshold_percent is NULL (see grilling-record.md Q3).
const defaultWatchThresholdPercent = 80

// EnrollCourse is a self-service, free enrollment: any org member (not
// gated by RequireEntitlement/RequireRole — ResolveCourseOrg already
// guarantees the caller is a member or platform owner, since it 404s
// otherwise) can enroll themselves in a published course, provided every
// course_prerequisite is already complete for them. Idempotent: a caller
// who already holds an access row for this course gets it back unchanged
// (200) instead of a duplicate-key error.
func EnrollCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		if course.Status != models.CourseStatusPublished {
			c.JSON(http.StatusConflict, gin.H{"error": "course is not published"})
			return
		}

		existing, err := d.LearnerCourseAccess.Get(ctx, tx, ac.UserID, course.ID)
		if err == nil {
			c.JSON(http.StatusOK, learnerCourseAccessResponse(existing))
			return
		}
		if !errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		prereqIDs, err := d.CoursePrereqs.ListForCourse(ctx, tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		var incomplete []string
		for _, prereqCourseID := range prereqIDs {
			// A prerequisite course is "complete" for this learner if they
			// hold a certificate for it — certificate issuance IS course
			// completion in this system (see grilling-record.md Q9;
			// completion-rule evaluation itself is built in Stage 6).
			if _, cerr := d.Certificates.GetByLearnerAndCourse(ctx, tx, ac.UserID, prereqCourseID); cerr != nil {
				if errors.Is(cerr, models.ErrNotFound) {
					incomplete = append(incomplete, prereqCourseID)
					continue
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		if len(incomplete) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":                    "prerequisite courses not yet completed",
				"incomplete_prerequisites": incomplete,
			})
			return
		}

		access, err := d.LearnerCourseAccess.Create(ctx, tx, course.OrgID, ac.UserID, course.ID, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.AnalyticsEvents.Record(ctx, tx, course.OrgID, models.EventEnrollment, ac.UserID, course.ID, nil)
		c.JSON(http.StatusCreated, learnerCourseAccessResponse(access))
	}
}

func learnerCourseAccessResponse(a *models.LearnerCourseAccess) gin.H {
	return gin.H{
		"id":             a.ID,
		"org_id":         a.OrgID,
		"learner_id":     a.LearnerID,
		"course_id":      a.CourseID,
		"entitlement_id": a.EntitlementID,
		"enrolled_at":    a.EnrolledAt,
		"access_status":  a.AccessStatus,
	}
}

// loadCourseStructureWithProgress returns every chapter (in order) with
// its lessons (in order), and a lookup of this learner's completion state
// per lesson — shared by GetPlayer and GetCourseProgress so both endpoints
// agree on lesson ordering/counting.
func loadCourseStructureWithProgress(c *gin.Context, d *AuthDeps, courseID, learnerID string) ([]*models.Chapter, map[string][]*models.Lesson, map[string]bool, error) {
	tx, _ := middleware.RequestTxFromGin(c)
	ctx := c.Request.Context()

	chapters, err := d.Chapters.ListByCourse(ctx, tx, courseID)
	if err != nil {
		return nil, nil, nil, err
	}
	lessonsByChapter := make(map[string][]*models.Lesson, len(chapters))
	for _, ch := range chapters {
		lessons, err := d.Lessons.ListByChapter(ctx, tx, ch.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		lessonsByChapter[ch.ID] = lessons
	}

	progress, err := d.LearnerProgress.ListByCourse(ctx, tx, learnerID, courseID)
	if err != nil {
		return nil, nil, nil, err
	}
	completed := make(map[string]bool, len(progress))
	for _, p := range progress {
		if p.CompletedAt != nil {
			completed[p.LessonID] = true
		}
	}

	return chapters, lessonsByChapter, completed, nil
}

// GetPlayer returns the learner's current lesson (from
// learner_resume_position, or the first lesson in course order if the
// learner has never opened the player before) plus enough chapter/lesson
// structure — with per-lesson completion flags — for the player to render
// navigation. Gated by RequireEntitlement.
func GetPlayer(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		chapters, lessonsByChapter, completed, err := loadCourseStructureWithProgress(c, d, course.ID, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		var currentLessonID *string
		if resume, err := d.ResumePositions.Get(ctx, tx, ac.UserID, course.ID); err == nil {
			currentLessonID = &resume.CurrentLessonID
		} else if !errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if currentLessonID == nil {
			for _, ch := range chapters {
				if lessons := lessonsByChapter[ch.ID]; len(lessons) > 0 {
					id := lessons[0].ID
					currentLessonID = &id
					break
				}
			}
		}

		chapterOut := make([]gin.H, len(chapters))
		for i, ch := range chapters {
			lessons := lessonsByChapter[ch.ID]
			lessonOut := make([]gin.H, len(lessons))
			for j, lsn := range lessons {
				lessonOut[j] = gin.H{
					"id":        lsn.ID,
					"title":     lsn.Title,
					"completed": completed[lsn.ID],
				}
			}
			chapterOut[i] = gin.H{
				"id":      ch.ID,
				"title":   ch.Title,
				"lessons": lessonOut,
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"current_lesson_id": currentLessonID,
			"chapters":          chapterOut,
		})
	}
}

// ResumeLesson sets/updates the learner's resume pointer to the given
// lesson, called when the learner navigates to it in the player. Gated by
// RequireEntitlement.
func ResumeLesson(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}

		resume, err := d.ResumePositions.Upsert(ctx, tx, course.OrgID, ac.UserID, course.ID, lesson.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"course_id":         resume.CourseID,
			"current_lesson_id": resume.CurrentLessonID,
			"last_resumed_at":   resume.LastResumedAt,
		})
	}
}

type lessonProgressRequest struct {
	WatchedDurationMs int64   `json:"watched_duration_ms" binding:"min=0"`
	WatchPercentage   float64 `json:"watch_percentage" binding:"min=0,max=100"`
}

// ReportLessonProgress accepts a video watch-progress ping, upserts the
// high-water mark into learner_lesson_progress, and marks the lesson
// complete (idempotently) once watch_percentage crosses the lesson's
// watch_threshold_percent (or the 80% default). Gated by RequireEntitlement.
func ReportLessonProgress(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req lessonProgressRequest
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

		progress, err := d.LearnerProgress.UpsertWatchProgress(ctx, tx, course.OrgID, ac.UserID, lesson.ID, course.ID, req.WatchedDurationMs, req.WatchPercentage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		threshold := defaultWatchThresholdPercent
		if lesson.WatchThresholdPercent != nil {
			threshold = *lesson.WatchThresholdPercent
		}
		if req.WatchPercentage >= float64(threshold) {
			progress, err = d.LearnerProgress.MarkComplete(ctx, tx, course.OrgID, ac.UserID, lesson.ID, course.ID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			if err := evaluateAndIssueCertificateIfComplete(ctx, tx, d, course.ID, ac.UserID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}

		c.JSON(http.StatusOK, lessonProgressResponse(progress))
	}
}

// CompleteLesson marks a non-video lesson (text/file/etc.) complete on
// first call — the player calls this the moment it renders such a lesson.
// Idempotent (MarkComplete never resets an existing completed_at). Gated
// by RequireEntitlement.
func CompleteLesson(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		lesson, ok := lessonInCourse(c, d, course.ID)
		if !ok {
			return
		}

		progress, err := d.LearnerProgress.MarkComplete(ctx, tx, course.OrgID, ac.UserID, lesson.ID, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if err := evaluateAndIssueCertificateIfComplete(ctx, tx, d, course.ID, ac.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.AnalyticsEvents.Record(ctx, tx, course.OrgID, models.EventLessonCompletion, ac.UserID, course.ID, nil)
		c.JSON(http.StatusOK, lessonProgressResponse(progress))
	}
}

// GetCourseProgress returns the learner's course-progress percentage:
// completed lesson count / total lesson count across the course. Gated by
// RequireEntitlement.
func GetCourseProgress(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		_, lessonsByChapter, completed, err := loadCourseStructureWithProgress(c, d, course.ID, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		total := 0
		completedCount := 0
		for _, lessons := range lessonsByChapter {
			for _, lsn := range lessons {
				total++
				if completed[lsn.ID] {
					completedCount++
				}
			}
		}

		var percentage float64
		if total > 0 {
			percentage = float64(completedCount) / float64(total) * 100
		}

		c.JSON(http.StatusOK, gin.H{
			"completed_lessons": completedCount,
			"total_lessons":     total,
			"percentage":        percentage,
		})
	}
}

// lessonInCourse loads :lessonId and verifies it belongs to the course
// already resolved by ResolveCourseOrg, writing a 404 response and
// returning ok=false if not — every learner lesson-scoped endpoint must
// not trust a client-supplied lessonId that belongs to a different course.
func lessonInCourse(c *gin.Context, d *AuthDeps, courseID string) (*models.Lesson, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	lesson, err := d.Lessons.Get(c.Request.Context(), tx, c.Param("lessonId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "lesson not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if lesson.CourseID != courseID {
		c.JSON(http.StatusNotFound, gin.H{"error": "lesson not found"})
		return nil, false
	}
	return lesson, true
}

func lessonProgressResponse(p *models.LearnerLessonProgress) gin.H {
	return gin.H{
		"lesson_id":           p.LessonID,
		"course_id":           p.CourseID,
		"completed_at":        p.CompletedAt,
		"watched_duration_ms": p.WatchedDurationMs,
		"watch_percentage":    p.WatchPercentage,
	}
}
