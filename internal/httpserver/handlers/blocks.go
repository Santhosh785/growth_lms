package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/sanitize"
)

// jsonUnmarshalBlockContent is a small helper so callers (e.g. the
// publish handler's video-readiness check) can decode a block's typed
// content without duplicating error handling everywhere.
func jsonUnmarshalBlockContent(b *models.Block, target any) error {
	return json.Unmarshal(b.Content, target)
}

type createBlockRequest struct {
	Type    string          `json:"type" binding:"required"`
	Content json.RawMessage `json:"content" binding:"required"`
}

var validBlockTypes = map[string]bool{
	models.BlockTypeText:  true,
	models.BlockTypeImage: true,
	models.BlockTypeVideo: true,
	models.BlockTypeFile:  true,
	models.BlockTypeQuiz:  true,
}

// sanitizeBlockContent re-marshals content with any "text" block's HTML
// run through the bluemonday allowlist. Called on every create/update, no
// exceptions.
func sanitizeBlockContent(blockType string, content json.RawMessage) (json.RawMessage, error) {
	if blockType != models.BlockTypeText {
		return content, nil
	}
	var text models.TextBlockContent
	if err := json.Unmarshal(content, &text); err != nil {
		return nil, err
	}
	text.HTML = sanitize.TextBlockHTML(text.HTML)
	return json.Marshal(text)
}

func CreateBlock(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createBlockRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if !validBlockTypes[req.Type] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block type"})
			return
		}

		content, err := sanitizeBlockContent(req.Type, req.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block content"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		lessonID := c.Param("lessonId")

		existing, err := d.Blocks.ListByLesson(c.Request.Context(), tx, lessonID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		sortOrders := make([]float64, len(existing))
		for i, b := range existing {
			sortOrders[i] = b.SortOrder
		}
		next := models.NextSortOrder(sortOrders)

		block, err := d.Blocks.Create(c.Request.Context(), tx, lessonID, course.ID, course.OrgID, ac.UserID, req.Type, content, next)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, blockResponse(block))
	}
}

func ListBlocks(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		blocks, err := d.Blocks.ListByLesson(c.Request.Context(), tx, c.Param("lessonId"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(blocks))
		for i, b := range blocks {
			out[i] = blockResponse(b)
		}
		c.JSON(http.StatusOK, gin.H{"blocks": out})
	}
}

type updateBlockRequest struct {
	Content   json.RawMessage `json:"content" binding:"required"`
	SortOrder *float64        `json:"sort_order"`
}

func UpdateBlock(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateBlockRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		existing, err := d.Blocks.Get(c.Request.Context(), tx, c.Param("blockId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		content, err := sanitizeBlockContent(existing.Type, req.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block content"})
			return
		}

		block, err := d.Blocks.Update(c.Request.Context(), tx, existing.ID, content, req.SortOrder)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, blockResponse(block))
	}
}

type autosaveBlockRequest struct {
	Content json.RawMessage `json:"content" binding:"required"`
}

// AutosaveBlock saves block content without changing course status —
// only updated_at changes, never published_at.
func AutosaveBlock(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req autosaveBlockRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		existing, err := d.Blocks.Get(c.Request.Context(), tx, c.Param("blockId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		content, err := sanitizeBlockContent(existing.Type, req.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block content"})
			return
		}

		block, err := d.Blocks.Autosave(c.Request.Context(), tx, existing.ID, content)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, blockResponse(block))
	}
}

func DeleteBlock(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Blocks.Delete(c.Request.Context(), tx, c.Param("blockId")); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func ReorderBlocks(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req reorderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		for _, item := range req.Items {
			if err := d.Blocks.SetSortOrder(c.Request.Context(), tx, item.ID, item.SortOrder); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		c.Status(http.StatusNoContent)
	}
}

func blockResponse(b *models.Block) gin.H {
	return gin.H{
		"id":         b.ID,
		"lesson_id":  b.LessonID,
		"type":       b.Type,
		"content":    b.Content,
		"sort_order": b.SortOrder,
		"updated_at": b.UpdatedAt,
	}
}
