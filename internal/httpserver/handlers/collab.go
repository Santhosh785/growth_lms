package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Task 7 collaborative boards (course-scoped whiteboards). CRUD lives here;
// the live editing/presence transport is the in-process realtime hub wired in
// server.go (see BoardSocket). Board state is a JSON snapshot the hub
// debounce-persists.

type createBoardRequest struct {
	Title string `json:"title" binding:"required"`
	// TemplateID optionally seeds the new board's snapshot from an org board
	// template (Task 9 "improved collaborative boards"). Empty = a blank board.
	TemplateID string `json:"template_id"`
}

// CreateBoard creates a board under a course, optionally seeded from an org
// board template.
func CreateBoard(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createBoardRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		// Resolve an optional template first so a bad template_id fails before a
		// board is created. The template must belong to the same org (RLS also
		// enforces this).
		var seed json.RawMessage
		if req.TemplateID != "" {
			tmpl, err := d.BoardTemplates.Get(ctx, tx, req.TemplateID)
			if err != nil || tmpl.OrgID != course.OrgID {
				c.JSON(http.StatusBadRequest, gin.H{"error": "template not found"})
				return
			}
			seed = tmpl.Snapshot
		}

		board, err := d.Boards.Create(ctx, tx, course.OrgID, course.ID, req.Title, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		// Seed the snapshot from the template, if any. A failure here leaves a
		// usable blank board, so surface it but don't fail the create.
		if len(seed) > 0 {
			if err := d.Boards.SaveSnapshot(ctx, tx, board.ID, seed, ac.UserID); err == nil {
				board.Snapshot = seed
			}
		}
		c.JSON(http.StatusCreated, boardResponse(board))
	}
}

// ListBoards returns a course's boards.
func ListBoards(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		boards, err := d.Boards.ListByCourse(c.Request.Context(), tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(boards))
		for i, b := range boards {
			out[i] = boardResponse(b)
		}
		c.JSON(http.StatusOK, gin.H{"boards": out})
	}
}

// GetBoard returns a board with its current snapshot.
func GetBoard(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		board, _ := middleware.BoardFromGin(c)
		c.JSON(http.StatusOK, boardResponseWithSnapshot(board))
	}
}

// DeleteBoard deletes a board (creator or moderator/owner; also RLS-enforced).
func DeleteBoard(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		board, _ := middleware.BoardFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if board.CreatedBy != ac.UserID && !isModeratorOrOwner(oc) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Boards.Delete(c.Request.Context(), tx, board.ID); err != nil {
			if errNotFoundResponse(c, err, "board not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

func boardResponse(b *models.CollabBoard) gin.H {
	return gin.H{
		"id": b.ID, "course_id": b.CourseID, "title": b.Title,
		"created_by": b.CreatedBy, "updated_at": b.UpdatedAt,
	}
}

func boardResponseWithSnapshot(b *models.CollabBoard) gin.H {
	r := boardResponse(b)
	r["snapshot"] = b.Snapshot
	return r
}
