package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/auth"
	"growth-lms/internal/dbctx"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// resolveOrgBySlugForCourseCreation is CreateCourse/ListCourses' own
// org-resolution step: unlike every other course-domain route, these two
// have no :courseId in their path to derive org context from (there's no
// course yet, or the caller wants courses across... no, exactly one org).
// The spec's documented paths are flat (POST/GET /api/courses, no
// :org_slug segment either), so the org is instead identified by an
// org_slug field in the request — resolved and role-checked here, the
// same way CreateOrg needs no pre-existing org context because it's
// inventing one.
func resolveOrgBySlugForCourseCreation(d *AuthDeps, c *gin.Context, orgSlug string) (orgID string, ok bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	ac, _ := middleware.AuthContextFromGin(c)
	ctx := c.Request.Context()

	if orgSlug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "org_slug is required"})
		return "", false
	}

	org, err := d.Orgs.GetBySlug(ctx, tx, orgSlug)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return "", false
	}

	role, err := d.Memberships.GetRole(ctx, tx, ac.UserID, org.ID)
	if err != nil || (role != auth.RoleOwner && role != auth.RoleTeacher) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return "", false
	}

	if err := dbctx.SetOrgContext(ctx, tx, org.ID, role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return "", false
	}
	return org.ID, true
}

type createCourseRequest struct {
	OrgSlug     string  `json:"org_slug" binding:"required"`
	Title       string  `json:"title" binding:"required,min=1,max=300"`
	Description string  `json:"description"`
	CategoryID  *string `json:"category_id"`
}

// CreateCourse creates a course in 'draft' status within the org named by
// org_slug in the request body. RLS additionally scopes the INSERT to
// that org — the role check above is defense-in-depth, not the boundary.
func CreateCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createCourseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		orgID, ok := resolveOrgBySlugForCourseCreation(d, c, req.OrgSlug)
		if !ok {
			return
		}

		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		course, err := d.Courses.Create(c.Request.Context(), tx, orgID, ac.UserID, req.Title, req.Description, req.CategoryID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, courseResponse(course))
	}
}

// GetCourse returns the course resolved by ResolveCourseOrg.
func GetCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		c.JSON(http.StatusOK, courseResponse(course))
	}
}

// ListCourses lists every course in the org named by the org_slug query
// parameter (same org-resolution rationale as CreateCourse — no
// :courseId exists yet to derive it from).
func ListCourses(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		ctx := c.Request.Context()

		orgSlug := c.Query("org_slug")
		if orgSlug == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "org_slug query parameter is required"})
			return
		}
		org, err := d.Orgs.GetBySlug(ctx, tx, orgSlug)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
			return
		}
		if _, err := d.Memberships.GetRole(ctx, tx, ac.UserID, org.ID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		courses, err := d.Courses.List(ctx, tx, org.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(courses))
		for i, course := range courses {
			out[i] = courseResponse(course)
		}
		c.JSON(http.StatusOK, gin.H{"courses": out})
	}
}

type updateCourseRequest struct {
	Title         string  `json:"title" binding:"required,min=1,max=300"`
	Description   string  `json:"description"`
	CategoryID    *string `json:"category_id"`
	CoverImageURL *string `json:"cover_image_url"`
}

// UpdateCourse updates caller-editable metadata only; status transitions
// go through the dedicated transition endpoints below.
func UpdateCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateCourseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		updated, err := d.Courses.UpdateMetadata(c.Request.Context(), tx, course.ID, req.Title, req.Description, req.CategoryID, req.CoverImageURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, courseResponse(updated))
	}
}

