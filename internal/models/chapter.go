package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Chapter struct {
	ID        string
	CourseID  string
	OrgID     string
	Title     string
	SortOrder float64
	CreatedBy string
	UpdatedAt time.Time
}

type ChapterRepo struct{}

func NewChapterRepo() *ChapterRepo { return &ChapterRepo{} }

const chapterColumns = `id, course_id, org_id, title, sort_order, created_by, updated_at`

func (r *ChapterRepo) Create(ctx context.Context, q Querier, courseID, orgID, createdBy, title string, sortOrder float64) (*Chapter, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO chapters (course_id, org_id, title, sort_order, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+chapterColumns, courseID, orgID, title, sortOrder, createdBy)
	return scanChapter(row)
}

func (r *ChapterRepo) Get(ctx context.Context, q Querier, id string) (*Chapter, error) {
	row := q.QueryRow(ctx, `SELECT `+chapterColumns+` FROM chapters WHERE id = $1`, id)
	return scanChapter(row)
}

func (r *ChapterRepo) ListByCourse(ctx context.Context, q Querier, courseID string) ([]*Chapter, error) {
	rows, err := q.Query(ctx, `SELECT `+chapterColumns+` FROM chapters WHERE course_id = $1 ORDER BY sort_order`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list chapters: %w", err)
	}
	defer rows.Close()

	var out []*Chapter
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.ID, &c.CourseID, &c.OrgID, &c.Title, &c.SortOrder, &c.CreatedBy, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("models: scan chapter: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *ChapterRepo) Update(ctx context.Context, q Querier, id, title string, sortOrder *float64) (*Chapter, error) {
	var row pgx.Row
	if sortOrder != nil {
		row = q.QueryRow(ctx, `
			UPDATE chapters SET title = $2, sort_order = $3, updated_at = now()
			WHERE id = $1 RETURNING `+chapterColumns, id, title, *sortOrder)
	} else {
		row = q.QueryRow(ctx, `
			UPDATE chapters SET title = $2, updated_at = now()
			WHERE id = $1 RETURNING `+chapterColumns, id, title)
	}
	return scanChapter(row)
}

func (r *ChapterRepo) SetSortOrder(ctx context.Context, q Querier, id string, sortOrder float64) error {
	tag, err := q.Exec(ctx, `UPDATE chapters SET sort_order = $2, updated_at = now() WHERE id = $1`, id, sortOrder)
	if err != nil {
		return fmt.Errorf("models: set chapter sort_order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete rejects (ErrHasChildren) if the chapter still has lessons — no
// cascade, per spec: the teacher must delete/move lessons first.
func (r *ChapterRepo) Delete(ctx context.Context, q Querier, id string) error {
	var lessonCount int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM lessons WHERE chapter_id = $1`, id).Scan(&lessonCount); err != nil {
		return fmt.Errorf("models: count lessons for chapter delete: %w", err)
	}
	if lessonCount > 0 {
		return ErrHasChildren{Count: lessonCount}
	}

	tag, err := q.Exec(ctx, `DELETE FROM chapters WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete chapter: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanChapter(row pgx.Row) (*Chapter, error) {
	var c Chapter
	if err := row.Scan(&c.ID, &c.CourseID, &c.OrgID, &c.Title, &c.SortOrder, &c.CreatedBy, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan chapter: %w", err)
	}
	return &c, nil
}
