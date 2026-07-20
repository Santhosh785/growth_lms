package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

func ListCourseVersions(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		versions, err := d.CourseVersions.List(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(versions))
		for i, v := range versions {
			out[i] = gin.H{
				"id":             v.ID,
				"version_number": v.VersionNumber,
				"created_by":     v.CreatedBy,
				"created_at":     v.CreatedAt,
				"chapter_count":  len(v.Snapshot.Chapters),
			}
		}
		c.JSON(http.StatusOK, gin.H{"versions": out})
	}
}

func GetCourseVersion(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		version, err := d.CourseVersions.Get(c.Request.Context(), tx, c.Param("versionId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"id":             version.ID,
			"version_number": version.VersionNumber,
			"created_by":     version.CreatedBy,
			"created_at":     version.CreatedAt,
			"snapshot":       version.Snapshot,
		})
	}
}

// RestoreCourseVersion restores the course's live content from a prior
// version's snapshot, creating a NEW version (undo via new version — the
// version being restored from is never overwritten or deleted).
func RestoreCourseVersion(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		restored, err := d.CourseVersions.Restore(c.Request.Context(), tx, course.ID, c.Param("versionId"), ac.UserID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"id":             restored.ID,
			"version_number": restored.VersionNumber,
			"created_by":     restored.CreatedBy,
			"created_at":     restored.CreatedAt,
		})
	}
}
