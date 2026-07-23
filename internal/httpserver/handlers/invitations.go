package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/auth"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/quota"
)

const invitationTTL = 7 * 24 * time.Hour

func generateInvitationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("handlers: generate invitation token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type createInvitationRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required"`
}

// CreateInvitation invites an email address to join the resolved
// organization with a given role. Requires RequireRole(owner).
//
// NOTE: this does not yet send the invitation email via Resend — it
// returns the invite link in the response so the flow is usable and
// testable end-to-end. Wiring actual email delivery is a small follow-up,
// not part of the tenant-isolation/permission work this task is
// concerned with.
func CreateInvitation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createInvitationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		switch req.Role {
		case auth.RoleTeacher, auth.RoleModerator, auth.RoleLearner:
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			return
		}

		token, err := generateInvitationToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		// Task 10 plan limits: refuse to invite past the org's member cap.
		// Enforced at invite time (the practical growth point) rather than at
		// acceptance, so an owner gets immediate feedback; the cap counts
		// current memberships, so pending invites can slightly overshoot — an
		// acceptable soft-cap tradeoff (see enforceQuota's doc comment).
		if !d.enforceQuota(c, oc.OrgID, quota.DimMembers) {
			return
		}

		email := strings.ToLower(strings.TrimSpace(req.Email))
		inv, err := d.Invitations.Create(c.Request.Context(), tx, oc.OrgID, email, req.Role, ac.UserID, token, time.Now().Add(invitationTTL))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "invitation.created", ResourceType: "invitation", ResourceID: &inv.ID,
			Details:   map[string]any{"email": email, "role": req.Role},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})

		c.JSON(http.StatusCreated, gin.H{
			"id":         inv.ID,
			"email":      inv.Email,
			"role":       inv.Role,
			"expires_at": inv.ExpiresAt,
			"invite_url": d.Config.BaseURL + "/invitations/" + inv.Token,
		})
	}
}

// ListInvitations returns pending invitations for the resolved
// organization. Requires RequireRole(owner).
func ListInvitations(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		invs, err := d.Invitations.ListPendingByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]gin.H, 0, len(invs))
		for _, inv := range invs {
			out = append(out, gin.H{
				"id": inv.ID, "email": inv.Email, "role": inv.Role,
				"status": inv.Status, "expires_at": inv.ExpiresAt, "created_at": inv.CreatedAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"invitations": out})
	}
}

// RevokeInvitation deletes a pending invitation. Requires
// RequireRole(owner).
func RevokeInvitation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		invitationID := c.Param("invitation_id")
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Invitations.Revoke(c.Request.Context(), tx, invitationID); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "invitation.revoked", ResourceType: "invitation", ResourceID: &invitationID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.Status(http.StatusNoContent)
	}
}

// AcceptInvitation accepts an invitation by token on behalf of the
// authenticated caller (matched by email inside accept_invitation()).
// Requires Authenticate + WithRequestTx, but no org context — the caller
// isn't a member of the target org yet.
func AcceptInvitation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Param("token")
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		membership, err := d.Invitations.Accept(c.Request.Context(), tx, token)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invitation not found, expired, or already resolved"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &membership.OrgID, UserID: &ac.UserID, Action: "invitation.accepted", ResourceType: "membership", ResourceID: &membership.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"org_id": membership.OrgID, "role": membership.Role})
	}
}

// DeclineInvitation declines an invitation by token on behalf of the
// authenticated caller.
func DeclineInvitation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Param("token")
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		inv, err := d.Invitations.Decline(c.Request.Context(), tx, token)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invitation not found, expired, or already resolved"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &inv.OrgID, UserID: &ac.UserID, Action: "invitation.declined", ResourceType: "invitation", ResourceID: &inv.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.Status(http.StatusNoContent)
	}
}
