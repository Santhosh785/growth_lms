package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type createOrgRequest struct {
	Name string `json:"name" binding:"required,min=2,max=200"`
	Slug string `json:"slug" binding:"required,min=2,max=60"`
}

// CreateOrg lets any authenticated user self-service create an
// organization and become its owner (create_organization() SQL function
// does both atomically). Requires Authenticate + WithRequestTx, but not
// ResolveOrg/RequireRole — there is no existing org to check a role
// against yet.
func CreateOrg(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createOrgRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		slug := strings.ToLower(strings.TrimSpace(req.Slug))
		if !slugPattern.MatchString(slug) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "slug must be lowercase letters, numbers, and hyphens"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		org, err := d.Orgs.Create(c.Request.Context(), tx, req.Name, slug)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "an organization with that slug already exists"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &org.ID, UserID: &ac.UserID, Action: "org.created", ResourceType: "organization", ResourceID: &org.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusCreated, orgResponse(org))
	}
}

// GetOrg returns the resolved organization. Requires ResolveOrg (any
// member may view their own org).
func GetOrg(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		org, err := d.Orgs.GetBySlug(c.Request.Context(), tx, oc.Slug)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
			return
		}
		c.JSON(http.StatusOK, orgResponse(org))
	}
}

type updateOrgRequest struct {
	Name string `json:"name" binding:"required,min=2,max=200"`
}

// UpdateOrg renames an organization. Requires RequireRole(owner) on top
// of ResolveOrg.
func UpdateOrg(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateOrgRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		org, err := d.Orgs.Update(c.Request.Context(), tx, oc.OrgID, req.Name)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "org.updated", ResourceType: "organization", ResourceID: &oc.OrgID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, orgResponse(org))
	}
}

// DeleteOrg deletes an organization (cascading to memberships, courses,
// etc. via FK). Requires RequireRole(owner).
func DeleteOrg(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		// Audit before deleting: audit_events.org_id references
		// organizations(id) ON DELETE SET NULL, so recording first (in the
		// same transaction) still ties the event to the org id in the
		// response even though the FK will later null it out on unrelated
		// historical rows.
		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "org.deleted", ResourceType: "organization", ResourceID: &oc.OrgID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})

		if err := d.Orgs.Delete(c.Request.Context(), tx, oc.OrgID); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func orgResponse(o *models.Organization) gin.H {
	return gin.H{
		"id":         o.ID,
		"slug":       o.Slug,
		"name":       o.Name,
		"created_at": o.CreatedAt,
	}
}
