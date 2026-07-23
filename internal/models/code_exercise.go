package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CodeExercise is one teacher-authored coding exercise belonging to an org
// (plan.md Task 9's "sandboxed code execution"). slug is a stable per-org
// identifier, unique per org. course_id/lesson_id optionally tie the exercise
// to a course/lesson. The *Limit fields are the resource ceiling a submission
// runs under; the handler clamps a run to the platform maxima on top of these.
type CodeExercise struct {
	ID                  string
	OrgID               string
	CourseID            *string
	LessonID            *string
	Slug                string
	Title               string
	Description         string
	Language            string
	StarterCode         string
	SolutionCode        string
	Stdin               string
	ExpectedOutput      string
	CPUMillisLimit      int
	MemoryBytesLimit    int64
	WallTimeMillisLimit int
	IsPublished         bool
	CreatedBy           *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CodeExerciseRepo struct{}

func NewCodeExerciseRepo() *CodeExerciseRepo { return &CodeExerciseRepo{} }

const codeExerciseColumns = `id, org_id, course_id, lesson_id, slug, title, description,
	language, starter_code, solution_code, stdin, expected_output,
	cpu_millis_limit, memory_bytes_limit, wall_time_millis_limit,
	is_published, created_by, created_at, updated_at`

func scanCodeExercise(row pgx.Row) (*CodeExercise, error) {
	var e CodeExercise
	if err := row.Scan(&e.ID, &e.OrgID, &e.CourseID, &e.LessonID, &e.Slug, &e.Title, &e.Description,
		&e.Language, &e.StarterCode, &e.SolutionCode, &e.Stdin, &e.ExpectedOutput,
		&e.CPUMillisLimit, &e.MemoryBytesLimit, &e.WallTimeMillisLimit,
		&e.IsPublished, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func scanCodeExerciseRows(rows pgx.Rows) (*CodeExercise, error) {
	var e CodeExercise
	if err := rows.Scan(&e.ID, &e.OrgID, &e.CourseID, &e.LessonID, &e.Slug, &e.Title, &e.Description,
		&e.Language, &e.StarterCode, &e.SolutionCode, &e.Stdin, &e.ExpectedOutput,
		&e.CPUMillisLimit, &e.MemoryBytesLimit, &e.WallTimeMillisLimit,
		&e.IsPublished, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, fmt.Errorf("models: scan code exercise: %w", err)
	}
	return &e, nil
}

// Create inserts a new exercise. courseID/lessonID/createdBy may be "" to
// store NULL.
func (r *CodeExerciseRepo) Create(ctx context.Context, q Querier, e CodeExercise) (*CodeExercise, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO code_exercises
			(org_id, course_id, lesson_id, slug, title, description, language,
			 starter_code, solution_code, stdin, expected_output,
			 cpu_millis_limit, memory_bytes_limit, wall_time_millis_limit,
			 is_published, created_by)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13, $14, $15, NULLIF($16, '')::uuid)
		RETURNING `+codeExerciseColumns,
		e.OrgID, strOrEmpty(e.CourseID), strOrEmpty(e.LessonID), e.Slug, e.Title, e.Description, e.Language,
		e.StarterCode, e.SolutionCode, e.Stdin, e.ExpectedOutput,
		e.CPUMillisLimit, e.MemoryBytesLimit, e.WallTimeMillisLimit,
		e.IsPublished, strOrEmpty(e.CreatedBy))
	out, err := scanCodeExercise(row)
	if err != nil {
		return nil, fmt.Errorf("models: create code exercise: %w", err)
	}
	return out, nil
}

func (r *CodeExerciseRepo) Get(ctx context.Context, q Querier, id string) (*CodeExercise, error) {
	row := q.QueryRow(ctx, `SELECT `+codeExerciseColumns+` FROM code_exercises WHERE id = $1`, id)
	e, err := scanCodeExercise(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get code exercise: %w", err)
	}
	return e, nil
}

func (r *CodeExerciseRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*CodeExercise, error) {
	rows, err := q.Query(ctx, `SELECT `+codeExerciseColumns+`
		FROM code_exercises WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list code exercises: %w", err)
	}
	defer rows.Close()

	var out []*CodeExercise
	for rows.Next() {
		e, err := scanCodeExerciseRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: list code exercises: %w", err)
	}
	return out, nil
}

// Update overwrites the editable fields of an exercise, including is_published.
func (r *CodeExerciseRepo) Update(ctx context.Context, q Querier, e CodeExercise) (*CodeExercise, error) {
	row := q.QueryRow(ctx, `
		UPDATE code_exercises
		SET course_id = NULLIF($2, '')::uuid, lesson_id = NULLIF($3, '')::uuid,
		    title = $4, description = $5, language = $6, starter_code = $7,
		    solution_code = $8, stdin = $9, expected_output = $10,
		    cpu_millis_limit = $11, memory_bytes_limit = $12, wall_time_millis_limit = $13,
		    is_published = $14, updated_at = now()
		WHERE id = $1
		RETURNING `+codeExerciseColumns,
		e.ID, strOrEmpty(e.CourseID), strOrEmpty(e.LessonID), e.Title, e.Description, e.Language,
		e.StarterCode, e.SolutionCode, e.Stdin, e.ExpectedOutput,
		e.CPUMillisLimit, e.MemoryBytesLimit, e.WallTimeMillisLimit, e.IsPublished)
	out, err := scanCodeExercise(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update code exercise: %w", err)
	}
	return out, nil
}

// SetPublished flips just the is_published flag of an exercise.
func (r *CodeExerciseRepo) SetPublished(ctx context.Context, q Querier, id string, published bool) (*CodeExercise, error) {
	row := q.QueryRow(ctx, `
		UPDATE code_exercises SET is_published = $2, updated_at = now()
		WHERE id = $1 RETURNING `+codeExerciseColumns, id, published)
	out, err := scanCodeExercise(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: set code exercise published: %w", err)
	}
	return out, nil
}

func (r *CodeExerciseRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM code_exercises WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete code exercise: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
