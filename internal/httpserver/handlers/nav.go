package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
	"growth-lms/internal/models"
)

// NavFragment renders the shared nav bar every server-rendered page
// htmx-loads on load (see templates/nav.html). Self-contained on purpose:
// it resolves auth state and role-based links (org admin, platform admin)
// itself, so pages that embed the nav don't need their own handler to
// thread that data through — only WithRequestTx + OptionalAuthenticate
// are required upstream (see registerNavRoute in server.go).
func NavFragment(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, ok := middleware.AuthContextFromGin(c)
		if !ok {
			c.Header("Content-Type", "text/html; charset=utf-8")
			_ = templates.Nav.ExecuteTemplate(c.Writer, "nav", gin.H{"LoggedIn": false})
			return
		}

		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		isPlatformOwner := false
		if profile, err := d.Profiles.GetByID(ctx, tx, ac.UserID); err == nil {
			isPlatformOwner = profile.IsPlatformOwner
		}

		ownedOrgs, err := d.Memberships.ListOwnedByUser(ctx, tx, ac.UserID)
		if err != nil {
			ownedOrgs = nil
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Nav.ExecuteTemplate(c.Writer, "nav", gin.H{
			"LoggedIn":        true,
			"Email":           ac.Email,
			"IsPlatformOwner": isPlatformOwner,
			"OwnedOrgs":       ownedOrgs,
		})
	}
}

// NavLogoutRedirect is a browser-navigable sibling of the JSON Logout
// handler, so the nav's plain <a> logout link works without any
// client-side JS: same effect (sign out of Supabase, clear session
// cookies, audit log), but responds with a redirect to "/" instead of a
// JSON body. Requires Authenticate to have run, matching Logout's own
// precondition.
func NavLogoutRedirect(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)

		if token, err := c.Cookie(middleware.SessionCookieName); err == nil && token != "" {
			_ = d.Supabase.SignOut(c.Request.Context(), token)
		}
		clearSessionCookies(c, d.Config)

		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			UserID: &ac.UserID, Action: "auth.logout", ResourceType: "profile", ResourceID: &ac.UserID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})

		c.Redirect(http.StatusFound, "/")
	}
}
