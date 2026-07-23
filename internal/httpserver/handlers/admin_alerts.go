package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// This file implements Task 10's operational-alerting read/resolve surface.
// Alerts themselves are recorded by the application's privileged paths (the
// worker's asynq error handler, the payment-webhook handler, the auth
// middleware) via AlertRepo.Record → record_system_alert(). Here a platform
// owner lists/resolves any alert; an org owner lists/resolves only their org's
// alerts. RLS on system_alerts enforces the same split independent of the
// route gate.

func alertView(a *models.SystemAlert) gin.H {
	return gin.H{
		"id":          a.ID,
		"org_id":      a.OrgID,
		"severity":    a.Severity,
		"category":    a.Category,
		"source":      a.Source,
		"message":     a.Message,
		"details":     a.Details,
		"resolved_at": a.ResolvedAt,
		"resolved_by": a.ResolvedBy,
		"created_at":  a.CreatedAt,
	}
}

// parseAlertFilter reads the shared ?category=&severity=&open=&limit= query
// params. orgID scopes to a single org (empty = no org filter, used by the
// platform-wide listing).
func parseAlertFilter(c *gin.Context, orgID string) models.AlertFilter {
	f := models.AlertFilter{OrgID: orgID, Category: c.Query("category"), Severity: c.Query("severity")}
	if open := c.Query("open"); open == "1" || open == "true" {
		f.OpenOnly = true
	}
	if n, err := strconv.Atoi(c.Query("limit")); err == nil {
		f.Limit = n
	}
	return f
}

// ListAlerts is GET /api/admin/alerts (platform owner): every alert on the
// platform, subject to the query filters. Also reports the open-alert count.
func ListAlerts(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)

		alerts, err := d.Alerts.List(ctx, tx, parseAlertFilter(c, ""))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		open, err := d.Alerts.CountOpen(ctx, tx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(alerts))
		for _, a := range alerts {
			out = append(out, alertView(a))
		}
		c.JSON(http.StatusOK, gin.H{"alerts": out, "open_count": open})
	}
}

// ResolveAlert is POST /api/admin/alerts/:alert_id/resolve (platform owner).
func ResolveAlert(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		id := c.Param("alert_id")
		if err := d.Alerts.Resolve(ctx, tx, id, ac.UserID); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "alert not found or already resolved"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			UserID: &ac.UserID, Action: "alert.resolved", ResourceType: "system_alert", ResourceID: &id,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"id": id, "resolved": true})
	}
}

// ListOrgAlerts is GET /api/orgs/:org_slug/alerts (owner): this org's alerts
// only. RLS additionally restricts the rows to the org, so this is defense in
// depth over the explicit org filter.
func ListOrgAlerts(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		alerts, err := d.Alerts.List(ctx, tx, parseAlertFilter(c, oc.OrgID))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(alerts))
		for _, a := range alerts {
			out = append(out, alertView(a))
		}
		c.JSON(http.StatusOK, gin.H{"alerts": out})
	}
}

// ResolveOrgAlert is POST /api/orgs/:org_slug/alerts/:alert_id/resolve (owner).
// RLS ensures an owner can only resolve their own org's alerts.
func ResolveOrgAlert(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		id := c.Param("alert_id")
		if err := d.Alerts.Resolve(ctx, tx, id, ac.UserID); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "alert not found or already resolved"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "alert.resolved",
			ResourceType: "system_alert", ResourceID: &id,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"id": id, "resolved": true})
	}
}
