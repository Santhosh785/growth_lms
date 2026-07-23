package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Course statuses, matching the CHECK constraint in
// db/migrations/000003_course_domain.up.sql. The valid-transition state
// machine itself lives in the courses handler (internal/httpserver/
// handlers/courses.go), not here — this repo only performs whatever
// UPDATE the handler has already decided is valid.
const (
	CourseStatusDraft       = "draft"
	CourseStatusReview      = "review"
	CourseStatusScheduled   = "scheduled"
	CourseStatusPublished   = "published"
	CourseStatusUnpublished = "unpublished"
	CourseStatusArchived    = "archived"
)

type Course struct {
	ID            string
	OrgID         string
	Title         string
	Description   string
	CoverImageURL *string
	CategoryID    *string
	Status        string
	PublishDate   *time.Time
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	PublishedAt   *time.Time
	ArchivedAt    *time.Time
}

// ErrHasChildren is returned by chapter/lesson delete when child rows
// still exist — the spec requires a 409 with the child count, not a
// cascade and not a silent no-op.
type ErrHasChildren struct {
	Count int
}

func (e ErrHasChildren) Error() string {
	return fmt.Sprintf("models: %d child row(s) still exist", e.Count)
}

type CourseRepo struct{}

func NewCourseRepo() *CourseRepo { return &CourseRepo{} }

const courseColumns = `id, org_id, title, description, cover_image_url, category_id, status, publish_date, created_by, created_at, updated_at, published_at, archived_at`

func (r *CourseRepo) Create(ctx context.Context, q Querier, orgID, createdBy, title, description string, categoryID *string) (*Course, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO courses (org_id, title, description, category_id, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+courseColumns, orgID, title, description, categoryID, createdBy)
	return scanCourse(row)
}

func (r *CourseRepo) Get(ctx context.Context, q Querier, id string) (*Course, error) {
	row := q.QueryRow(ctx, `SELECT `+courseColumns+` FROM courses WHERE id = $1`, id)
	return scanCourse(row)
}

// PublishedCourse is the public-facing projection of a course, returned
// by ListPublished for anonymous consumers (sitemap, embeddable catalog,
// public org landing page) that have no org membership to satisfy
// courses_select's RLS policy.
type PublishedCourse struct {
	ID            string
	Title         string
	Description   string
	CoverImageURL *string
	PublishedAt   *time.Time
}

