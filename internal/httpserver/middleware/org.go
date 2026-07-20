package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/dbctx"
	"growth-lms/internal/models"
)

const orgContextKey = "org_context"

// OrgContext is what ResolveOrg resolves for the organization named by
// the request's :org_slug path parameter: which org, and the caller's
// role within it (empty if the caller is not a member but is a platform
// owner viewing it for support/admin purposes).
type OrgContext struct {
	OrgID           string
	Slug            string
	Role            string
	IsPlatformOwner bool
}

// OrgContextFromGin returns the OrgContext stored by ResolveOrg, and
// whether one was present.
func OrgContextFromGin(c *gin.Context) (OrgContext, bool) {
	v, ok := c.Get(orgContextKey)
	if !ok {
		return OrgContext{}, false
	}
	oc, ok := v.(OrgContext)
	return oc, ok
}

// ResolveOrg reads :org_slug, loads the organization and the caller's
// membership role, and stores OrgContext in the Gin context. It must run
// after WithRequestTx (it queries through the request's transaction) and
// after Authenticate. Responds 404 if the org doesn't exist or isn't
// visible to the caller under RLS (the two are indistinguishable by
// design — see models.ErrNotFound), 403 if the caller has no membership
// and is not a platform owner.
//
// On success it also calls dbctx.SetOrgContext to stamp
// app.current_org_id / app.current_role on the transaction, so
// subsequent org-scoped queries in the handler are correctly RLS-scoped.
func ResolveOrg(orgs *models.OrgRepo, memberships *models.MembershipRepo, profiles *models.ProfileRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, ok := RequestTxFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		ac, ok := AuthContextFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		slug := c.Param("org_slug")
		ctx := c.Request.Context()

		org, err := orgs.GetBySlug(ctx, tx, slug)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "organization not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		role, err := memberships.GetRole(ctx, tx, ac.UserID, org.ID)
		isPlatformOwner := false
		if err != nil {
			if !errors.Is(err, models.ErrNotFound) {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			profile, perr := profiles.GetByID(ctx, tx, ac.UserID)
			if perr != nil || !profile.IsPlatformOwner {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this organization"})
				return
			}
			isPlatformOwner = true
			role = ""
		}

		if err := dbctx.SetOrgContext(ctx, tx, org.ID, role); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.Set(orgContextKey, OrgContext{OrgID: org.ID, Slug: org.Slug, Role: role, IsPlatformOwner: isPlatformOwner})
		c.Next()
	}
}

// RequireRole responds 403 unless the caller's role (resolved by
// ResolveOrg) is one of roles, or the caller is a platform owner. This is
// defense-in-depth alongside RLS, not a replacement for it — the database
// enforces the same boundary independently.
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(c *gin.Context) {
		oc, ok := OrgContextFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if oc.IsPlatformOwner || allowed[oc.Role] {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	}
}

// RequirePlatformOwner responds 403 unless the authenticated caller has
// profiles.is_platform_owner set. Used for platform-wide admin routes
// that have no organization context at all. Must run after
// WithRequestTx.
func RequirePlatformOwner(profiles *models.ProfileRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, ok := RequestTxFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		ac, ok := AuthContextFromGin(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		profile, err := profiles.GetByID(c.Request.Context(), tx, ac.UserID)
		if err != nil || !profile.IsPlatformOwner {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}
