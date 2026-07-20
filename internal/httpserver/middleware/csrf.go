package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/config"
)

// CSRFCookieName holds a random per-browser token for the double-submit-
// cookie CSRF check. Deliberately NOT HttpOnly: the course-editor page's
// own script needs to read it to echo it back as the X-CSRF-Token header
// on htmx requests. This is only applied to the cookie-authenticated HTML
// course-editor routes (see registerCourseRoutes) — JSON API routes carry
// no ambient cookie auth, so CSRF does not apply to them.
const CSRFCookieName = "lms_csrf"

// CSRFHeaderName is the header htmx/JS must echo the cookie's value back
// on for any state-changing request.
const CSRFHeaderName = "X-CSRF-Token"

// EnsureCSRFCookie issues a new lms_csrf cookie if one isn't already
// present, so any GET to a course-editor page hands the browser a token
// it can echo back on subsequent mutations. Safe to call on every request
// in the HTML route group, including GETs.
func EnsureCSRFCookie(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, err := c.Cookie(CSRFCookieName); err != nil {
			token, genErr := generateCSRFToken()
			if genErr == nil {
				secure := cfg.Env != config.EnvDevelopment
				c.SetCookie(CSRFCookieName, token, 0, "/", "", secure, false)
			}
		}
		c.Next()
	}
}

// RequireCSRF rejects state-changing requests (anything but
// GET/HEAD/OPTIONS) unless the X-CSRF-Token header matches the lms_csrf
// cookie, using a constant-time comparison. Must run after a cookie has
// been established (EnsureCSRFCookie, or a prior page load).
func RequireCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}

		cookie, err := c.Cookie(CSRFCookieName)
		if err != nil || cookie == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing csrf token"})
			return
		}
		header := c.GetHeader(CSRFHeaderName)
		if header == "" || subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid csrf token"})
			return
		}
		c.Next()
	}
}

func generateCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
