package models

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Notification is an in-app notification row. Recipients see and mark-read
// only their own rows (RLS on recipient_id). Written both from the request
// path (inline mention) and from worker fan-out (broadcast) at pool privilege.
type Notification struct {
	ID          string
	OrgID       string
	RecipientID string
	Type        string
	Title       string
	Body        string
	LinkURL     string
	ActorID     *string
	ReadAt      *time.Time
	CreatedAt   time.Time
}

type NotificationRepo struct{}

func NewNotificationRepo() *NotificationRepo { return &NotificationRepo{} }

const notificationColumns = `id, org_id, recipient_id, type, title, body, link_url, actor_id, read_at, created_at`

func (r *NotificationRepo) Create(ctx context.Context, q Querier, orgID, recipientID, typ, title, body, linkURL string, actorID *string) (*Notification, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO notifications (org_id, recipient_id, type, title, body, link_url, actor_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+notificationColumns, orgID, recipientID, typ, title, body, linkURL, actorID)
	return scanNotification(row)
}

// ListByRecipient returns a user's notifications newest first, capped at
// limit. Unread and read are interleaved by recency (the bell shows both).
func (r *NotificationRepo) ListByRecipient(ctx context.Context, q Querier, recipientID string, limit int) ([]*Notification, error) {
	rows, err := q.Query(ctx, `
		SELECT `+notificationColumns+` FROM notifications
		WHERE recipient_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, recipientID, limit)
	if err != nil {
		return nil, fmt.Errorf("models: list notifications: %w", err)
	}
	defer rows.Close()
	var out []*Notification
	for rows.Next() {
		n, err := scanNotificationRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CountUnread returns the recipient's unread count for the nav badge.
func (r *NotificationRepo) CountUnread(ctx context.Context, q Querier, recipientID string) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE recipient_id = $1 AND read_at IS NULL`, recipientID).Scan(&n); err != nil {
		return 0, fmt.Errorf("models: count unread: %w", err)
	}
	return n, nil
}

// MarkRead marks one notification read (RLS scopes it to the caller's rows).
func (r *NotificationRepo) MarkRead(ctx context.Context, q Querier, id, recipientID string) error {
	tag, err := q.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE id = $1 AND recipient_id = $2 AND read_at IS NULL`, id, recipientID)
	if err != nil {
		return fmt.Errorf("models: mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead marks every unread notification read for the caller.
func (r *NotificationRepo) MarkAllRead(ctx context.Context, q Querier, recipientID string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE recipient_id = $1 AND read_at IS NULL`, recipientID)
	if err != nil {
		return fmt.Errorf("models: mark all read: %w", err)
	}
	return nil
}

func scanNotification(row pgx.Row) (*Notification, error) {
	var n Notification
	if err := row.Scan(&n.ID, &n.OrgID, &n.RecipientID, &n.Type, &n.Title, &n.Body, &n.LinkURL, &n.ActorID, &n.ReadAt, &n.CreatedAt); err != nil {
		return nil, fmt.Errorf("models: scan notification: %w", err)
	}
	return &n, nil
}

func scanNotificationRows(rows pgx.Rows) (*Notification, error) {
	var n Notification
	if err := rows.Scan(&n.ID, &n.OrgID, &n.RecipientID, &n.Type, &n.Title, &n.Body, &n.LinkURL, &n.ActorID, &n.ReadAt, &n.CreatedAt); err != nil {
		return nil, fmt.Errorf("models: scan notification: %w", err)
	}
	return &n, nil
}
