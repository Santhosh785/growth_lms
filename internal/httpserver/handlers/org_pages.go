package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// ListOrgPages is GET /api/orgs/:org_slug/pages — the landing-page
// builder's page list, owner/teacher only (see server.go route wiring).
func ListOrgPages(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		pages, err := d.OrgPages.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"pages": pages})
	}
}

type upsertOrgPageRequest struct {
	Title       string `json:"title" binding:"required"`
	ContentHTML string `json:"content_html"`
	IsPublished bool   `json:"is_published"`
}

// UpsertOrgPage is PUT /api/orgs/:org_slug/pages/:slug — creates or
// updates one page (slug "home" is the org's landing page by
// convention). Owner/teacher only.
func UpsertOrgPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req upsertOrgPageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		slug := c.Param("slug")

		page, err := d.OrgPages.Upsert(ctx, tx, oc.OrgID, slug, req.Title, req.ContentHTML, req.IsPublished, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"page": page})
	}
}

// DeleteOrgPage is DELETE /api/orgs/:org_slug/pages/:slug. Owner/teacher only.
func DeleteOrgPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		slug := c.Param("slug")

		if err := d.OrgPages.Delete(c.Request.Context(), tx, oc.OrgID, slug); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "page not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
