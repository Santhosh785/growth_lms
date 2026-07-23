package handlers

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/worker"
)

// Task 7 in-app notifications. List/unread-count/mark-read are recipient-
// scoped: no org in the path, RLS restricts every row to recipient_id =
// current user. Broadcast creation is an owner/teacher action that fans out
// through the notification worker.

const notificationListLimit = 50

// ListNotifications returns the caller's most recent notifications (JSON).
func ListNotifications(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		items, err := d.Notifications.ListByRecipient(c.Request.Context(), tx, ac.UserID, notificationListLimit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(items))
		for i, n := range items {
			out[i] = gin.H{
				"id": n.ID, "type": n.Type, "title": n.Title, "body": n.Body,
				"link_url": n.LinkURL, "read_at": n.ReadAt, "created_at": n.CreatedAt,
			}
		}
		c.JSON(http.StatusOK, gin.H{"notifications": out})
	}
}

// NotificationsUnreadCount returns an HTML badge fragment for the nav bell
// (HTMX hx-get target). Empty when there is nothing unread, so the badge
// disappears.
func NotificationsUnreadCount(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		n, err := d.Notifications.CountUnread(c.Request.Context(), tx, ac.UserID)
		if err != nil {
			c.Data(http.StatusOK, "text/html; charset=utf-8", nil)
			return
		}
		if n <= 0 {
			c.Data(http.StatusOK, "text/html; charset=utf-8", nil)
			return
		}
		label := fmt.Sprintf("%d", n)
		if n > 99 {
			label = "99+"
		}
		frag := fmt.Sprintf(`<span class="nav-badge" aria-label="%s unread notifications">%s</span>`,
			html.EscapeString(label), html.EscapeString(label))
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(frag))
	}
}

// MarkNotificationRead marks one notification read.
func MarkNotificationRead(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Notifications.MarkRead(c.Request.Context(), tx, c.Param("id"), ac.UserID); err != nil {
			if errNotFoundResponse(c, err, "notification not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "read"})
	}
}

// MarkAllNotificationsRead marks every unread notification read.
func MarkAllNotificationsRead(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Notifications.MarkAllRead(c.Request.Context(), tx, ac.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

type broadcastRequest struct {
	Title    string `json:"title" binding:"required"`
	Body     string `json:"body" binding:"required"`
	LinkPath string `json:"link_path"`
}

// CreateBroadcast enqueues an owner/teacher announcement to every org member.
func CreateBroadcast(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req broadcastRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if err := worker.EnqueueBroadcastNotification(d.AsyncQueue, worker.NotifyBroadcastPayload{
			OrgID: oc.OrgID, ActorID: ac.UserID, Title: req.Title, Body: req.Body, LinkPath: req.LinkPath,
		}); err != nil {
			slog.Default().Error("handlers: enqueue broadcast", "error", err, "org_id", oc.OrgID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"status": "queued"})
	}
}
