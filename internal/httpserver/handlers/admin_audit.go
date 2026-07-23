package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Task 10's audit-log viewer. The audit trail is written throughout the app
// (AuditRepo.Record on the mutation's own transaction); these handlers read it
// back. RLS on audit_events does the real scoping — a platform owner sees
// every event, an org owner only their org's — so the two handlers differ only
// in which filters they expose, not in a privilege check beyond the route gate.

func auditEventView(e models.AuditEventRow) gin.H {
	return gin.H{
		"id": e.ID, "org_id": e.OrgID, "user_id": e.UserID,
		"action": e.Action, "resource_type": e.ResourceType, "resource_id": e.ResourceID,
		"details": e.Details, "ip_address": e.IPAddress, "user_agent": e.UserAgent,
		"created_at": e.CreatedAt,
	}
}

// ListAuditEvents is GET /api/admin/audit?org_id=&user_id=&action=&limit=&offset=
// (platform owner): the platform-wide audit log, newest first.
func ListAuditEvents(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		limit, offset := parsePageQuery(c)
		f := models.AuditFilter{
			OrgID: c.Query("org_id"), UserID: c.Query("user_id"),
			Action: c.Query("action"), Limit: limit, Offset: offset,
		}
		events, err := d.AdminOps.ListAuditEvents(c.Request.Context(), tx, f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(events))
		for _, e := range events {
			out = append(out, auditEventView(e))
		}
		c.JSON(http.StatusOK, gin.H{"audit_events": out})
	}
}

// ListOrgAuditEvents is GET /api/orgs/:org_slug/audit?action=&limit=&offset=
// (org owner): this org's audit log only. The org filter is forced to the
// resolved org and RLS enforces the same boundary independently.
func ListOrgAuditEvents(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		limit, offset := parsePageQuery(c)
		f := models.AuditFilter{
			OrgID: oc.OrgID, UserID: c.Query("user_id"),
			Action: c.Query("action"), Limit: limit, Offset: offset,
		}
		events, err := d.AdminOps.ListAuditEvents(c.Request.Context(), tx, f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(events))
		for _, e := range events {
			out = append(out, auditEventView(e))
		}
		c.JSON(http.StatusOK, gin.H{"audit_events": out})
	}
}
