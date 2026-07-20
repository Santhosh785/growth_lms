package models

import (
	"context"
	"fmt"
)

// Tag is a freeform, get-or-create label available to teacher/owner
// (unlike Category's curated, owner-only taxonomy).
type Tag struct {
	ID    string
	OrgID string
	Name  string
	Slug  string
}

type TagRepo struct{}

func NewTagRepo() *TagRepo { return &TagRepo{} }

// GetOrCreate creates the tag row if it doesn't already exist in this org
// (matched by slug), or returns the existing one — tagging a course never
// fails because "the tag doesn't exist yet".
func (r *TagRepo) GetOrCreate(ctx context.Context, q Querier, orgID, name, slug string) (*Tag, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO tags (org_id, name, slug)
		VALUES ($1, $2, $3)
		ON CONFLICT (org_id, slug) DO UPDATE SET name = tags.name
		RETURNING id, org_id, name, slug
	`, orgID, name, slug)

	var t Tag
	if err := row.Scan(&t.ID, &t.OrgID, &t.Name, &t.Slug); err != nil {
		return nil, fmt.Errorf("models: get-or-create tag: %w", err)
	}
	return &t, nil
}

func (r *TagRepo) List(ctx context.Context, q Querier, orgID string) ([]*Tag, error) {
	rows, err := q.Query(ctx, `SELECT id, org_id, name, slug FROM tags WHERE org_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list tags: %w", err)
	}
	defer rows.Close()

	var out []*Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name, &t.Slug); err != nil {
			return nil, fmt.Errorf("models: scan tag: %w", err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// AttachToCourse creates the course_tags junction row (idempotent: tagging
// a course with the same tag twice is a no-op, not an error).
func (r *TagRepo) AttachToCourse(ctx context.Context, q Querier, orgID, courseID, tagID string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO course_tags (course_id, tag_id, org_id) VALUES ($1, $2, $3)
		ON CONFLICT (course_id, tag_id) DO NOTHING
	`, courseID, tagID, orgID)
	if err != nil {
		return fmt.Errorf("models: attach tag to course: %w", err)
	}
	return nil
}

func (r *TagRepo) DetachFromCourse(ctx context.Context, q Querier, courseID, tagID string) error {
	_, err := q.Exec(ctx, `DELETE FROM course_tags WHERE course_id = $1 AND tag_id = $2`, courseID, tagID)
	if err != nil {
		return fmt.Errorf("models: detach tag from course: %w", err)
	}
	return nil
}

func (r *TagRepo) ListForCourse(ctx context.Context, q Querier, courseID string) ([]*Tag, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, t.org_id, t.name, t.slug
		FROM tags t JOIN course_tags ct ON ct.tag_id = t.id
		WHERE ct.course_id = $1 ORDER BY t.name
	`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list tags for course: %w", err)
	}
	defer rows.Close()

	var out []*Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name, &t.Slug); err != nil {
			return nil, fmt.Errorf("models: scan tag: %w", err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}
