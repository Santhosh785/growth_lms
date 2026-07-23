// Task 7 community notification dispatch. Four notification kinds — mention,
// reply, report-filed, and broadcast — each enqueued from the discussions/
// moderation/notifications HTTP handlers and dispatched here, never sent
// synchronously in the request path (same rule as the Task 5 notifications
// in notifications.go). Every handler ALWAYS writes an in-app notifications
// row and conditionally sends email, gated by the global
// profiles.notification_opt_out kill-switch AND the per-category
// notification_preferences.email_enabled flag.
package worker

import (
	"github.com/hibiken/asynq"
)

const (
	TypeNotifyMention     = "notify:mention"
	TypeNotifyReply       = "notify:reply"
	TypeNotifyReportFiled = "notify:report_filed"
	TypeNotifyBroadcast   = "notify:broadcast"
)

// NotifyMentionPayload is enqueued once per @[uuid]-mentioned member when a
// post is created.
type NotifyMentionPayload struct {
	OrgID       string `json:"org_id"`
	RecipientID string `json:"recipient_id"`
	ActorID     string `json:"actor_id"`
	ActorName   string `json:"actor_name"`
	ThreadID    string `json:"thread_id"`
	ThreadTitle string `json:"thread_title"`
	PostID      string `json:"post_id"`
	Preview     string `json:"preview"`
	LinkPath    string `json:"link_path"`
}

// NotifyReplyPayload is enqueued to a root post's author when someone replies.
type NotifyReplyPayload struct {
	OrgID       string `json:"org_id"`
	RecipientID string `json:"recipient_id"`
	ActorID     string `json:"actor_id"`
	ActorName   string `json:"actor_name"`
	ThreadID    string `json:"thread_id"`
	ThreadTitle string `json:"thread_title"`
	PostID      string `json:"post_id"`
	Preview     string `json:"preview"`
	LinkPath    string `json:"link_path"`
}

// NotifyReportFiledPayload is enqueued when a member reports a post; the
// handler fans out to every moderator/owner of the org.
type NotifyReportFiledPayload struct {
	OrgID    string `json:"org_id"`
	PostID   string `json:"post_id"`
	ReportID string `json:"report_id"`
	Reason   string `json:"reason"`
	LinkPath string `json:"link_path"`
}

// NotifyBroadcastPayload is enqueued by an owner/teacher broadcast; the
// handler fans out to every member of the org.
type NotifyBroadcastPayload struct {
	OrgID    string `json:"org_id"`
	ActorID  string `json:"actor_id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	LinkPath string `json:"link_path"`
}

func EnqueueMentionNotification(client *asynq.Client, payload NotifyMentionPayload) error {
	return enqueueNotification(client, TypeNotifyMention, payload)
}

func EnqueueReplyNotification(client *asynq.Client, payload NotifyReplyPayload) error {
	return enqueueNotification(client, TypeNotifyReply, payload)
}

func EnqueueReportFiledNotification(client *asynq.Client, payload NotifyReportFiledPayload) error {
	return enqueueNotification(client, TypeNotifyReportFiled, payload)
}

func EnqueueBroadcastNotification(client *asynq.Client, payload NotifyBroadcastPayload) error {
	return enqueueNotification(client, TypeNotifyBroadcast, payload)
}
