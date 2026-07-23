package models

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// FeatureFlag is one platform-wide runtime feature flag (Task 10). Its
// default_enabled is the value used for any org that has no override row in
// org_feature_flags.
type FeatureFlag struct {
	Key            string
	Description    string
	DefaultEnabled bool
}

// EffectiveFlag pairs a flag with its resolved value for a particular org and
// whether that value came from an org override (vs the platform default).
type EffectiveFlag struct {
	Key         string
	Description string
	Enabled     bool
	Overridden  bool
}

type FeatureFlagRepo struct{}

func NewFeatureFlagRepo() *FeatureFlagRepo { return &FeatureFlagRepo{} }

// List returns the full flag catalog, ordered by key.
func (r *FeatureFlagRepo) List(ctx context.Context, q Querier) ([]*FeatureFlag, error) {
	rows, err := q.Query(ctx, `SELECT key, description, default_enabled FROM feature_flags ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("models: list feature flags: %w", err)
	}
	defer rows.Close()

	var out []*FeatureFlag
	for rows.Next() {
		var f FeatureFlag
		if err := rows.Scan(&f.Key, &f.Description, &f.DefaultEnabled); err != nil {
			return nil, fmt.Errorf("models: scan feature flag: %w", err)
		}
		out = append(out, &f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list feature flags: %w", err)
	}
	return out, nil
}

// Get returns one flag by key.
func (r *FeatureFlagRepo) Get(ctx context.Context, q Querier, key string) (*FeatureFlag, error) {
	var f FeatureFlag
	err := q.QueryRow(ctx, `SELECT key, description, default_enabled FROM feature_flags WHERE key = $1`, key).
		Scan(&f.Key, &f.Description, &f.DefaultEnabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get feature flag: %w", err)
	}
	return &f, nil
}

// Upsert creates or updates a flag by key (platform-owner action). Idempotent
// so re-seeding or re-declaring a flag from the console is safe.
func (r *FeatureFlagRepo) Upsert(ctx context.Context, q Querier, f FeatureFlag) error {
	_, err := q.Exec(ctx, `
		INSERT INTO feature_flags (key, description, default_enabled)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET
			description = EXCLUDED.description,
			default_enabled = EXCLUDED.default_enabled,
			updated_at = now()`,
		f.Key, f.Description, f.DefaultEnabled)
	if err != nil {
		return fmt.Errorf("models: upsert feature flag: %w", err)
	}
	return nil
}

// Delete removes a flag and (via ON DELETE CASCADE) any org overrides of it.
func (r *FeatureFlagRepo) Delete(ctx context.Context, q Querier, key string) error {
	tag, err := q.Exec(ctx, `DELETE FROM feature_flags WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("models: delete feature flag: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListEffectiveForOrg returns every flag with its resolved value for one org:
// the org's override if present, else the flag default. A single LEFT JOIN so
// one query covers the whole catalog.
func (r *FeatureFlagRepo) ListEffectiveForOrg(ctx context.Context, q Querier, orgID string) ([]EffectiveFlag, error) {
	rows, err := q.Query(ctx, `
		SELECT f.key, f.description, f.default_enabled, o.enabled
		FROM feature_flags f
		LEFT JOIN org_feature_flags o ON o.flag_key = f.key AND o.org_id = $1
		ORDER BY f.key`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list effective flags: %w", err)
	}
	defer rows.Close()

	var out []EffectiveFlag
	for rows.Next() {
		var (
			key, desc string
			def       bool
			override  *bool
		)
		if err := rows.Scan(&key, &desc, &def, &override); err != nil {
			return nil, fmt.Errorf("models: scan effective flag: %w", err)
		}
		ef := EffectiveFlag{Key: key, Description: desc, Enabled: def}
		if override != nil {
			ef.Enabled = *override
			ef.Overridden = true
		}
		out = append(out, ef)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list effective flags: %w", err)
	}
	return out, nil
}

// IsEnabledForOrg resolves a single flag's effective value for an org. A flag
// key that doesn't exist resolves to false (fail-closed) rather than an error,
// so callers can gate on flags that may not be declared yet.
func (r *FeatureFlagRepo) IsEnabledForOrg(ctx context.Context, q Querier, orgID, key string) (bool, error) {
	var (
		def      *bool
		override *bool
	)
	err := q.QueryRow(ctx, `
		SELECT f.default_enabled, o.enabled
		FROM feature_flags f
		LEFT JOIN org_feature_flags o ON o.flag_key = f.key AND o.org_id = $2
		WHERE f.key = $1`, key, orgID).Scan(&def, &override)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("models: is flag enabled: %w", err)
	}
	if override != nil {
		return *override, nil
	}
	return def != nil && *def, nil
}

// SetOrgOverride upserts an org's override for a flag (org-owner or
// platform-owner action). updatedBy may be "" for a system-initiated change.
func (r *FeatureFlagRepo) SetOrgOverride(ctx context.Context, q Querier, orgID, key string, enabled bool, updatedBy string) error {
	var by any
	if updatedBy != "" {
		by = updatedBy
	}
	_, err := q.Exec(ctx, `
		INSERT INTO org_feature_flags (org_id, flag_key, enabled, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, flag_key) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()`,
		orgID, key, enabled, by)
	if err != nil {
		return fmt.Errorf("models: set org flag override: %w", err)
	}
	return nil
}

// ClearOrgOverride removes an org's override so the flag falls back to its
// platform default. Removing a non-existent override is a no-op (not an
// error).
func (r *FeatureFlagRepo) ClearOrgOverride(ctx context.Context, q Querier, orgID, key string) error {
	_, err := q.Exec(ctx, `DELETE FROM org_feature_flags WHERE org_id = $1 AND flag_key = $2`, orgID, key)
	if err != nil {
		return fmt.Errorf("models: clear org flag override: %w", err)
	}
	return nil
}
