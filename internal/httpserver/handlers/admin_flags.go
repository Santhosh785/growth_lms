package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// This file implements Task 10's runtime feature-flag surface. Platform owners
// manage the flag catalog (GET/PUT/DELETE /api/admin/feature-flags[/:key]);
// org owners set or clear their org's override of a flag and read the
// effective values (GET/PUT/DELETE /api/orgs/:org_slug/feature-flags[/:key]).
// A flag's effective value for an org is its override if set, else the flag's
// platform default.

// --- Platform-owner catalog management -------------------------------------

// ListFeatureFlags is GET /api/admin/feature-flags (platform owner).
func ListFeatureFlags(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		flags, err := d.FeatureFlags.List(c.Request.Context(), tx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(flags))
		for _, f := range flags {
			out = append(out, gin.H{"key": f.Key, "description": f.Description, "default_enabled": f.DefaultEnabled})
		}
		c.JSON(http.StatusOK, gin.H{"feature_flags": out})
	}
}

type upsertFlagRequest struct {
	Description    string `json:"description"`
	DefaultEnabled bool   `json:"default_enabled"`
}

// UpsertFeatureFlag is PUT /api/admin/feature-flags/:key (platform owner).
// Idempotent create-or-update keyed by the URL :key.
func UpsertFeatureFlag(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.Param("key"))
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "flag key is required"})
			return
		}
		var req upsertFlagRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		if err := d.FeatureFlags.Upsert(c.Request.Context(), tx, models.FeatureFlag{
			Key: key, Description: req.Description, DefaultEnabled: req.DefaultEnabled,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			UserID: &ac.UserID, Action: "feature_flag.upserted", ResourceType: "feature_flag", ResourceID: &key,
			Details:   map[string]any{"default_enabled": req.DefaultEnabled},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"key": key, "description": req.Description, "default_enabled": req.DefaultEnabled})
	}
}

// DeleteFeatureFlag is DELETE /api/admin/feature-flags/:key (platform owner).
// Cascades to any org overrides.
func DeleteFeatureFlag(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Param("key")
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		if err := d.FeatureFlags.Delete(c.Request.Context(), tx, key); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "feature flag not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			UserID: &ac.UserID, Action: "feature_flag.deleted", ResourceType: "feature_flag", ResourceID: &key,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.Status(http.StatusNoContent)
	}
}

// --- Org-owner overrides ----------------------------------------------------

// ListOrgFeatureFlags is GET /api/orgs/:org_slug/feature-flags (owner): every
// flag with its resolved value for this org and whether it's overridden.
func ListOrgFeatureFlags(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		flags, err := d.FeatureFlags.ListEffectiveForOrg(ctx, tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(flags))
		for _, f := range flags {
			out = append(out, gin.H{
				"key": f.Key, "description": f.Description,
				"enabled": f.Enabled, "overridden": f.Overridden,
			})
		}
		c.JSON(http.StatusOK, gin.H{"feature_flags": out})
	}
}

type setOrgFlagRequest struct {
	Enabled bool `json:"enabled"`
}

// SetOrgFeatureFlag is PUT /api/orgs/:org_slug/feature-flags/:key (owner): set
// this org's override for a flag. The flag must exist in the catalog (the FK
// enforces it; a missing flag yields 404).
func SetOrgFeatureFlag(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Param("key")
		var req setOrgFlagRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		// Confirm the flag exists so a typo returns 404 rather than a FK 500.
		if _, err := d.FeatureFlags.Get(ctx, tx, key); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "feature flag not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if err := d.FeatureFlags.SetOrgOverride(ctx, tx, oc.OrgID, key, req.Enabled, ac.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "feature_flag.override_set",
			ResourceType: "feature_flag", ResourceID: &key,
			Details:   map[string]any{"enabled": req.Enabled},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"key": key, "enabled": req.Enabled, "overridden": true})
	}
}

// ClearOrgFeatureFlag is DELETE /api/orgs/:org_slug/feature-flags/:key (owner):
// remove the override so the flag reverts to its platform default.
func ClearOrgFeatureFlag(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Param("key")
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.FeatureFlags.ClearOrgOverride(ctx, tx, oc.OrgID, key); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "feature_flag.override_cleared",
			ResourceType: "feature_flag", ResourceID: &key,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.Status(http.StatusNoContent)
	}
}
