package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/quota"
)

// This file implements Task 10's usage & quota surface. An org owner sees
// their own org's usage-vs-limits at GET /api/orgs/:org_slug/quota; a platform
// owner sees any org's at GET /api/admin/orgs/:org_slug/quota. Both render the
// same quota.Report. The enforcement counterpart (enforceQuota) lives here too
// and is called from resource-creation handlers.

// quotaReportView renders a quota.Report as JSON.
func quotaReportView(r *quota.Report) gin.H {
	limits := make([]gin.H, 0, len(r.Limits))
	for _, l := range r.Limits {
		limits = append(limits, gin.H{
			"dimension": l.Dimension,
			"used":      l.Used,
			"limit":     l.Limit,
			"exceeded":  l.Exceeded(),
		})
	}
	return gin.H{
		"plan": gin.H{
			"id":   r.Plan.ID,
			"code": r.Plan.Code,
			"name": r.Plan.Name,
		},
		"limits": limits,
	}
}

// OrgQuotaDashboard is GET /api/orgs/:org_slug/quota (owner). It reports the
// caller's own org usage against its plan.
func OrgQuotaDashboard(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		report, err := d.Quota.Report(ctx, tx, oc.OrgID, time.Now())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, quotaReportView(report))
	}
}

// PlatformOrgQuota is GET /api/admin/orgs/:org_slug/quota (platform owner). The
// org is resolved by slug within the handler.
func PlatformOrgQuota(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)

		org, err := d.Orgs.GetBySlug(ctx, tx, c.Param("org_slug"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		report, err := d.Quota.Report(ctx, tx, org.ID, time.Now())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, quotaReportView(report))
	}
}

// enforceQuota checks whether the org may add `delta` more of dimension d and,
// if not, writes a 402 Payment Required response and returns false. Callers
// that get false must return immediately without performing the create. A
// quota-service error is treated as a 500 (fail-closed on infrastructure
// errors is safer than silently allowing an over-limit create). On success it
// returns true and writes nothing.
//
// Enforcement is best-effort/advisory against races (two concurrent creates
// could both pass a check at the cap boundary); the plan-limits feature is a
// soft business cap, not a security boundary, so a rare one-over is acceptable
// and cheaper than serializing every create behind a lock.
func (d *AuthDeps) enforceQuota(c *gin.Context, orgID string, dim quota.Dimension) bool {
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	ok, lu, err := d.Quota.Check(ctx, tx, orgID, dim, 1, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}
	if !ok {
		var limit int64
		if lu.Limit != nil {
			limit = *lu.Limit
		}
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":     "plan limit reached",
			"dimension": lu.Dimension,
			"used":      lu.Used,
			"limit":     limit,
		})
		return false
	}
	return true
}
