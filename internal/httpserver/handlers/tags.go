package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
)

type addTagRequest struct {
	Name string `json:"name" binding:"required,min=1,max=100"`
}

// AddTagToCourse is freeform get-or-create: tagging a course with a name
// that doesn't exist yet in this org creates the tag row, available to
// teacher/owner (unlike categories, which are owner-only).
func AddTagToCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req addTagRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		slug := slugify(req.Name)

		tag, err := d.Tags.GetOrCreate(c.Request.Context(), tx, course.OrgID, req.Name, slug)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if err := d.Tags.AttachToCourse(c.Request.Context(), tx, course.OrgID, course.ID, tag.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": tag.ID, "name": tag.Name, "slug": tag.Slug})
	}
}

func RemoveTagFromCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Tags.DetachFromCourse(c.Request.Context(), tx, course.ID, c.Param("tagId")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func ListCourseTags(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		tags, err := d.Tags.ListForCourse(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(tags))
		for i, t := range tags {
			out[i] = gin.H{"id": t.ID, "name": t.Name, "slug": t.Slug}
		}
		c.JSON(http.StatusOK, gin.H{"tags": out})
	}
}
