package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Collection struct {
	ID          string
	OrgID       string
	Name        string
	Description string
	CreatedBy   string
	UpdatedAt   time.Time
}

type CollectionRepo struct{}

func NewCollectionRepo() *CollectionRepo { return &CollectionRepo{} }

const collectionColumns = `id, org_id, name, description, created_by, updated_at`

func (r *CollectionRepo) Create(ctx context.Context, q Querier, orgID, createdBy, name, description string) (*Collection, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO collections (org_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING `+collectionColumns, orgID, name, description, createdBy)
	return scanCollection(row)
}

func (r *CollectionRepo) Get(ctx context.Context, q Querier, id string) (*Collection, error) {
	row := q.QueryRow(ctx, `SELECT `+collectionColumns+` FROM collections WHERE id = $1`, id)
	return scanCollection(row)
}

func (r *CollectionRepo) List(ctx context.Context, q Querier, orgID string) ([]*Collection, error) {
	rows, err := q.Query(ctx, `SELECT `+collectionColumns+` FROM collections WHERE org_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list collections: %w", err)
	}
	defer rows.Close()

	var out []*Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Name, &c.Description, &c.CreatedBy, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("models: scan collection: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *CollectionRepo) Update(ctx context.Context, q Querier, id, name, description string) (*Collection, error) {
	row := q.QueryRow(ctx, `
		UPDATE collections SET name = $2, description = $3, updated_at = now()
		WHERE id = $1 RETURNING `+collectionColumns, id, name, description)
	return scanCollection(row)
}

func (r *CollectionRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM collections WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete collection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type CollectionCourse struct {
	CollectionID string
	CourseID     string
	OrgID        string
	SortOrder    float64
}

func (r *CollectionRepo) AddCourse(ctx context.Context, q Querier, collectionID, courseID, orgID string, sortOrder float64) error {
	_, err := q.Exec(ctx, `
		INSERT INTO collection_courses (collection_id, course_id, org_id, sort_order) VALUES ($1, $2, $3, $4)
		ON CONFLICT (collection_id, course_id) DO UPDATE SET sort_order = EXCLUDED.sort_order
	`, collectionID, courseID, orgID, sortOrder)
	if err != nil {
		return fmt.Errorf("models: add course to collection: %w", err)
	}
	return nil
}

func (r *CollectionRepo) RemoveCourse(ctx context.Context, q Querier, collectionID, courseID string) error {
	_, err := q.Exec(ctx, `DELETE FROM collection_courses WHERE collection_id = $1 AND course_id = $2`, collectionID, courseID)
	if err != nil {
		return fmt.Errorf("models: remove course from collection: %w", err)
	}
	return nil
}

func (r *CollectionRepo) ListCourses(ctx context.Context, q Querier, collectionID string) ([]*CollectionCourse, error) {
	rows, err := q.Query(ctx, `
		SELECT collection_id, course_id, org_id, sort_order FROM collection_courses
		WHERE collection_id = $1 ORDER BY sort_order
	`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("models: list collection courses: %w", err)
	}
	defer rows.Close()

	var out []*CollectionCourse
	for rows.Next() {
		var cc CollectionCourse
		if err := rows.Scan(&cc.CollectionID, &cc.CourseID, &cc.OrgID, &cc.SortOrder); err != nil {
			return nil, fmt.Errorf("models: scan collection course: %w", err)
		}
		out = append(out, &cc)
	}
	return out, rows.Err()
}

func (r *CollectionRepo) SetCourseSortOrder(ctx context.Context, q Querier, collectionID, courseID string, sortOrder float64) error {
	tag, err := q.Exec(ctx, `
		UPDATE collection_courses SET sort_order = $3 WHERE collection_id = $1 AND course_id = $2
	`, collectionID, courseID, sortOrder)
	if err != nil {
		return fmt.Errorf("models: set collection course sort_order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanCollection(row pgx.Row) (*Collection, error) {
	var c Collection
	if err := row.Scan(&c.ID, &c.OrgID, &c.Name, &c.Description, &c.CreatedBy, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: scan collection: %w", err)
	}
	return &c, nil
}
