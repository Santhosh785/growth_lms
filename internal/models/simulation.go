package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Simulation is one teacher-authored interactive artifact belonging to an org
// (plan.md Task 9's "interactive simulations and diagrams"). slug is a stable
// per-org identifier, unique per org. Kind is "simulation" or "diagram". Spec
// carries the validated definition (an internal/simulations.Spec) the client
// renders; Config carries the optional completion/grading policy (an
// internal/simulations.Config). Both are stored and served verbatim; the
// handler marshals/unmarshals against internal/simulations.
type Simulation struct {
	ID          string
	OrgID       string
	CourseID    *string
	LessonID    *string
	Slug        string
	Title       string
	Description string
	Kind        string
	Spec        json.RawMessage
	Config      json.RawMessage
	IsPublished bool
	CreatedBy   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SimulationRepo struct{}

func NewSimulationRepo() *SimulationRepo { return &SimulationRepo{} }

const simulationColumns = `id, org_id, course_id, lesson_id, slug, title, description,
	kind, spec, config, is_published, created_by, created_at, updated_at`

func scanSimulation(row pgx.Row) (*Simulation, error) {
	var s Simulation
	if err := row.Scan(&s.ID, &s.OrgID, &s.CourseID, &s.LessonID, &s.Slug, &s.Title, &s.Description,
		&s.Kind, &s.Spec, &s.Config, &s.IsPublished, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// Create inserts a new simulation. courseID/lessonID/createdBy may be "" to
// store NULL. A nil Spec/Config is stored as an empty JSON object.
func (r *SimulationRepo) Create(ctx context.Context, q Querier, s Simulation) (*Simulation, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO simulations
			(org_id, course_id, lesson_id, slug, title, description, kind, spec, config, is_published, created_by)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7,
			COALESCE($8, '{}')::jsonb, COALESCE($9, '{}')::jsonb, $10, NULLIF($11, '')::uuid)
		RETURNING `+simulationColumns,
		s.OrgID, strOrEmpty(s.CourseID), strOrEmpty(s.LessonID), s.Slug, s.Title, s.Description, s.Kind,
		jsonOrNil(s.Spec), jsonOrNil(s.Config), s.IsPublished, strOrEmpty(s.CreatedBy))
	out, err := scanSimulation(row)
	if err != nil {
		return nil, fmt.Errorf("models: create simulation: %w", err)
	}
	return out, nil
}

func (r *SimulationRepo) Get(ctx context.Context, q Querier, id string) (*Simulation, error) {
	row := q.QueryRow(ctx, `SELECT `+simulationColumns+` FROM simulations WHERE id = $1`, id)
	s, err := scanSimulation(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get simulation: %w", err)
	}
	return s, nil
}

// ListByOrg returns every simulation in an org (published-or-not), newest
// first, for the authoring view.
func (r *SimulationRepo) ListByOrg(ctx context.Context, q Querier, orgID string) ([]*Simulation, error) {
	rows, err := q.Query(ctx, `SELECT `+simulationColumns+`
		FROM simulations WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list simulations: %w", err)
	}
	defer rows.Close()
	return collectSimulations(rows)
}

// ListPublished returns an org's published simulations, newest first, for the
// learner-facing catalog.
func (r *SimulationRepo) ListPublished(ctx context.Context, q Querier, orgID string) ([]*Simulation, error) {
	rows, err := q.Query(ctx, `SELECT `+simulationColumns+`
		FROM simulations WHERE org_id = $1 AND is_published = true ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("models: list published simulations: %w", err)
	}
	defer rows.Close()
	return collectSimulations(rows)
}

// Update overwrites the editable fields of a simulation, including its spec and
// config (re-validated by the handler before the call).
func (r *SimulationRepo) Update(ctx context.Context, q Querier, s Simulation) (*Simulation, error) {
	row := q.QueryRow(ctx, `
		UPDATE simulations
		SET course_id = NULLIF($2, '')::uuid, lesson_id = NULLIF($3, '')::uuid,
		    title = $4, description = $5, kind = $6,
		    spec = COALESCE($7, '{}')::jsonb, config = COALESCE($8, '{}')::jsonb,
		    is_published = $9, updated_at = now()
		WHERE id = $1
		RETURNING `+simulationColumns,
		s.ID, strOrEmpty(s.CourseID), strOrEmpty(s.LessonID), s.Title, s.Description, s.Kind,
		jsonOrNil(s.Spec), jsonOrNil(s.Config), s.IsPublished)
	out, err := scanSimulation(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: update simulation: %w", err)
	}
	return out, nil
}

// SetPublished flips just the is_published flag.
func (r *SimulationRepo) SetPublished(ctx context.Context, q Querier, id string, published bool) (*Simulation, error) {
	row := q.QueryRow(ctx, `
		UPDATE simulations SET is_published = $2, updated_at = now()
		WHERE id = $1 RETURNING `+simulationColumns, id, published)
	out, err := scanSimulation(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: set simulation published: %w", err)
	}
	return out, nil
}

func (r *SimulationRepo) Delete(ctx context.Context, q Querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM simulations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("models: delete simulation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func collectSimulations(rows pgx.Rows) ([]*Simulation, error) {
	var out []*Simulation
	for rows.Next() {
		s, err := scanSimulation(rows)
		if err != nil {
			return nil, fmt.Errorf("models: scan simulation: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate simulations: %w", err)
	}
	return out, nil
}
