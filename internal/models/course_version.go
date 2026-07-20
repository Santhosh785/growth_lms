package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CourseSnapshot is the full nested JSON shape stored in
// course_versions.snapshot: chapters with their lessons with their
// blocks, at the moment of publish (or restore). Self-contained so
// diffing/restoration never needs to re-query the live tables.
type CourseSnapshot struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Chapters    []SnapshotChapter `json:"chapters"`
}

type SnapshotChapter struct {
	Title     string           `json:"title"`
	SortOrder float64          `json:"sort_order"`
	Lessons   []SnapshotLesson `json:"lessons"`
}

type SnapshotLesson struct {
	Title     string          `json:"title"`
	SortOrder float64         `json:"sort_order"`
	Blocks    []SnapshotBlock `json:"blocks"`
}

type SnapshotBlock struct {
	Type      string          `json:"type"`
	Content   json.RawMessage `json:"content"`
	SortOrder float64         `json:"sort_order"`
}

type CourseVersion struct {
	ID            string
	CourseID      string
	OrgID         string
	VersionNumber int
	Snapshot      CourseSnapshot
	CreatedBy     string
	CreatedAt     time.Time
}

type CourseVersionRepo struct{}

func NewCourseVersionRepo() *CourseVersionRepo { return &CourseVersionRepo{} }

// Snapshot builds a CourseSnapshot from the course's current live state
// and inserts a new course_versions row — "publish always snapshots", so
// this is called unconditionally on every transition into 'published',
// never conditionally on whether content changed.
func (r *CourseVersionRepo) Snapshot(ctx context.Context, q Querier, courseID, orgID, createdBy string) (*CourseVersion, error) {
	course, err := (&CourseRepo{}).Get(ctx, q, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: snapshot: get course: %w", err)
	}

	chapters, err := (&ChapterRepo{}).ListByCourse(ctx, q, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: snapshot: list chapters: %w", err)
	}

	snapshot := CourseSnapshot{Title: course.Title, Description: course.Description}
	for _, ch := range chapters {
		lessons, err := (&LessonRepo{}).ListByChapter(ctx, q, ch.ID)
		if err != nil {
			return nil, fmt.Errorf("models: snapshot: list lessons: %w", err)
		}
		snapChapter := SnapshotChapter{Title: ch.Title, SortOrder: ch.SortOrder}
		for _, lsn := range lessons {
			blocks, err := (&BlockRepo{}).ListByLesson(ctx, q, lsn.ID)
			if err != nil {
				return nil, fmt.Errorf("models: snapshot: list blocks: %w", err)
			}
			snapLesson := SnapshotLesson{Title: lsn.Title, SortOrder: lsn.SortOrder}
			for _, b := range blocks {
				snapLesson.Blocks = append(snapLesson.Blocks, SnapshotBlock{Type: b.Type, Content: b.Content, SortOrder: b.SortOrder})
			}
			snapChapter.Lessons = append(snapChapter.Lessons, snapLesson)
		}
		snapshot.Chapters = append(snapshot.Chapters, snapChapter)
	}

	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("models: snapshot: marshal: %w", err)
	}

	var nextVersion int
	if err := q.QueryRow(ctx, `SELECT COALESCE(MAX(version_number), 0) + 1 FROM course_versions WHERE course_id = $1`, courseID).Scan(&nextVersion); err != nil {
		return nil, fmt.Errorf("models: snapshot: next version number: %w", err)
	}

	row := q.QueryRow(ctx, `
		INSERT INTO course_versions (course_id, org_id, version_number, snapshot, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, course_id, org_id, version_number, snapshot, created_by, created_at
	`, courseID, orgID, nextVersion, snapshotJSON, createdBy)
	return scanCourseVersion(row)
}

