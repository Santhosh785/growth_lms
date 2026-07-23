package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Task 9 "improved collaborative boards": two additive enhancements over the
// Task 7 board CRUD in collab.go —
//
//   - board versions: a member saves a named checkpoint of a board's current
//     state and can restore the board to any checkpoint (recover from a bad
//     edit, keep a lightweight revision trail);
//   - board templates: a teacher authors an org-level reusable starting
//     snapshot a new board can be seeded from (see CreateBoard's template_id).
//
// Both are tenant-scoped by RLS; version routes resolve org from the board row
// (ResolveBoardOrg), template routes from :org_slug (ResolveOrg). Neither is
// feature-flagged — collaborative boards are an already-shipped, always-on
// Task 7 capability, so these extend it directly rather than gating behind a
// new module flag.

// --- Board versions (any member) -------------------------------------------

type saveBoardVersionRequest struct {
	Label string `json:"label"`
}

// SaveBoardVersion is POST /api/boards/:boardId/versions: snapshot the board's
// current state as a labeled checkpoint. The snapshot is taken from the board
// row (server-side), never trusted from the client, so a checkpoint always
// reflects real persisted board state.
func SaveBoardVersion(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req saveBoardVersionRequest
		// Label is optional; a malformed body is still a 400.
		if err := c.ShouldBindJSON(&req); err != nil && c.Request.ContentLength > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		board, _ := middleware.BoardFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		version, err := d.BoardVersions.Create(c.Request.Context(), tx, oc.OrgID, board.ID, req.Label, board.Snapshot, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, boardVersionResponse(version, false))
	}
}

// ListBoardVersions is GET /api/boards/:boardId/versions: the board's checkpoint
// history (metadata only, newest first).
func ListBoardVersions(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		board, _ := middleware.BoardFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		versions, err := d.BoardVersions.ListByBoard(c.Request.Context(), tx, board.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(versions))
		for i, v := range versions {
			out[i] = boardVersionResponse(v, false)
		}
		c.JSON(http.StatusOK, gin.H{"versions": out})
	}
}

// RestoreBoardVersion is POST /api/boards/:boardId/versions/:versionId/restore:
// copy a checkpoint's snapshot back onto the board. Any org member who can edit
// the board may restore it (RLS enforces both the version read and the board
// update). Restoring does not consume the checkpoint — it remains for future
// restores.
func RestoreBoardVersion(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		board, _ := middleware.BoardFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		version, ok := d.loadBoardVersion(c, board.ID, oc.OrgID)
		if !ok {
			return
		}
		if err := d.Boards.SaveSnapshot(c.Request.Context(), tx, board.ID, version.Snapshot, ac.UserID); err != nil {
			if errNotFoundResponse(c, err, "board not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "restored", "version_id": version.ID, "snapshot": version.Snapshot})
	}
}

// DeleteBoardVersion is DELETE /api/boards/:boardId/versions/:versionId: prune a
// checkpoint. Its creator or a moderator/owner may delete it (also RLS-enforced).
func DeleteBoardVersion(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		board, _ := middleware.BoardFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		version, ok := d.loadBoardVersion(c, board.ID, oc.OrgID)
		if !ok {
			return
		}
		if !ownsOrModerates(version.CreatedBy, ac.UserID, oc) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if err := d.BoardVersions.Delete(c.Request.Context(), tx, version.ID); err != nil {
			if errNotFoundResponse(c, err, "version not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

// loadBoardVersion loads the :versionId and verifies it belongs to this board
// (and org), 404ing otherwise so an id from another board is indistinguishable
// from a missing one (defense in depth over RLS).
func (d *AuthDeps) loadBoardVersion(c *gin.Context, boardID, orgID string) (*models.CollabBoardVersion, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	version, err := d.BoardVersions.Get(c.Request.Context(), tx, c.Param("versionId"))
	if err != nil {
		if errNotFoundResponse(c, err, "version not found") {
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if version.BoardID != boardID || version.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
		return nil, false
	}
	return version, true
}

// ownsOrModerates reports whether userID created the row or is a moderator/owner
// of the org.
func ownsOrModerates(createdBy *string, userID string, oc middleware.OrgContext) bool {
	if createdBy != nil && *createdBy == userID {
		return true
	}
	return isModeratorOrOwner(oc)
}

func boardVersionResponse(v *models.CollabBoardVersion, withSnapshot bool) gin.H {
	r := gin.H{
		"id": v.ID, "board_id": v.BoardID, "label": v.Label,
		"created_by": v.CreatedBy, "created_at": v.CreatedAt,
	}
	if withSnapshot {
		r["snapshot"] = v.Snapshot
	}
	return r
}

// --- Board templates -------------------------------------------------------

type boardTemplateRequest struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Snapshot    json.RawMessage `json:"snapshot"`
}

// CreateBoardTemplate is POST /api/orgs/:org_slug/board-templates (owner/teacher).
func CreateBoardTemplate(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req boardTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		tmpl, err := d.BoardTemplates.Create(c.Request.Context(), tx, oc.OrgID, req.Title, req.Description, req.Snapshot, ac.UserID)
		if err != nil {
			if models.IsUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "a template with that title already exists"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, boardTemplateResponse(tmpl, true))
	}
}

// ListBoardTemplates is GET /api/orgs/:org_slug/board-templates (any member).
func ListBoardTemplates(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		tmpls, err := d.BoardTemplates.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(tmpls))
		for i, t := range tmpls {
			// List omits snapshots to stay light; GET returns the full body.
			out[i] = boardTemplateResponse(t, false)
		}
		c.JSON(http.StatusOK, gin.H{"templates": out})
	}
}

// GetBoardTemplate is GET /api/orgs/:org_slug/board-templates/:templateId.
func GetBoardTemplate(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tmpl, ok := d.getOrgBoardTemplate(c, oc.OrgID)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, boardTemplateResponse(tmpl, true))
	}
}

// UpdateBoardTemplate is PATCH /api/orgs/:org_slug/board-templates/:templateId (owner/teacher).
func UpdateBoardTemplate(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req boardTemplateRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		tmpl, ok := d.getOrgBoardTemplate(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		updated, err := d.BoardTemplates.Update(c.Request.Context(), tx, tmpl.ID, req.Title, req.Description, req.Snapshot)
		if err != nil {
			if models.IsUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "a template with that title already exists"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, boardTemplateResponse(updated, true))
	}
}

// DeleteBoardTemplate is DELETE /api/orgs/:org_slug/board-templates/:templateId (owner/teacher).
func DeleteBoardTemplate(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tmpl, ok := d.getOrgBoardTemplate(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.BoardTemplates.Delete(c.Request.Context(), tx, tmpl.ID); err != nil {
			if errNotFoundResponse(c, err, "template not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

// getOrgBoardTemplate loads the :templateId and verifies it belongs to the
// request's org, 404ing otherwise.
func (d *AuthDeps) getOrgBoardTemplate(c *gin.Context, orgID string) (*models.CollabBoardTemplate, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	tmpl, err := d.BoardTemplates.Get(c.Request.Context(), tx, c.Param("templateId"))
	if err != nil {
		if errNotFoundResponse(c, err, "template not found") {
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if tmpl.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return nil, false
	}
	return tmpl, true
}

func boardTemplateResponse(t *models.CollabBoardTemplate, withSnapshot bool) gin.H {
	r := gin.H{
		"id": t.ID, "title": t.Title, "description": t.Description,
		"created_by": t.CreatedBy, "created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
	}
	if withSnapshot {
		r["snapshot"] = t.Snapshot
	}
	return r
}
