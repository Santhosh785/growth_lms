package handlers

import (
	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
)

// Task 7 server-rendered pages. Each renders a thin HTMX/JS shell that drives
// the JSON API with the session cookie (same pattern as the Task 5 learner UI
// — no CSRF token on those same-origin fetch calls, SameSite=Lax cookies).

// CommunityPage renders the org community (thread list + composer).
func CommunityPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Discussions.Execute(c.Writer, gin.H{"Slug": oc.Slug})
	}
}

// ThreadPage renders a single discussion thread.
func ThreadPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		thread, _ := middleware.ThreadFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Thread.Execute(c.Writer, gin.H{
			"ThreadID":    thread.ID,
			"Title":       thread.Title,
			"UserID":      ac.UserID,
			"IsModerator": isModeratorOrOwner(oc),
		})
	}
}

// NotificationsPage renders the in-app notification inbox.
func NotificationsPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Notifications.Execute(c.Writer, gin.H{})
	}
}

// BoardPage renders a collaborative board.
func BoardPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		board, _ := middleware.BoardFromGin(c)
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Board.Execute(c.Writer, gin.H{"BoardID": board.ID, "Title": board.Title})
	}
}

// ModerationPage renders the moderator report queue.
func ModerationPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Moderation.Execute(c.Writer, gin.H{"Slug": oc.Slug})
	}
}
