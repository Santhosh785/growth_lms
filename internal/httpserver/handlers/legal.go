package handlers

import (
	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/templates"
)

// PrivacyPage renders GET /privacy — the platform's public privacy policy
// (plan.md Task 12: "privacy and terms pages"). Static content, no auth.
func PrivacyPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = templates.Privacy.Execute(c.Writer, nil)
}

// TermsPage renders GET /terms — the platform's public terms of service.
func TermsPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = templates.Terms.Execute(c.Writer, nil)
}
