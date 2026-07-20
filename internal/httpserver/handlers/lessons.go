package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

type createLessonRequest struct {
	Title string `json:"title" binding:"required,min=1,max=300"`
}

func CreateLesson(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createLessonRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		chapterID := c.Param("chapterId")

		existing, err := d.Lessons.ListByChapter(c.Request.Context(), tx, chapterID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		sortOrders := make([]float64, len(existing))
		for i, lsn := range existing {
			sortOrders[i] = lsn.SortOrder
		}
		next := models.NextSortOrder(sortOrders)

		lesson, err := d.Lessons.Create(c.Request.Context(), tx, chapterID, course.ID, course.OrgID, ac.UserID, req.Title, next)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, lessonResponse(lesson))
	}
}

func ListLessons(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		lessons, err := d.Lessons.ListByChapter(c.Request.Context(), tx, c.Param("chapterId"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(lessons))
		for i, lsn := range lessons {
			out[i] = lessonResponse(lsn)
		}
		c.JSON(http.StatusOK, gin.H{"lessons": out})
	}
}

type updateLessonRequest struct {
	Title     string   `json:"title" binding:"required,min=1,max=300"`
	SortOrder *float64 `json:"sort_order"`
}

func UpdateLesson(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateLessonRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		lesson, err := d.Lessons.Update(c.Request.Context(), tx, c.Param("lessonId"), req.Title, req.SortOrder)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "lesson not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, lessonResponse(lesson))
	}
}

// DeleteLesson rejects with 409 (including the block count) if the
// lesson still has blocks — no cascade, per spec.
func DeleteLesson(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		err := d.Lessons.Delete(c.Request.Context(), tx, c.Param("lessonId"))
		if err != nil {
			var hasChildren models.ErrHasChildren
			if errors.As(err, &hasChildren) {
				c.JSON(http.StatusConflict, gin.H{"error": "lesson still has blocks", "block_count": hasChildren.Count})
				return
			}
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "lesson not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func ReorderLessons(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req reorderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		for _, item := range req.Items {
			if err := d.Lessons.SetSortOrder(c.Request.Context(), tx, item.ID, item.SortOrder); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		c.Status(http.StatusNoContent)
	}
}

func lessonResponse(lsn *models.Lesson) gin.H {
	return gin.H{
		"id":         lsn.ID,
		"chapter_id": lsn.ChapterID,
		"course_id":  lsn.CourseID,
		"title":      lsn.Title,
		"sort_order": lsn.SortOrder,
		"updated_at": lsn.UpdatedAt,
	}
}
