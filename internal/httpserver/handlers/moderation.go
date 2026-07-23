package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/auth"
	"growth-lms/internal/httpserver/middleware"
)

// Task 7 moderation. Pin/lock and hide are moderator/owner actions (also
// gated by RequireRole at the route and by is_org_moderator in RLS). Editing
// and deleting a post is allowed to its author, with moderators/owners as an
// override — mirrored by the discussion_posts author-or-moderator RLS policy.

func isModeratorOrOwner(oc middleware.OrgContext) bool {
	return oc.IsPlatformOwner || oc.Role == auth.RoleOwner || oc.Role == auth.RoleModerator
}

type pinRequest struct {
	Pinned bool `json:"pinned"`
}

// SetThreadPinned pins/unpins a thread (moderator/owner).
func SetThreadPinned(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req pinRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		thread, _ := middleware.ThreadFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		updated, err := d.Threads.SetPinned(c.Request.Context(), tx, thread.ID, req.Pinned)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, threadResponse(updated))
	}
}

type lockRequest struct {
	Locked bool `json:"locked"`
}

// SetThreadLocked locks/unlocks a thread (moderator/owner).
func SetThreadLocked(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req lockRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		thread, _ := middleware.ThreadFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		updated, err := d.Threads.SetLocked(c.Request.Context(), tx, thread.ID, req.Locked)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, threadResponse(updated))
	}
}

// HidePost soft-hides a post (moderator/owner). Route is RequireRole-gated.
func HidePost(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		post, _ := middleware.PostFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		if _, err := d.Posts.UpdateStatus(c.Request.Context(), tx, post.ID, "hidden"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "hidden"})
	}
}

type editPostRequest struct {
	Body string `json:"body" binding:"required"`
}

// EditPost edits a post's body. Allowed to the author; moderators/owners may
// also edit (RLS enforces the same author-or-moderator boundary).
func EditPost(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req editPostRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		post, _ := middleware.PostFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if post.AuthorID != ac.UserID && !isModeratorOrOwner(oc) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		updated, err := d.Posts.UpdateBody(c.Request.Context(), tx, post.ID, req.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, postResponse(updated, nil))
	}
}

// DeletePost soft-deletes a post. Allowed to the author or a moderator/owner.
func DeletePost(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		post, _ := middleware.PostFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if post.AuthorID != ac.UserID && !isModeratorOrOwner(oc) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		if _, err := d.Posts.UpdateStatus(c.Request.Context(), tx, post.ID, "deleted"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

// ListReports returns the org's open moderation queue (moderator/owner).
func ListReports(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		reports, err := d.Reports.ListOpenByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(reports))
		for i, r := range reports {
			out[i] = gin.H{
				"id": r.ID, "post_id": r.PostID, "reporter_id": r.ReporterID,
				"reason": r.Reason, "status": r.Status, "created_at": r.CreatedAt,
			}
		}
		c.JSON(http.StatusOK, gin.H{"reports": out})
	}
}

// ResolveReport marks a report resolved (moderator/owner).
func ResolveReport(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		if _, err := d.Reports.Resolve(c.Request.Context(), tx, c.Param("reportId"), ac.UserID); err != nil {
			if errNotFoundResponse(c, err, "report not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "resolved"})
	}
}

// DismissReport marks a report dismissed (moderator/owner).
func DismissReport(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		if _, err := d.Reports.Dismiss(c.Request.Context(), tx, c.Param("reportId"), ac.UserID); err != nil {
			if errNotFoundResponse(c, err, "report not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "dismissed"})
	}
}