// DeleteCourse hard-deletes a course. Per spec, only allowed while the
// course is still 'draft' — draft courses are never learner-visible so no
// enrollment/attempt can exist against them. Non-draft courses must be
// archived instead (see ArchiveCourse).
func DeleteCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		if course.Status != models.CourseStatusDraft {
			c.JSON(http.StatusConflict, gin.H{"error": "only draft courses can be deleted; archive this course instead"})
			return
		}

		if err := d.Courses.Delete(c.Request.Context(), tx, course.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// validTransitions documents every allowed courses.status edge. Anything
// not listed here is rejected with 400 — the spec is explicit that
// transitions outside this documented flow must never silently apply.
var validTransitions = map[string]map[string]bool{
	models.CourseStatusDraft:       {models.CourseStatusReview: true, models.CourseStatusArchived: true},
	models.CourseStatusReview:      {models.CourseStatusDraft: true, models.CourseStatusPublished: true, models.CourseStatusScheduled: true, models.CourseStatusArchived: true},
	models.CourseStatusScheduled:   {models.CourseStatusReview: true, models.CourseStatusPublished: true, models.CourseStatusArchived: true},
	models.CourseStatusPublished:   {models.CourseStatusUnpublished: true, models.CourseStatusArchived: true},
	models.CourseStatusUnpublished: {models.CourseStatusArchived: true},
}

type transitionRequest struct {
	Status      string     `json:"status" binding:"required"`
	PublishDate *time.Time `json:"publish_date"`
}

// TransitionCourse handles every plain (non-publish) status change:
// draft<->review, review->scheduled, review->draft, published->
// unpublished, and *->archived. Publishing itself (review/scheduled/
// unpublished -> published) always goes through PublishCourse instead,
// since it has side effects (snapshot, published_at, video-readiness
// check) a plain transition doesn't.
func TransitionCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req transitionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		if req.Status == models.CourseStatusPublished {
			c.JSON(http.StatusBadRequest, gin.H{"error": "use POST /publish to transition to published"})
			return
		}
		if !validTransitions[course.Status][req.Status] {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("cannot transition from %q to %q", course.Status, req.Status)})
			return
		}

		var updated *models.Course
		var err error
		switch {
		case req.Status == models.CourseStatusArchived:
			updated, err = d.Courses.Archive(c.Request.Context(), tx, course.ID)
		case req.Status == models.CourseStatusScheduled:
			if req.PublishDate == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "publish_date is required to schedule a course"})
				return
			}
			updated, err = d.Courses.SetScheduled(c.Request.Context(), tx, course.ID, *req.PublishDate)
		default:
			updated, err = d.Courses.SetStatus(c.Request.Context(), tx, course.ID, req.Status)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, courseResponse(updated))
	}
}

// PublishCourse atomically transitions review/scheduled/unpublished ->
// published: rejects if any video block's asset isn't ready, sets
// published_at, and unconditionally creates a course_versions snapshot
// (publish always snapshots, even if content is unchanged since the last
// one).
func PublishCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		allowed := course.Status == models.CourseStatusReview ||
			course.Status == models.CourseStatusScheduled ||
			course.Status == models.CourseStatusUnpublished
		if !allowed {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("cannot publish from status %q", course.Status)})
			return
		}

		videoBlocks, err := d.Blocks.ListVideoBlocksByCourse(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		var notReady []string
		for _, b := range videoBlocks {
			var content models.VideoBlockContent
			_ = jsonUnmarshalBlockContent(b, &content)
			asset, err := d.Assets.Get(c.Request.Context(), tx, content.AssetID)
			if err != nil || asset.ProcessingStatus != models.ProcessingStatusReady {
				notReady = append(notReady, b.ID)
			}
		}
		if len(notReady) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error":  "cannot publish: some video blocks are not finished processing",
				"blocks": notReady,
			})
			return
		}

		updated, err := d.Courses.Publish(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if _, err := d.CourseVersions.Snapshot(c.Request.Context(), tx, course.ID, course.OrgID, ac.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, courseResponse(updated))
	}
}

// UnpublishCourse transitions published -> unpublished; learners lose
// access immediately (enforced by Task 5's learner-visibility rules,
// which key off status alone).
func UnpublishCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		if course.Status != models.CourseStatusPublished {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("cannot unpublish from status %q", course.Status)})
			return
		}
		updated, err := d.Courses.SetStatus(c.Request.Context(), tx, course.ID, models.CourseStatusUnpublished)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		// Revoke cached signed URLs immediately: a previously issued
		// long-lived (up to 1hr) published-course URL must not remain
		// trustable after unpublish, per spec.
		if err := d.Assets.RevokeSignedURLsForCourse(c.Request.Context(), tx, course.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, courseResponse(updated))
	}
}

// DuplicateCourse creates a deep copy (new chapter/lesson/block IDs,
// shared asset_id references) starting in 'draft' status.
func DuplicateCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		dup, err := d.Courses.Duplicate(c.Request.Context(), tx, course.ID, ac.UserID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, courseResponse(dup))
	}
}

func courseResponse(course *models.Course) gin.H {
	return gin.H{
		"id":              course.ID,
		"org_id":          course.OrgID,
		"title":           course.Title,
		"description":     course.Description,
		"cover_image_url": course.CoverImageURL,
		"category_id":     course.CategoryID,
		"status":          course.Status,
		"publish_date":    course.PublishDate,
		"created_by":      course.CreatedBy,
		"created_at":      course.CreatedAt,
		"updated_at":      course.UpdatedAt,
		"published_at":    course.PublishedAt,
		"archived_at":     course.ArchivedAt,
	}
}
