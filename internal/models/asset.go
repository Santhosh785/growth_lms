package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	AssetTypeImage = "image"
	AssetTypeVideo = "video"
	AssetTypeFile  = "file"

	StorageProviderBunny    = "bunny"
	StorageProviderSupabase = "supabase"

	ProcessingStatusPending    = "pending"
	ProcessingStatusProcessing = "processing"
	ProcessingStatusReady      = "ready"
	ProcessingStatusFailed     = "failed"
)

type Asset struct {
	ID                 string
	OrgID              string
	CourseID           string
	Type               string
	Filename           string
	SizeBytes          *int64
	MimeType           *string
	StorageProvider    string
	StorageKey         string
	SignedURL          *string
	SignedURLExpiresAt *time.Time
	ProcessingStatus   string
	DurationSeconds    *int
	ThumbnailURL       *string
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type AssetRepo struct{}

func NewAssetRepo() *AssetRepo { return &AssetRepo{} }

const assetColumns = `id, org_id, course_id, type, filename, size_bytes, mime_type, storage_provider, storage_key, signed_url, signed_url_expires_at, processing_status, duration_seconds, thumbnail_url, created_by, created_at, updated_at`

// Create inserts an assets row immediately when an upload URL is issued,
// so the asset is trackable from the start (spec: video assets start
// processing_status='pending'; the DB default 'ready' covers image/file,
// but callers pass initialStatus explicitly to keep intent visible at
// the call site rather than relying on an implicit default).
func (r *AssetRepo) Create(ctx context.Context, q Querier, orgID, courseID, assetType, filename, storageProvider, storageKey, createdBy, initialStatus string) (*Asset, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO assets (org_id, course_id, type, filename, storage_provider, storage_key, created_by, processing_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+assetColumns, orgID, courseID, assetType, filename, storageProvider, storageKey, createdBy, initialStatus)
	return scanAsset(row)
}

// CreateWithID inserts an assets row with a caller-supplied ID, used when
// the ID must be known before the row exists (e.g. Supabase Storage
// uploads, whose storage_key path embeds the asset ID).
func (r *AssetRepo) CreateWithID(ctx context.Context, q Querier, id, orgID, courseID, assetType, filename, storageProvider, storageKey, createdBy, initialStatus string) (*Asset, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO assets (id, org_id, course_id, type, filename, storage_provider, storage_key, created_by, processing_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+assetColumns, id, orgID, courseID, assetType, filename, storageProvider, storageKey, createdBy, initialStatus)
	return scanAsset(row)
}

func (r *AssetRepo) Get(ctx context.Context, q Querier, id string) (*Asset, error) {
	row := q.QueryRow(ctx, `SELECT `+assetColumns+` FROM assets WHERE id = $1`, id)
	return scanAsset(row)
}

// SetMetadata is used by the upload-confirmation handler once a
// server-side HEAD check has confirmed the object really exists —
// records size/mime and marks it ready.
func (r *AssetRepo) SetMetadata(ctx context.Context, q Querier, id string, sizeBytes int64, mimeType string) (*Asset, error) {
	row := q.QueryRow(ctx, `
		UPDATE assets SET size_bytes = $2, mime_type = $3, processing_status = 'ready', updated_at = now()
		WHERE id = $1 RETURNING `+assetColumns, id, sizeBytes, mimeType)
	return scanAsset(row)
}

// SetProcessingStatus is used by the Bunny transcode-complete webhook
// path to mark a video asset ready/failed and fill in duration/thumbnail.
func (r *AssetRepo) SetProcessingStatus(ctx context.Context, q Querier, id, status string, durationSeconds *int, thumbnailURL *string) (*Asset, error) {
	row := q.QueryRow(ctx, `
		UPDATE assets SET processing_status = $2, duration_seconds = $3, thumbnail_url = $4, updated_at = now()
		WHERE id = $1 RETURNING `+assetColumns, id, status, durationSeconds, thumbnailURL)
	return scanAsset(row)
}

// RefreshSignedURL persists a newly generated signed URL and its expiry
// (generation itself happens via the media package's clients — this
// method only stores the result).
func (r *AssetRepo) RefreshSignedURL(ctx context.Context, q Querier, id, signedURL string, expiresAt time.Time) (*Asset, error) {
	row := q.QueryRow(ctx, `
		UPDATE assets SET signed_url = $2, signed_url_expires_at = $3, updated_at = now()
		WHERE id = $1 RETURNING `+assetColumns, id, signedURL, expiresAt)
	return scanAsset(row)
}

// RevokeSignedURLsForCourse clears the cached signed_url/expiry on every
// asset belonging to a course, called on unpublish so a previously issued
// long-lived URL can no longer be trusted as still-valid — the next access
// attempt must go through refresh-url, which re-checks the course's current
// status.
func (r *AssetRepo) RevokeSignedURLsForCourse(ctx context.Context, q Querier, courseID string) error {
	_, err := q.Exec(ctx, `
		UPDATE assets SET signed_url = NULL, signed_url_expires_at = NULL, updated_at = now()
		WHERE course_id = $1 AND signed_url IS NOT NULL`, courseID)
	if err != nil {
		return fmt.Errorf("models: revoke signed urls for course: %w", err)
	}
	return nil
}

func scanAsset(row pgx.Row) (*Asset, error) {
	var a Asset
	if err := row.Scan(&a.ID, &a.OrgID, &a.CourseID, &a.Type, &a.Filename, &a.SizeBytes, &a.MimeType,
		&a.StorageProvider, &a.StorageKey, &a.SignedURL, &a.SignedURLExpiresAt, &a.ProcessingStatus,
		&a.DurationSeconds, &a.ThumbnailURL, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan asset: %w", err)
	}
	return &a, nil
}
