package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// This file implements Task 10's plan-catalog admin surface: platform-owner
// CRUD over the plans table and assignment of a plan to an organization.
// Every route here is gated by middleware.RequirePlatformOwner at the route
// layer AND by the plans/organizations RLS policies (app_is_platform_owner()),
// so a non-owner can neither reach the handler nor have their query succeed.
// Reads are authenticated-org-owner-wide too (see OrgQuotaDashboard, which
// surfaces the org's own plan) but writes are platform-owner-only.

// planView is the JSON shape returned for a plan. Pointer limit fields
// serialize as null when uncapped.
func planView(p *models.Plan) gin.H {
	return gin.H{
		"id":                    p.ID,
		"code":                  p.Code,
		"name":                  p.Name,
		"description":           p.Description,
		"max_courses":           p.MaxCourses,
		"max_published_courses": p.MaxPublishedCourses,
		"max_members":           p.MaxMembers,
		"max_storage_bytes":     p.MaxStorageBytes,
		"max_ai_tokens_month":   p.MaxAITokensMonth,
		"price_cents":           p.PriceCents,
		"currency":              p.Currency,
		"is_default":            p.IsDefault,
		"is_active":             p.IsActive,
	}
}

// ListPlans is GET /api/admin/plans (platform owner).
func ListPlans(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		plans, err := d.Plans.List(c.Request.Context(), tx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(plans))
		for _, p := range plans {
			out = append(out, planView(p))
		}
		c.JSON(http.StatusOK, gin.H{"plans": out})
	}
}

// GetPlan is GET /api/admin/plans/:plan_id (platform owner).
func GetPlan(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		p, err := d.Plans.Get(c.Request.Context(), tx, c.Param("plan_id"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, planView(p))
	}
}

// planRequest is the create/update body. Limit fields are pointers so an
// explicit null means "unlimited" and an omitted field is distinguishable
// where it matters. All limits must be non-negative.
type planRequest struct {
	Code                string `json:"code"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	MaxCourses          *int64 `json:"max_courses"`
	MaxPublishedCourses *int64 `json:"max_published_courses"`
	MaxMembers          *int64 `json:"max_members"`
	MaxStorageBytes     *int64 `json:"max_storage_bytes"`
	MaxAITokensMonth    *int64 `json:"max_ai_tokens_month"`
	PriceCents          int64  `json:"price_cents"`
	Currency            string `json:"currency"`
	IsDefault           bool   `json:"is_default"`
	IsActive            bool   `json:"is_active"`
}

// negativeLimit reports whether any provided limit is negative (limits are
// caps, never negative; nil = unlimited is fine).
func (r planRequest) negativeLimit() bool {
	for _, v := range []*int64{r.MaxCourses, r.MaxPublishedCourses, r.MaxMembers, r.MaxStorageBytes, r.MaxAITokensMonth} {
		if v != nil && *v < 0 {
			return true
		}
	}
	return r.PriceCents < 0
}

func (r planRequest) toModel() models.Plan {
	currency := strings.TrimSpace(r.Currency)
	if currency == "" {
		currency = "INR"
	}
	return models.Plan{
		Code:                strings.TrimSpace(r.Code),
		Name:                strings.TrimSpace(r.Name),
		Description:         r.Description,
		MaxCourses:          r.MaxCourses,
		MaxPublishedCourses: r.MaxPublishedCourses,
		MaxMembers:          r.MaxMembers,
		MaxStorageBytes:     r.MaxStorageBytes,
		MaxAITokensMonth:    r.MaxAITokensMonth,
		PriceCents:          r.PriceCents,
		Currency:            currency,
		IsDefault:           r.IsDefault,
		IsActive:            r.IsActive,
	}
}

// CreatePlan is POST /api/admin/plans (platform owner).
func CreatePlan(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req planRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.Name) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "code and name are required"})
			return
		}
		if req.negativeLimit() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limits must not be negative"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		p, err := d.Plans.Create(c.Request.Context(), tx, req.toModel())
		if err != nil {
			if models.IsUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "a plan with this code already exists, or another plan is already the default"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			UserID: &ac.UserID, Action: "plan.created", ResourceType: "plan", ResourceID: &p.ID,
			Details:   map[string]any{"code": p.Code},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusCreated, planView(p))
	}
}

// UpdatePlan is PATCH /api/admin/plans/:plan_id (platform owner). Code is
// immutable; the request's code field is ignored.
func UpdatePlan(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req planRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
			return
		}
		if req.negativeLimit() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limits must not be negative"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		m := req.toModel()
		m.ID = c.Param("plan_id")
		p, err := d.Plans.Update(c.Request.Context(), tx, m)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
				return
			}
			if models.IsUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "another plan is already the default"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(c.Request.Context(), tx, models.AuditEvent{
			UserID: &ac.UserID, Action: "plan.updated", ResourceType: "plan", ResourceID: &p.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, planView(p))
	}
}

// assignPlanRequest assigns a plan to an org. An empty plan_id clears the
// assignment (org falls back to the default plan).
type assignPlanRequest struct {
	PlanID string `json:"plan_id"`
}

// AssignOrgPlan is PUT /api/admin/orgs/:org_slug/plan (platform owner). The org
// is resolved by slug within the handler (this route has no ResolveOrg — see
// registerAdminAPIRoutes' doc comment).
func AssignOrgPlan(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req assignPlanRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		org, err := d.Orgs.GetBySlug(ctx, tx, c.Param("org_slug"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		// Validate the plan exists (a clear 404 beats a FK error) unless clearing.
		if strings.TrimSpace(req.PlanID) != "" {
			if _, err := d.Plans.Get(ctx, tx, req.PlanID); err != nil {
				if errors.Is(err, models.ErrNotFound) {
					c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		if err := d.Plans.AssignToOrg(ctx, tx, org.ID, strings.TrimSpace(req.PlanID)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &org.ID, UserID: &ac.UserID, Action: "org.plan_assigned",
			ResourceType: "organization", ResourceID: &org.ID,
			Details:   map[string]any{"plan_id": req.PlanID},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"org_id": org.ID, "plan_id": req.PlanID})
	}
}