// ListPublished calls the list_published_courses() SECURITY DEFINER SQL
// function (migration 000009) rather than a plain SELECT, since the
// caller here has no app.current_user_id/app.current_org_id session
// context at all.
func (r *CourseRepo) ListPublished(ctx context.Context, q Querier, orgID string) ([]PublishedCourse, error) {
	rows, err := q.Query(ctx, `SELECT id, title, description, cover_image_url, published_at FROM list_published_courses($1)`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list published courses: %w", err)
	}
	defer rows.Close()

	var out []PublishedCourse
	for rows.Next() {
		var c PublishedCourse
		if err := rows.Scan(&c.ID, &c.Title, &c.Description, &c.CoverImageURL, &c.PublishedAt); err != nil {
			return nil, fmt.Errorf("models: scan published course: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list published courses: %w", err)
	}
	return out, nil
}

func (r *CourseRepo) List(ctx context.Context, q Querier, orgID string) ([]*Course, error) {
	rows, err := q.Query(ctx, `SELECT `+courseColumns+` FROM courses WHERE org_id = $1 ORDER BY updated_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list courses: %w", err)
	}
	defer rows.Close()

	var out []*Course
	for rows.Next() {
		c, err := scanCourseRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountByOrg returns the number of courses in an org — added by Task 9
// (admin-dashboard) for the platform-owner cross-org list, preferred over
// len(List(...)) so the platform-wide dashboard doesn't have to load
// every org's full course rows just to count them.
//
// NOTE (RLS gap, flagged per task-9-admin-dashboard.md rather than
// silently worked around): courses_select
// (db/migrations/000003_course_domain.up.sql) is `USING
// (is_org_member(courses.org_id))` with NO `OR app_is_platform_owner()`
// clause, unlike organizations_select/memberships_select. A platform
// owner who is not a member of the org being viewed will see zero rows
// here even though middleware.RequirePlatformOwner authorized the
// request — courses_select needs the same platform-owner bypass
// organizations_select already has before this method (and the
// platform-owner cross-org dashboard's course counts) is fully correct.
func (r *CourseRepo) CountByOrg(ctx context.Context, q Querier, orgID string) (int, error) {
	var count int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM courses WHERE org_id = $1`, orgID).Scan(&count); err != nil {
		return 0, fmt.Errorf("models: count courses by org: %w", err)
	}
	return count, nil
}

// UpdateMetadata updates the caller-editable fields only; status and its
// timestamps are handled separately by the status-transition methods
// below so the two concerns can't accidentally interfere with each other.
func (r *CourseRepo) UpdateMetadata(ctx context.Context, q Querier, id, title, description string, categoryID, coverImageURL *string) (*Course, error) {
	row := q.QueryRow(ctx, `
		UPDATE courses SET title = $2, description = $3, category_id = $4, cover_image_url = $5, updated_at = now()
		WHERE id = $1
		RETURNING `+courseColumns, id, title, description, categoryID, coverImageURL)
	return scanCourse(row)
}

// SetStatus performs a plain status transition with no side effects
// (no publish_at/snapshot) — used for draft<->review<->scheduled/
// unpublished/archived transitions that don't publish.
func (r *CourseRepo) SetStatus(ctx context.Context, q Querier, id, status string) (*Course, error) {
	row := q.QueryRow(ctx, `
		UPDATE courses SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+courseColumns, id, status)
	return scanCourse(row)
}

// SetScheduled transitions review -> scheduled, recording the future
// publish_date the worker's sweep will act on.
func (r *CourseRepo) SetScheduled(ctx context.Context, q Querier, id string, publishDate time.Time) (*Course, error) {
	row := q.QueryRow(ctx, `
		UPDATE courses SET status = 'scheduled', publish_date = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+courseColumns, id, publishDate)
	return scanCourse(row)
}

// Publish transitions the course to published, setting published_at.
// Callers (handler or worker sweep) are responsible for creating the
// course_versions snapshot in the SAME transaction — see
// CourseVersionRepo.Snapshot — since "publish always snapshots"
// unconditionally.
func (r *CourseRepo) Publish(ctx context.Context, q Querier, id string) (*Course, error) {
	row := q.QueryRow(ctx, `
		UPDATE courses SET status = 'published', published_at = now(), publish_date = NULL, updated_at = now()
		WHERE id = $1
		RETURNING `+courseColumns, id)
	return scanCourse(row)
}

func (r *CourseRepo) Archive(ctx context.Context, q Querier, id string) (*Course, error) {
	row := q.QueryRow(ctx, `
		UPDATE courses SET status = 'archived', archived_at = now(), updated_at = now()
		WHERE id = $1
		RETURNING `+courseColumns, id)
	return scanCourse(row)
}

// Delete hard-deletes a course. The caller (handler) must verify
// status = 'draft' first — this repo method doesn't re-check, matching
// the spec's rule that non-draft courses must be archived instead.
func (r *CourseRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM courses WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete course: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Duplicate deep-copies a course's chapters/lessons/blocks with new IDs
// into a brand-new draft course. Block content referencing an asset_id
// keeps the SAME asset_id (assets are never duplicated, per the spec's
// storage-bloat note) — only the JSONB content blob is copied verbatim.
func (r *CourseRepo) Duplicate(ctx context.Context, q Querier, sourceCourseID, createdBy string) (*Course, error) {
	source, err := r.Get(ctx, q, sourceCourseID)
	if err != nil {
		return nil, err
	}

	newCourseRow := q.QueryRow(ctx, `
		INSERT INTO courses (org_id, title, description, cover_image_url, category_id, created_by, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'draft')
		RETURNING `+courseColumns,
		source.OrgID, source.Title+" (copy)", source.Description, source.CoverImageURL, source.CategoryID, createdBy)
	newCourse, err := scanCourse(newCourseRow)
	if err != nil {
		return nil, fmt.Errorf("models: duplicate course: create copy: %w", err)
	}

	chapterRows, err := q.Query(ctx, `SELECT id, title, sort_order FROM chapters WHERE course_id = $1 ORDER BY sort_order`, sourceCourseID)
	if err != nil {
		return nil, fmt.Errorf("models: duplicate course: list chapters: %w", err)
	}
	type chapterRow struct {
		id, title string
		sortOrder float64
	}
	var chapters []chapterRow
	for chapterRows.Next() {
		var cr chapterRow
		if err := chapterRows.Scan(&cr.id, &cr.title, &cr.sortOrder); err != nil {
			chapterRows.Close()
			return nil, fmt.Errorf("models: duplicate course: scan chapter: %w", err)
		}
		chapters = append(chapters, cr)
	}
	chapterRows.Close()
	if err := chapterRows.Err(); err != nil {
		return nil, err
	}

	for _, ch := range chapters {
		var newChapterID string
		if err := q.QueryRow(ctx, `
			INSERT INTO chapters (course_id, org_id, title, sort_order, created_by)
			VALUES ($1, $2, $3, $4, $5) RETURNING id
		`, newCourse.ID, newCourse.OrgID, ch.title, ch.sortOrder, createdBy).Scan(&newChapterID); err != nil {
			return nil, fmt.Errorf("models: duplicate course: insert chapter: %w", err)
		}

		lessonRows, err := q.Query(ctx, `SELECT id, title, sort_order FROM lessons WHERE chapter_id = $1 ORDER BY sort_order`, ch.id)
		if err != nil {
			return nil, fmt.Errorf("models: duplicate course: list lessons: %w", err)
		}
		type lessonRow struct {
			id, title string
			sortOrder float64
		}
		var lessons []lessonRow
		for lessonRows.Next() {
			var lr lessonRow
			if err := lessonRows.Scan(&lr.id, &lr.title, &lr.sortOrder); err != nil {
				lessonRows.Close()
				return nil, fmt.Errorf("models: duplicate course: scan lesson: %w", err)
			}
			lessons = append(lessons, lr)
		}
		lessonRows.Close()
		if err := lessonRows.Err(); err != nil {
			return nil, err
		}

		for _, lsn := range lessons {
			var newLessonID string
			if err := q.QueryRow(ctx, `
				INSERT INTO lessons (chapter_id, course_id, org_id, title, sort_order, created_by)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
			`, newChapterID, newCourse.ID, newCourse.OrgID, lsn.title, lsn.sortOrder, createdBy).Scan(&newLessonID); err != nil {
				return nil, fmt.Errorf("models: duplicate course: insert lesson: %w", err)
			}

			_, err := q.Exec(ctx, `
				INSERT INTO blocks (lesson_id, course_id, org_id, type, content, sort_order, created_by)
				SELECT $1, $2, $3, type, content, sort_order, $4 FROM blocks WHERE lesson_id = $5
			`, newLessonID, newCourse.ID, newCourse.OrgID, createdBy, lsn.id)
			if err != nil {
				return nil, fmt.Errorf("models: duplicate course: insert blocks: %w", err)
			}
		}
	}

	return newCourse, nil
}

func scanCourse(row pgx.Row) (*Course, error) {
	var c Course
	if err := row.Scan(&c.ID, &c.OrgID, &c.Title, &c.Description, &c.CoverImageURL, &c.CategoryID, &c.Status,
		&c.PublishDate, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt, &c.PublishedAt, &c.ArchivedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan course: %w", err)
	}
	return &c, nil
}

func scanCourseRows(rows pgx.Rows) (*Course, error) {
	var c Course
	if err := rows.Scan(&c.ID, &c.OrgID, &c.Title, &c.Description, &c.CoverImageURL, &c.CategoryID, &c.Status,
		&c.PublishDate, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt, &c.PublishedAt, &c.ArchivedAt); err != nil {
		return nil, fmt.Errorf("models: scan course: %w", err)
	}
	return &c, nil
}
