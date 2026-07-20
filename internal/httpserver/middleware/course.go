package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/models"
)

const courseContextKey = "course_context"

// CourseFromGin returns the *models.Course loaded by ResolveCourseOrg for
// this request's :courseId, and whether one was present.
func CourseFromGin(c *gin.Context) (*models.Course, bool) {
	v, ok := c.Get(courseContextKey)
	if !ok {
		return nil, false
	}
	course, ok := v.(*models.Course)
	return course, ok
}

// ResolveCourseOrg is course-domain routes' equivalent of ResolveOrg: it
// reads :courseId, loads the course, resolves the caller's role in the
// course's org, and stamps RLS session context — all without the
// request's URL needing an :org_slug segment (course-domain endpoints are
// flat, per the spec's documented paths).
//
// This works before org context is known because courses' RLS SELECT
// policy (db/migrations/000003_course_domain.up.sql) is built on
// is_org_member(courses.org_id), which only needs app_current_user_id()
// — already set by dbctx.Begin at request start — not
// app.current_org_id. This is the same bootstrap-safe pattern
// organizations' own RLS policy already relies on.
//
// Must run after Authenticate + WithRequestTx. Stores the SAME
// middleware.OrgContext type ResolveOrg uses (via the same accessor,
// OrgContextFromGin), so RequireRole and any other downstream code work
// unmodified regardless of which middleware resolved org context.
func ResolveCourseOrg(courses *models.CourseRepo, memberships *models.MembershipRepo, profiles *models.ProfileRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, ok := RequestTxFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		ac, ok := AuthContextFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		courseID := c.Param("courseId")
		ctx := c.Request.Context()

		course, err := courses.Get(ctx, tx, courseID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "course not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		role, err := memberships.GetRole(ctx, tx, ac.UserID, course.OrgID)
		isPlatformOwner := false
		if err != nil {
			if !errors.Is(err, models.ErrNotFound) {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			profile, perr := profiles.GetByID(ctx, tx, ac.UserID)
			if perr != nil || !profile.IsPlatformOwner {
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "course not found"})
				return
			}
			isPlatformOwner = true
			role = ""
		}

		if err := dbctx.SetOrgContext(ctx, tx, course.OrgID, role); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.Set(orgContextKey, OrgContext{OrgID: course.OrgID, Role: role, IsPlatformOwner: isPlatformOwner})
		c.Set(courseContextKey, course)
		c.Next()
	}
}
