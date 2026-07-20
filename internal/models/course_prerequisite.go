package models

import (
	"context"
	"fmt"
)

type CoursePrerequisiteRepo struct{}

func NewCoursePrerequisiteRepo() *CoursePrerequisiteRepo { return &CoursePrerequisiteRepo{} }

func (r *CoursePrerequisiteRepo) Add(ctx context.Context, q Querier, orgID, courseID, prerequisiteCourseID string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO course_prerequisites (course_id, prerequisite_course_id, org_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, courseID, prerequisiteCourseID, orgID)
	if err != nil {
		return fmt.Errorf("models: add course prerequisite: %w", err)
	}
	return nil
}

func (r *CoursePrerequisiteRepo) Remove(ctx context.Context, q Querier, courseID, prerequisiteCourseID string) error {
	_, err := q.Exec(ctx, `DELETE FROM course_prerequisites WHERE course_id = $1 AND prerequisite_course_id = $2`, courseID, prerequisiteCourseID)
	if err != nil {
		return fmt.Errorf("models: remove course prerequisite: %w", err)
	}
	return nil
}

func (r *CoursePrerequisiteRepo) ListForCourse(ctx context.Context, q Querier, courseID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT prerequisite_course_id FROM course_prerequisites WHERE course_id = $1`, courseID)
	if err != nil {
		return nil, fmt.Errorf("models: list course prerequisites: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("models: scan course prerequisite: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