func (r *CourseVersionRepo) List(ctx context.Context, q Querier, courseID string) ([]*CourseVersion, error) {
	rows, err := q.Query(ctx, `
		SELECT id, course_id, org_id, version_number, snapshot, created_by, created_at
		FROM course_versions WHERE course_id = $1 ORDER BY version_number DESC
	`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list course versions: %w", err)
	}
	defer rows.Close()

	var out []*CourseVersion
	for rows.Next() {
		var cv CourseVersion
		var snapshotJSON []byte
		if err := rows.Scan(&cv.ID, &cv.CourseID, &cv.OrgID, &cv.VersionNumber, &snapshotJSON, &cv.CreatedBy, &cv.CreatedAt); err != nil {
			return nil, fmt.Errorf("models: scan course version: %w", err)
		}
		if err := json.Unmarshal(snapshotJSON, &cv.Snapshot); err != nil {
			return nil, fmt.Errorf("models: unmarshal snapshot: %w", err)
		}
		out = append(out, &cv)
	}
	return out, rows.Err()
}

func (r *CourseVersionRepo) Get(ctx context.Context, q Querier, id string) (*CourseVersion, error) {
	row := q.QueryRow(ctx, `
		SELECT id, course_id, org_id, version_number, snapshot, created_by, created_at
		FROM course_versions WHERE id = $1
	`, id)
	return scanCourseVersion(row)
}

// Restore rebuilds the course's live chapters/lessons/blocks from a prior
// version's snapshot, deleting the current live content and replacing it
// (existing chapter/lesson/block IDs are not preserved — restoration
// creates fresh rows), then takes a NEW snapshot of the restored state.
// This is "undo via new version": course_versions history is never
// deleted or overwritten.
func (r *CourseVersionRepo) Restore(ctx context.Context, q Querier, courseID, versionID, restoredBy string) (*CourseVersion, error) {
	version, err := r.Get(ctx, q, versionID)
	if err != nil {
		return nil, err
	}
	if version.CourseID != courseID {
		return nil, ErrNotFound
	}

	course, err := (&CourseRepo{}).Get(ctx, q, courseID)
	if err != nil {
		return nil, err
	}

	if _, err := q.Exec(ctx, `DELETE FROM chapters WHERE course_id = $1`, courseID); err != nil {
		return nil, fmt.Errorf("models: restore: clear chapters: %w", err)
	}

	for _, ch := range version.Snapshot.Chapters {
		var chapterID string
		if err := q.QueryRow(ctx, `
			INSERT INTO chapters (course_id, org_id, title, sort_order, created_by)
			VALUES ($1, $2, $3, $4, $5) RETURNING id
		`, courseID, course.OrgID, ch.Title, ch.SortOrder, restoredBy).Scan(&chapterID); err != nil {
			return nil, fmt.Errorf("models: restore: insert chapter: %w", err)
		}

		for _, lsn := range ch.Lessons {
			var lessonID string
			if err := q.QueryRow(ctx, `
				INSERT INTO lessons (chapter_id, course_id, org_id, title, sort_order, created_by)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
			`, chapterID, courseID, course.OrgID, lsn.Title, lsn.SortOrder, restoredBy).Scan(&lessonID); err != nil {
				return nil, fmt.Errorf("models: restore: insert lesson: %w", err)
			}

			for _, b := range lsn.Blocks {
				if _, err := q.Exec(ctx, `
					INSERT INTO blocks (lesson_id, course_id, org_id, type, content, sort_order, created_by)
					VALUES ($1, $2, $3, $4, $5, $6, $7)
				`, lessonID, courseID, course.OrgID, b.Type, b.Content, b.SortOrder, restoredBy); err != nil {
					return nil, fmt.Errorf("models: restore: insert block: %w", err)
				}
			}
		}
	}

	if _, err := q.Exec(ctx, `UPDATE courses SET title = $2, description = $3, updated_at = now() WHERE id = $1`,
		courseID, version.Snapshot.Title, version.Snapshot.Description); err != nil {
		return nil, fmt.Errorf("models: restore: update course metadata: %w", err)
	}

	return r.Snapshot(ctx, q, courseID, course.OrgID, restoredBy)
}

func scanCourseVersion(row pgx.Row) (*CourseVersion, error) {
	var cv CourseVersion
	var snapshotJSON []byte
	if err := row.Scan(&cv.ID, &cv.CourseID, &cv.OrgID, &cv.VersionNumber, &snapshotJSON, &cv.CreatedBy, &cv.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan course version: %w", err)
	}
	if err := json.Unmarshal(snapshotJSON, &cv.Snapshot); err != nil {
		return nil, fmt.Errorf("models: unmarshal snapshot: %w", err)
	}
	return &cv, nil
}
