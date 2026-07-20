package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/auth"
	"growth-lms/internal/models"
)

// RequireEntitlement gates learner-facing routes (player, resume,
// progress) that need the opposite check from RequireRole: any *enrolled*
// org member, regardless of role, rather than owner/teacher only.
//
// Must run after ResolveCourseOrg (needs both OrgContext and course
// context) and after WithRequestTx. Owners/teachers (and platform owners
// impersonating for support) always pass — they can access their own
// org's courses regardless of enrollment, matching the authoring routes'
// precedent. Every other caller must hold an `active`
// learner_course_access row for (learner_id=caller, course_id), AND the
// course must be `published` — a learner with a stale/leftover access
// row to a course that got unpublished must not see draft content (see
// grilling-record.md Q5).
func RequireEntitlement(access *models.LearnerCourseAccessRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := OrgContextFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if oc.IsPlatformOwner || oc.Role == auth.RoleOwner || oc.Role == auth.RoleTeacher {
			c.Next()
			return
		}

		course, ok := CourseFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if course.Status != models.CourseStatusPublished {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		ac, ok := AuthContextFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		tx, ok := RequestTxFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		grant, err := access.Get(c.Request.Context(), tx, ac.UserID, course.ID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if grant.AccessStatus != models.AccessStatusActive {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		c.Next()
	}
}
