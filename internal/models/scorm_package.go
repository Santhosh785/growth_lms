package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ScormPackage is one teacher-authored SCORM 1.2/2004 package belonging to an
// org (plan.md Task 9's "SCORM package validation, launch, progress, and
// reporting"). slug is a stable per-org identifier, unique per org. Version is
// "1.2" or "2004"; LaunchHref is the resource the API adapter loads, resolved
// from the imsmanifest.xml at import time. Manifest carries the parsed item
// tree (an internal/scorm.Package) as JSON for rendering a table of contents.
type ScormPackage struct {
	ID           string
	OrgID        string
	CourseID     *string
	LessonID     *string
	Slug         string
	Title        string
	Description  string
	Version      string
	Identifier   string
	LaunchHref   string
	StoragePath  string
	MasteryScore *float64
	// Manifest is the raw parsed-manifest JSON (internal/scorm.Package). Stored
	// and served verbatim; the handler marshals/unmarshals against scorm.Package.
	Manifest    json.RawMessage
	IsPublished bool
	CreatedBy   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ScormPackageRepo struct{}

func NewScormPackageRepo() *ScormPackageRepo { return &ScormPackageRepo{} }

const scormPackageColumns = `id, org_id, course_id, lesson_id, slug, title, description,
	version, identifier, launch_href, storage_path, mastery_score, manifest,
	is_published, created_by, created_at, updated_at`

func scanScormPackage(row pgx.Row) (*ScormPackage, error) {
	var p ScormPackage
	if err := row.Scan(&p.ID, &p.OrgID, &p.CourseID, &p.LessonID, &p.Slug, &p.Title, &p.Description,
		&p.Version, &p.Identifier, &p.LaunchHref, &p.StoragePath, &p.MasteryScore, &p.Manifest,
		&p.IsPublished, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// Create inserts a new package. courseID/lessonID/createdBy may be "" to store
// NULL. A nil Manifest is stored as an empty JSON object.
func (r *ScormPackageRepo) Create(ctx context.Context, q Querier, p ScormPackage) (*ScormPackage, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO scorm_packages
			(org_id, course_id, lesson_id, slug, title, description, version,
			 identifier, launch_href, storage_path, mastery_score, manifest,
			 is_published, created_by)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7,
			$8, $9, $10, $11, COALESCE($12, '{}')::jsonb, $13, NULLIF($14, '')::uuid)
		RETURNING `+scormPackageColumns,
		p.OrgID, strOrEmpty(p.CourseID), strOrEmpty(p.LessonID), p.Slug, p.Title, p.Description, p.Version,
		p.Identifier, p.LaunchHref, p.StoragePath, p.MasteryScore, jsonOrNil(p.Manifest),
		p.IsPublished, strOrEmpty(p.CreatedBy))
	out, err := scanScormPackage(row)
	if err != nil {
		return nil, fmt.Errorf("models: create scorm package: %w", err)
	}
	return out, nil
}

func (r *ScormPackageRepo) Get(ctx context.Context, q Querier, id string) (*ScormPackage, error) {
	row := q.QueryRow(ctx, `SELECT `+scormPackageColumns+` FROM scorm_packages WHERE id = $1`, id)
	p, err := scanScormPackage(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get scorm package: %w", err)
	}
	return p, nil
}

func (r *ScormPackageRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*ScormPackage, error) {
	rows, err := q.Query(ctx, `SELECT `+scormPackageColumns+`
		FROM scorm_packages WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list scorm packages: %w", err)
	}
	defer rows.Close()
	return collectScormPackages(rows)
}

func collectScormPackages(rows pgx.Rows) ([]*ScormPackage, error) {
	var out []*ScormPackage
	for rows.Next() {
		p, err := scanScormPackage(rows)
		if err != nil {
			return nil, fmt.Errorf("models: scan scorm package: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate scorm packages: %w", err)
	}
	return out, nil
}

// Update overwrites the editable metadata of a package (not its immutable
// version/manifest/launch identity, which come from the imported file and are
// only replaced by re-importing).
func (r *ScormPackageRepo) Update(ctx context.Context, q Querier, p ScormPackage) (*ScormPackage, error) {
	row := q.QueryRow(ctx, `
		UPDATE scorm_packages
		SET course_id = NULLIF($2, '')::uuid, lesson_id = NULLIF($3, '')::uuid,
		    title = $4, description = $5, mastery_score = $6, is_published = $7,
		    updated_at = now()
		WHERE id = $1
		RETURNING `+scormPackageColumns,
		p.ID, strOrEmpty(p.CourseID), strOrEmpty(p.LessonID), p.Title, p.Description,
		p.MasteryScore, p.IsPublished)
	out, err := scanScormPackage(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update scorm package: %w", err)
	}
	return out, nil
}

// SetPublished flips just the is_published flag of a package.
func (r *ScormPackageRepo) SetPublished(ctx context.Context, q Querier, id string, published bool) (*ScormPackage, error) {
	row := q.QueryRow(ctx, `
		UPDATE scorm_packages SET is_published = $2, updated_at = now()
		WHERE id = $1 RETURNING `+scormPackageColumns, id, published)
	out, err := scanScormPackage(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: set scorm package published: %w", err)
	}
	return out, nil
}

func (r *ScormPackageRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM scorm_packages WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete scorm package: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// jsonOrNil returns nil for an empty raw message so the SQL COALESCE stores an
// empty object rather than an invalid empty string.
func jsonOrNil(m json.RawMessage) []byte {
	if len(m) == 0 {
		return nil
	}
	return m
}
