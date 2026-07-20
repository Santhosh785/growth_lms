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
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ProfileRepo struct{}

func NewProfileRepo() *ProfileRepo { return &ProfileRepo{} }

func (r *ProfileRepo) GetByID(ctx context.Context, q Querier, id string) (*Profile, error) {
	row := q.QueryRow(ctx, `
		SELECT id, email, full_name, avatar_url, is_platform_owner, created_at, updated_at
		FROM profiles WHERE id = $1
	`, id)

	var p Profile
	if err := row.Scan(&p.ID, &p.Email, &p.FullName, &p.AvatarURL, &p.IsPlatformOwner, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get profile: %w", err)
	}
	return &p, nil
}

func (r *ProfileRepo) UpdateSelf(ctx context.Context, q Querier, id string, fullName, avatarURL *string) (*Profile, error) {
	row := q.QueryRow(ctx, `
		UPDATE profiles SET full_name = $2, avatar_url = $3, updated_at = now()
		WHERE id = $1
		RETURNING id, email, full_name, avatar_url, is_platform_owner, created_at, updated_at
	`, id, fullName, avatarURL)

	var p Profile
	if err := row.Scan(&p.ID, &p.Email, &p.FullName, &p.AvatarURL, &p.IsPlatformOwner, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update profile: %w", err)
	}
	return &p, nil
}
