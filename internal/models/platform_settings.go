package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PlatformSettings is the platform-wide (not org-scoped) commission
// configuration (db/migrations/000006_commerce.up.sql). Task 1 seeds
// exactly one row; this repo never creates one.
type PlatformSettings struct {
	ID                string
	CommissionPercent float64 // NUMERIC(5,2); a rate, never a money amount
	UpdatedBy         *string
	UpdatedAt         time.Time
}

type PlatformSettingsRepo struct{}

func NewPlatformSettingsRepo() *PlatformSettingsRepo { return &PlatformSettingsRepo{} }

const platformSettingsColumns = `id, commission_percent, updated_by, updated_at`

// Get returns the single platform_settings row. If the table is empty,
// that's a migration/seed bug and this surfaces as ErrNotFound, which is
// the correct failure mode, not a silent default. This is the single
// source OrderRepo.Create's caller reads commission_percent from before
// snapshotting it onto a new order.
func (r *PlatformSettingsRepo) Get(ctx context.Context, q Querier) (*PlatformSettings, error) {
	row := q.QueryRow(ctx, `SELECT `+platformSettingsColumns+` FROM platform_settings LIMIT 1`)

	var s PlatformSettings
	if err := row.Scan(&s.ID, &s.CommissionPercent, &s.UpdatedBy, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get platform settings: %w", err)
	}
	return &s, nil
}

// Update sets commission_percent/updated_by/updated_at. This repo does
// not check who updatedBy is or whether they're the platform owner —
// that's middleware.RequirePlatformOwner's job at the route layer.
// Existing orders keep their already-snapshotted
// commission_rate_snapshot — this update only affects orders created
// after it.
func (r *PlatformSettingsRepo) Update(ctx context.Context, q Querier, id string, commissionPercent float64, updatedBy string) (*PlatformSettings, error) {
	row := q.QueryRow(ctx, `
		UPDATE platform_settings
		SET commission_percent = $2, updated_by = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+platformSettingsColumns, id, commissionPercent, updatedBy)

	var s PlatformSettings
	if err := row.Scan(&s.ID, &s.CommissionPercent, &s.UpdatedBy, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update platform settings: %w", err)
	}
	return &s, nil
}
