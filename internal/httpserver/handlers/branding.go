package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// GetBranding returns GET /api/orgs/:org_slug/branding: the org's
// logo/favicon/theme/SEO settings, for the owner-only settings page.
func GetBranding(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		b, err := d.Orgs.GetBranding(c.Request.Context(), tx, oc.Slug)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"branding": b})
	}
}

type updateBrandingRequest struct {
	LogoURL         *string         `json:"logo_url"`
	FaviconURL      *string         `json:"favicon_url"`
	ThemeJSON       json.RawMessage `json:"theme_json"`
	MetaDescription string          `json:"meta_description"`
	OGImageURL      *string         `json:"og_image_url"`
}

// UpdateBranding is PATCH /api/orgs/:org_slug/branding, owner-only —
// mirrors UpdateOrg's shape (see orgs.go).
func UpdateBranding(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateBrandingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if len(req.ThemeJSON) > 0 && !json.Valid(req.ThemeJSON) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "theme_json must be valid JSON"})
			return
		}

		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Orgs.UpdateBranding(ctx, tx, oc.OrgID, req.LogoURL, req.FaviconURL, req.ThemeJSON, req.MetaDescription, req.OGImageURL); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "org.branding_updated", ResourceType: "organization", ResourceID: &oc.OrgID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})

		b, err := d.Orgs.GetBranding(ctx, tx, oc.Slug)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"branding": b})
	}
}
