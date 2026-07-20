package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Lesson struct {
	ID                    string
	ChapterID             string
	CourseID              string
	OrgID                 string
	Title                 string
	SortOrder             float64
	CreatedBy             string
	UpdatedAt             time.Time
	WatchThresholdPercent *int
}

type LessonRepo struct{}

func NewLessonRepo() *LessonRepo { return &LessonRepo{} }

const lessonColumns = `id, chapter_id, course_id, org_id, title, sort_order, created_by, updated_at, watch_threshold_percent`

func (r *LessonRepo) Create(ctx context.Context, q Querier, chapterID, courseID, orgID, createdBy, title string, sortOrder float64) (*Lesson, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO lessons (chapter_id, course_id, org_id, title, sort_order, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+lessonColumns, chapterID, courseID, orgID, title, sortOrder, createdBy)
	return scanLesson(row)
}

func (r *LessonRepo) Get(ctx context.Context, q Querier, id string) (*Lesson, error) {
	row := q.QueryRow(ctx, `SELECT `+lessonColumns+` FROM lessons WHERE id = $1`, id)
	return scanLesson(row)
}

// SetWatchThresholdPercent sets or clears (nil) the video watch-completion
// threshold for a lesson. NULL falls back to a hardcoded 80% default
// applied in Go (see grilling-record.md Q3), not here — this repo just
// stores whatever the caller decided.
func (r *LessonRepo) SetWatchThresholdPercent(ctx context.Context, q Querier, id string, watchThresholdPercent *int) (*Lesson, error) {
	row := q.QueryRow(ctx, `
		UPDATE lessons SET watch_threshold_percent = $2, updated_at = now()
		WHERE id = $1 RETURNING `+lessonColumns, id, watchThresholdPercent)
	return scanLesson(row)
}

func (r *LessonRepo) ListByChapter(ctx context.Context, q Querier, chapterID string) ([]*Lesson, error) {
	rows, err := q.Query(ctx, `SELECT `+lessonColumns+` FROM lessons WHERE chapter_id = $1 ORDER BY sort_order`, chapterID)
	if err != nil {
		return nil, fmt.Errorf("models: list lessons: %w", err)
	}
	defer rows.Close()

	var out []*Lesson
	for rows.Next() {
		l, err := scanLessonRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *LessonRepo) Update(ctx context.Context, q Querier, id, title string, sortOrder *float64) (*Lesson, error) {
	var row pgx.Row
	if sortOrder != nil {
		row = q.QueryRow(ctx, `
			UPDATE lessons SET title = $2, sort_order = $3, updated_at = now()
			WHERE id = $1 RETURNING `+lessonColumns, id, title, *sortOrder)
	} else {
		row = q.QueryRow(ctx, `
			UPDATE lessons SET title = $2, updated_at = now()
			WHERE id = $1 RETURNING `+lessonColumns, id, title)
	}
	return scanLesson(row)
}

func (r *LessonRepo) SetSortOrder(ctx context.Context, q Querier, id string, sortOrder float64) error {
	tag, err := q.Exec(ctx, `UPDATE lessons SET sort_order = $2, updated_at = now() WHERE id = $1`, id, sortOrder)
	if err != nil {
		return fmt.Errorf("models: set lesson sort_order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete rejects (ErrHasChildren) if the lesson still has blocks — no
// cascade, per spec: the teacher must delete the blocks first.
func (r *LessonRepo) Delete(ctx context.Context, q Querier, id string) error {
	var blockCount int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM blocks WHERE lesson_id = $1`, id).Scan(&blockCount); err != nil {
		return fmt.Errorf("models: count blocks for lesson delete: %w", err)
	}
	if blockCount > 0 {
		return ErrHasChildren{Count: blockCount}
	}

	tag, err := q.Exec(ctx, `DELETE FROM lessons WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete lesson: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanLesson(row pgx.Row) (*Lesson, error) {
	var l Lesson
	if err := row.Scan(&l.ID, &l.ChapterID, &l.CourseID, &l.OrgID, &l.Title, &l.SortOrder, &l.CreatedBy, &l.UpdatedAt, &l.WatchThresholdPercent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan lesson: %w", err)
	}
	return &l, nil
}

func scanLessonRows(rows pgx.Rows) (*Lesson, error) {
	var l Lesson
	if err := rows.Scan(&l.ID, &l.ChapterID, &l.CourseID, &l.OrgID, &l.Title, &l.SortOrder, &l.CreatedBy, &l.UpdatedAt, &l.WatchThresholdPercent); err != nil {
		return nil, fmt.Errorf("models: scan lesson: %w", err)
	}
	return &l, nil
}
