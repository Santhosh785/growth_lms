package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotificationPreference is a per-user, per-org, per-category email/in-app
// opt-in. Absence of a row means opted-in (the default), so preferences only
// need to be persisted when a user turns something off.
type NotificationPreference struct {
	UserID       string
	OrgID        string
	Category     string
	EmailEnabled bool
	InAppEnabled bool
	UpdatedAt    time.Time
}

type NotificationPreferenceRepo struct{}

func NewNotificationPreferenceRepo() *NotificationPreferenceRepo {
	return &NotificationPreferenceRepo{}
}

const notificationPreferenceColumns = `user_id, org_id, category, email_enabled, inapp_enabled, updated_at`

// Upsert stores a preference, replacing any existing row for the same
// (user, org, category).
func (r *NotificationPreferenceRepo) Upsert(ctx context.Context, q Querier, userID, orgID, category string, emailEnabled, inAppEnabled bool) (*NotificationPreference, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO notification_preferences (user_id, org_id, category, email_enabled, inapp_enabled)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, org_id, category)
		DO UPDATE SET email_enabled = EXCLUDED.email_enabled, inapp_enabled = EXCLUDED.inapp_enabled, updated_at = now()
		RETURNING `+notificationPreferenceColumns, userID, orgID, category, emailEnabled, inAppEnabled)
	return scanNotificationPreference(row)
}

// ListByUserOrg returns all stored preferences for a user in an org (rows
// that differ from the opted-in default).
func (r *NotificationPreferenceRepo) ListByUserOrg(ctx context.Context, q Querier, userID, orgID string) ([]*NotificationPreference, error) {
	rows, err := q.Query(ctx, `
		SELECT `+notificationPreferenceColumns+` FROM notification_preferences
		WHERE user_id = $1 AND org_id = $2 ORDER BY category`, userID, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list preferences: %w", err)
	}
	defer rows.Close()
	var out []*NotificationPreference
	for rows.Next() {
		p, err := scanNotificationPreferenceRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// IsEmailEnabled reports whether email for a category is on for a user in an
// org. Missing row = enabled (opt-out model). Called by the worker at pool
// privilege, so it reads across users without RLS scoping.
func (r *NotificationPreferenceRepo) IsEmailEnabled(ctx context.Context, q Querier, userID, orgID, category string) (bool, error) {
	var enabled bool
	err := q.QueryRow(ctx, `
		SELECT email_enabled FROM notification_preferences
		WHERE user_id = $1 AND org_id = $2 AND category = $3`, userID, orgID, category).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("models: is email enabled: %w", err)
	}
	return enabled, nil
}

func scanNotificationPreference(row pgx.Row) (*NotificationPreference, error) {
	var p NotificationPreference
	if err := row.Scan(&p.UserID, &p.OrgID, &p.Category, &p.EmailEnabled, &p.InAppEnabled, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan preference: %w", err)
	}
	return &p, nil
}

func scanNotificationPreferenceRows(rows pgx.Rows) (*NotificationPreference, error) {
	var p NotificationPreference
	if err := rows.Scan(&p.UserID, &p.OrgID, &p.Category, &p.EmailEnabled, &p.InAppEnabled, &p.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan preference: %w", err)
	}
	return &p, nil
}
