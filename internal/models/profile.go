package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned by repository lookups that find no matching row
// (whether because it truly doesn't exist, or because RLS hides it from
// the caller — the two are indistinguishable by design, which is what
// makes tenant isolation real rather than cosmetic).
var ErrNotFound = errors.New("models: not found")

type Profile struct {
	ID              string
	Email           string
	FullName        *string
	AvatarURL       *string
	IsPlatformOwner bool
	// NotificationOptOut is Stage 1's migration 000004 column: learners
	// who set this skip every Task 5 async notification email (assignment
	// graded, certificate issued, announcement posted, course reminder) —
	// the worker handlers in internal/worker/notifications.go check this
	// before ever calling the Resend client.
	NotificationOptOut bool
	// SuspendedAt/SuspendedReason are the Task 10 platform-admin
	// suspension state (migration 000017). A non-nil SuspendedAt means the
	// user is blocked from logging in (auth handler) and from acting on any
	// organization (ResolveOrg). Only a platform owner can set/clear them,
	// via admin_set_user_suspended().
	SuspendedAt     *time.Time
	SuspendedReason *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ProfileRepo struct{}

func NewProfileRepo() *ProfileRepo { return &ProfileRepo{} }

func (r *ProfileRepo) GetByID(ctx context.Context, q Querier, id string) (*Profile, error) {
	row := q.QueryRow(ctx, `
		SELECT id, email, full_name, avatar_url, is_platform_owner, notification_opt_out, suspended_at, suspended_reason, created_at, updated_at
		FROM profiles WHERE id = $1
	`, id)

	var p Profile
	if err := row.Scan(&p.ID, &p.Email, &p.FullName, &p.AvatarURL, &p.IsPlatformOwner, &p.NotificationOptOut, &p.SuspendedAt, &p.SuspendedReason, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get profile: %w", err)
	}
	return &p, nil
}

// IsSuspended reports whether the user is currently suspended, via the
// app_is_user_suspended() SECURITY DEFINER function (migration 000017). Safe to
// call on the raw pool with no RLS session context — the login handler does
// exactly that to refuse a suspended account before issuing a session.
func (r *ProfileRepo) IsSuspended(ctx context.Context, q Querier, id string) (bool, error) {
	var suspended bool
	if err := q.QueryRow(ctx, `SELECT app_is_user_suspended($1)`, id).Scan(&suspended); err != nil {
		return false, fmt.Errorf("models: check user suspended: %w", err)
	}
	return suspended, nil
}

func (r *ProfileRepo) UpdateSelf(ctx context.Context, q Querier, id string, fullName, avatarURL *string) (*Profile, error) {
	row := q.QueryRow(ctx, `
		UPDATE profiles SET full_name = $2, avatar_url = $3, updated_at = now()
		WHERE id = $1
		RETURNING id, email, full_name, avatar_url, is_platform_owner, notification_opt_out, suspended_at, suspended_reason, created_at, updated_at
	`, id, fullName, avatarURL)

	var p Profile
	if err := row.Scan(&p.ID, &p.Email, &p.FullName, &p.AvatarURL, &p.IsPlatformOwner, &p.NotificationOptOut, &p.SuspendedAt, &p.SuspendedReason, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update profile: %w", err)
	}
	return &p, nil
}
