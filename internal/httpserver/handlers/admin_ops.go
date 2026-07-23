package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// This file implements Task 10's platform-owner administrative *actions* —
// the write side the read-only dashboards (admin_ui.go) deliberately omitted:
// suspend/reactivate a user, deactivate/reactivate an organization, and take a
// course down (or restore it) across org boundaries. Every route is mounted
// under /api/admin behind middleware.RequirePlatformOwner; each handler also
// goes through a SECURITY DEFINER function that re-checks platform ownership
// (migration 000017), and records an audit event on the same transaction as
// the mutation so "the action happened but wasn't logged" cannot occur.

func parsePageQuery(c *gin.Context) (limit, offset int) {
	if n, err := strconv.Atoi(c.Query("limit")); err == nil {
		limit = n
	}
	if n, err := strconv.Atoi(c.Query("offset")); err == nil && n > 0 {
		offset = n
	}
	return limit, offset
}

// ListUsers is GET /api/admin/users?search=&suspended=1&limit=&offset=
// (platform owner): the platform-wide user directory.
func ListUsers(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		limit, offset := parsePageQuery(c)
		f := models.UserFilter{
			Search: c.Query("search"),
			Limit:  limit, Offset: offset,
		}
		if s := c.Query("suspended"); s == "1" || s == "true" {
			f.SuspendedOnly = true
		}

		users, err := d.AdminOps.ListUsers(c.Request.Context(), tx, f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(users))
		for _, u := range users {
			out = append(out, gin.H{
				"id": u.ID, "email": u.Email, "full_name": u.FullName,
				"is_platform_owner": u.IsPlatformOwner,
				"suspended_at":      u.SuspendedAt, "suspended_reason": u.SuspendedReason,
				"org_count": u.OrgCount, "created_at": u.CreatedAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"users": out})
	}
}

type suspendUserRequest struct {
	Reason string `json:"reason"`
}

// SuspendUser is POST /api/admin/users/:user_id/suspend (platform owner).
func SuspendUser(d *AuthDeps) gin.HandlerFunc {
	return setUserSuspended(d, true, "user.suspended")
}

// ReactivateUser is POST /api/admin/users/:user_id/reactivate (platform owner).
func ReactivateUser(d *AuthDeps) gin.HandlerFunc {
	return setUserSuspended(d, false, "user.reactivated")
}

func setUserSuspended(d *AuthDeps, suspend bool, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req suspendUserRequest
		// Reason is optional; a body is not required to reactivate.
		_ = c.ShouldBindJSON(&req)

		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		targetID := c.Param("user_id")

		if err := d.AdminOps.SetUserSuspended(ctx, tx, targetID, suspend, req.Reason); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "user not found or is a platform owner"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		details := map[string]any{}
		if suspend && req.Reason != "" {
			details["reason"] = req.Reason
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			UserID: &ac.UserID, Action: action, ResourceType: "profile", ResourceID: &targetID,
			Details: details, IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"id": targetID, "suspended": suspend})
	}
}

type deactivateOrgRequest struct {
	Reason string `json:"reason"`
}

// DeactivateOrg is POST /api/admin/orgs/:org_slug/deactivate (platform owner).
func DeactivateOrg(d *AuthDeps) gin.HandlerFunc {
	return setOrgActive(d, false, "org.deactivated")
}

// ReactivateOrg is POST /api/admin/orgs/:org_slug/reactivate (platform owner).
func ReactivateOrg(d *AuthDeps) gin.HandlerFunc {
	return setOrgActive(d, true, "org.reactivated")
}

func setOrgActive(d *AuthDeps, active bool, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req deactivateOrgRequest
		_ = c.ShouldBindJSON(&req)

		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		// Resolve the org by slug directly (no ResolveOrg on the platform
		// surface — see registerAdminAPIRoutes).
		org, err := d.Orgs.GetBySlug(ctx, tx, c.Param("org_slug"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		if err := d.AdminOps.SetOrgActive(ctx, tx, org.ID, active, req.Reason); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		details := map[string]any{}
		if !active && req.Reason != "" {
			details["reason"] = req.Reason
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &org.ID, UserID: &ac.UserID, Action: action,
			ResourceType: "organization", ResourceID: &org.ID,
			Details: details, IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"org_slug": org.Slug, "active": active})
	}
}

type courseStatusRequest struct {
	Reason string `json:"reason"`
}

// TakedownCourse is POST /api/admin/courses/:course_id/takedown (platform
// owner): force any course to "archived" regardless of org membership, for
// abuse/DMCA/policy enforcement.
func TakedownCourse(d *AuthDeps) gin.HandlerFunc {
	return setCourseStatus(d, "archived", "course.takedown")
}

// RestoreCourse is POST /api/admin/courses/:course_id/restore (platform
// owner): return a taken-down course to "draft" so its org can re-review and
// re-publish it.
func RestoreCourse(d *AuthDeps) gin.HandlerFunc {
	return setCourseStatus(d, "draft", "course.restored")
}

func setCourseStatus(d *AuthDeps, status, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req courseStatusRequest
		_ = c.ShouldBindJSON(&req)

		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		courseID := c.Param("course_id")

		if err := d.AdminOps.SetCourseStatus(ctx, tx, courseID, status); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		details := map[string]any{"status": status}
		if req.Reason != "" {
			details["reason"] = req.Reason
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			UserID: &ac.UserID, Action: action, ResourceType: "course", ResourceID: &courseID,
			Details: details, IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"id": courseID, "status": status})
	}
}
