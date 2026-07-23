package handlers

import (
	"errors"
	"html"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Task 7 notification preferences (per-user/org/category) and the public
// one-click unsubscribe flow. Preferences are self-scoped under an org.
// Unsubscribe is unauthenticated and resolves through the resolve_unsubscribe
// SECURITY DEFINER function (no RLS session context), so it runs against the
// pool directly rather than a request transaction.

var notificationCategories = []string{"mentions", "replies", "broadcasts", "digest"}

// GetNotificationPreferences returns the caller's per-category preferences for
// an org, filling in the opted-in default for any category with no stored row.
func GetNotificationPreferences(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		stored, err := d.NotificationPrefs.ListByUserOrg(c.Request.Context(), tx, ac.UserID, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		byCat := make(map[string]*models.NotificationPreference, len(stored))
		for _, p := range stored {
			byCat[p.Category] = p
		}
		out := make([]gin.H, 0, len(notificationCategories))
		for _, cat := range notificationCategories {
			email, inapp := true, true // opted-in default
			if p, ok := byCat[cat]; ok {
				email, inapp = p.EmailEnabled, p.InAppEnabled
			}
			out = append(out, gin.H{"category": cat, "email_enabled": email, "inapp_enabled": inapp})
		}
		c.JSON(http.StatusOK, gin.H{"preferences": out})
	}
}

type updatePreferenceRequest struct {
	Category     string `json:"category" binding:"required"`
	EmailEnabled bool   `json:"email_enabled"`
	InAppEnabled bool   `json:"inapp_enabled"`
}

// UpdateNotificationPreference upserts one category preference.
func UpdateNotificationPreference(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updatePreferenceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if !validCategory(req.Category) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid category"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		pref, err := d.NotificationPrefs.Upsert(c.Request.Context(), tx, ac.UserID, oc.OrgID, req.Category, req.EmailEnabled, req.InAppEnabled)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"category": pref.Category, "email_enabled": pref.EmailEnabled, "inapp_enabled": pref.InAppEnabled,
		})
	}
}

func validCategory(cat string) bool {
	for _, c := range notificationCategories {
		if c == cat {
			return true
		}
	}
	return false
}

// UnsubscribePage renders a confirmation page for an unsubscribe token. Using
// a confirm-then-POST (rather than acting on GET) avoids email link-scanners
// silently unsubscribing recipients. Public, unauthenticated.
func UnsubscribePage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Param("token")
		page := `<!doctype html><html><head><meta charset="utf-8"><title>Unsubscribe</title>` +
			`<meta name="viewport" content="width=device-width, initial-scale=1">` +
			`<style>body{font-family:system-ui,sans-serif;max-width:480px;margin:60px auto;padding:0 16px}` +
			`button{padding:10px 18px;font-size:15px;cursor:pointer}</style></head><body>` +
			`<h1>Unsubscribe</h1><p>Stop receiving these email notifications?</p>` +
			`<form method="post" action="/unsubscribe/` + html.EscapeString(token) + `">` +
			`<button type="submit">Unsubscribe</button></form></body></html>`
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
	}
}

// Unsubscribe applies an unsubscribe token. Idempotent: an unknown or used
// token still renders a success page (never reveals whether a token was
// valid). Public, unauthenticated — resolved via resolve_unsubscribe against
// the pool directly.
func Unsubscribe(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Param("token")
		_, err := d.UnsubscribeTokens.Resolve(c.Request.Context(), d.Pool, token)
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			c.Data(http.StatusInternalServerError, "text/html; charset=utf-8",
				[]byte(`<!doctype html><p>Something went wrong. Please try again later.</p>`))
			return
		}
		page := `<!doctype html><html><head><meta charset="utf-8"><title>Unsubscribed</title>` +
			`<style>body{font-family:system-ui,sans-serif;max-width:480px;margin:60px auto;padding:0 16px}</style>` +
			`</head><body><h1>You're unsubscribed</h1>` +
			`<p>You will no longer receive these email notifications. You can re-enable them anytime in your notification preferences.</p>` +
			`</body></html>`
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
	}
}
