package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/auth"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// ListMembers returns every member of the resolved organization. Any
// member may list the roster (ResolveOrg already gates on membership).
func ListMembers(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		members, err := d.Memberships.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]gin.H, 0, len(members))
		for _, m := range members {
			out = append(out, gin.H{
				"user_id":   m.UserID,
				"email":     m.Email,
				"full_name": m.FullName,
				"role":      m.Role,
				"joined_at": m.JoinedAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"members": out})
	}
}

type changeMemberRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// ChangeMemberRole updates a member's role. Requires RequireRole(owner).
func ChangeMemberRole(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req changeMemberRoleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		switch req.Role {
		case auth.RoleOwner, auth.RoleTeacher, auth.RoleModerator, auth.RoleLearner:
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			return
		}

		targetUserID := c.Param("user_id")
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Memberships.UpdateRole(c.Request.Context(), tx, targetUserID, oc.OrgID, req.Role); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "member.role_changed", ResourceType: "membership", ResourceID: &targetUserID,
			Details:   map[string]any{"new_role": req.Role},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.Status(http.StatusNoContent)
	}
}

// RemoveMember removes a member from the organization. Requires
// RequireRole(owner).
func RemoveMember(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		targetUserID := c.Param("user_id")
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Memberships.Remove(c.Request.Context(), tx, targetUserID, oc.OrgID); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "member.removed", ResourceType: "membership", ResourceID: &targetUserID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.Status(http.StatusNoContent)
	}
}
