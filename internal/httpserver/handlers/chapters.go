package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

type createChapterRequest struct {
	Title string `json:"title" binding:"required,min=1,max=300"`
}

// CreateChapter auto-assigns the next sort_order after the course's
// existing chapters.
func CreateChapter(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createChapterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		existing, err := d.Chapters.ListByCourse(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		sortOrders := make([]float64, len(existing))
		for i, ch := range existing {
			sortOrders[i] = ch.SortOrder
		}
		next := models.NextSortOrder(sortOrders)

		chapter, err := d.Chapters.Create(c.Request.Context(), tx, course.ID, course.OrgID, ac.UserID, req.Title, next)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, chapterResponse(chapter))
	}
}

func ListChapters(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		chapters, err := d.Chapters.ListByCourse(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(chapters))
		for i, ch := range chapters {
			out[i] = chapterResponse(ch)
		}
		c.JSON(http.StatusOK, gin.H{"chapters": out})
	}
}

type updateChapterRequest struct {
	Title     string   `json:"title" binding:"required,min=1,max=300"`
	SortOrder *float64 `json:"sort_order"`
}

func UpdateChapter(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateChapterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		chapter, err := d.Chapters.Update(c.Request.Context(), tx, c.Param("chapterId"), req.Title, req.SortOrder)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "chapter not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, chapterResponse(chapter))
	}
}

// DeleteChapter rejects with 409 (including the lesson count) if the
// chapter still has lessons — no cascade, per spec.
func DeleteChapter(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		err := d.Chapters.Delete(c.Request.Context(), tx, c.Param("chapterId"))
		if err != nil {
			var hasChildren models.ErrHasChildren
			if errors.As(err, &hasChildren) {
				c.JSON(http.StatusConflict, gin.H{"error": "chapter still has lessons", "lesson_count": hasChildren.Count})
				return
			}
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "chapter not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type reorderRequest struct {
	Items []struct {
		ID        string  `json:"id" binding:"required"`
		SortOrder float64 `json:"sort_order"`
	} `json:"items" binding:"required,min=1"`
}

// ReorderChapters updates sort_order for multiple chapters in one
// transaction (the request's transaction is already scoped per-request;
// each Exec below runs within it).
func ReorderChapters(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req reorderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		for _, item := range req.Items {
			if err := d.Chapters.SetSortOrder(c.Request.Context(), tx, item.ID, item.SortOrder); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		c.Status(http.StatusNoContent)
	}
}

func chapterResponse(ch *models.Chapter) gin.H {
	return gin.H{
		"id":         ch.ID,
		"course_id":  ch.CourseID,
		"title":      ch.Title,
		"sort_order": ch.SortOrder,
		"updated_at": ch.UpdatedAt,
	}
}
