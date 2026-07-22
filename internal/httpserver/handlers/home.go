package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
)

// HomePage is the public "/" route. Mounted behind OptionalAuthenticate:
// an already-authenticated visitor (valid lms_session cookie) is sent
// straight to /dashboard; anyone else gets the login/signup page.
func HomePage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.AuthContextFromGin(c); ok {
			c.Redirect(http.StatusFound, "/dashboard")
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Home.Execute(c.Writer, nil)
	}
}
