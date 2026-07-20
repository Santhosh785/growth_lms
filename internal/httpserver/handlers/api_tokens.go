package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

type createAPITokenRequest struct {
	Name string `json:"name" binding:"required,min=1,max=200"`
}

// CreateAPIToken issues a new API token for the resolved organization.
// The plaintext secret is returned exactly once and never stored or
// retrievable again — only its bcrypt hash and a display prefix persist.
// Requires RequireRole(owner).
func CreateAPIToken(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createAPITokenRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		token, secret, err := d.APITokens.Create(c.Request.Context(), tx, oc.OrgID, req.Name, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "apitoken.created", ResourceType: "api_token", ResourceID: &token.ID,
			Details:   map[string]any{"name": req.Name},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})

		c.JSON(http.StatusCreated, gin.H{
			"id": token.ID, "name": token.Name, "created_at": token.CreatedAt,
			"secret": token.TokenPrefix + "." + secret,
		})
	}
}

// ListAPITokens lists API tokens for the resolved organization (never
// including secrets). Requires RequireRole(owner).
func ListAPITokens(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		tokens, err := d.APITokens.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]gin.H, 0, len(tokens))
		for _, t := range tokens {
			out = append(out, gin.H{
				"id": t.ID, "name": t.Name, "prefix": t.TokenPrefix,
				"created_at": t.CreatedAt, "revoked_at": t.RevokedAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"api_tokens": out})
	}
}

// RevokeAPIToken revokes an API token. Requires RequireRole(owner).
func RevokeAPIToken(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenID := c.Param("token_id")
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.APITokens.Revoke(c.Request.Context(), tx, tokenID); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "api token not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "apitoken.revoked", ResourceType: "api_token", ResourceID: &tokenID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.Status(http.StatusNoContent)
	}
}
