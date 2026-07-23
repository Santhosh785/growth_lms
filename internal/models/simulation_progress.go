package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SimulationProgress is one learner's tracked interaction state for one
// simulation (plan.md Task 9's simulations "progress"). State is opaque client
// state for resume; InteractionCount and LastScore drive reporting/completion.
// Learner-owned via flat RLS on LearnerID, the same shape as PodcastProgress /
// ScormAttempt. OrgID is denormalized from the parent simulation for flat RLS.
type SimulationProgress struct {
	ID               string
	OrgID            string
	SimulationID     string
	LearnerID        string
	State            json.RawMessage
	InteractionCount int
	LastScore        *float64
	IsComplete       bool
	StartedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

// RecordProgress is one learner interaction folded onto a progress row. Each
// call increments InteractionCount by one, overwrites State, and updates
// LastScore when Score is non-nil. Completion is sticky and becomes true when
// Complete is set OR (CompletionInteractions > 0 AND the new interaction count
// reaches it) — the threshold check runs in SQL against the post-increment
// count so it is atomic against concurrent records on the same row.
type RecordProgress struct {
	OrgID                  string
	SimulationID           string
	LearnerID              string
	State                  json.RawMessage
	Score                  *float64
	Complete               bool
	CompletionInteractions int
}

type SimulationProgressRepo struct{}

func NewSimulationProgressRepo() *SimulationProgressRepo { return &SimulationProgressRepo{} }

const simulationProgressColumns = `id, org_id, simulation_id, learner_id, state,
	interaction_count, last_score, is_complete, started_at, updated_at, completed_at`

func scanSimulationProgress(row pgx.Row) (*SimulationProgress, error) {
	var p SimulationProgress
	if err := row.Scan(&p.ID, &p.OrgID, &p.SimulationID, &p.LearnerID, &p.State,
		&p.InteractionCount, &p.LastScore, &p.IsComplete, &p.StartedAt, &p.UpdatedAt, &p.CompletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// Record upserts a learner's progress for a simulation: the first interaction
// inserts the row (interaction_count 1), subsequent ones increment it. The
// completion threshold and the explicit complete flag are folded in SQL so
// completion is decided atomically and stays sticky (completed_at is stamped
// once and never cleared). RLS restricts the write to the owning learner.
func (r *SimulationProgressRepo) Record(ctx context.Context, q Querier, rp RecordProgress) (*SimulationProgress, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO simulation_progress
			(org_id, simulation_id, learner_id, state, interaction_count, last_score, is_complete, completed_at)
		VALUES ($1, $2, $3, COALESCE($4, '{}')::jsonb, 1, $5,
			-- First interaction completes only if explicitly done or the
			-- threshold is 1.
			($6 OR ($7 > 0 AND 1 >= $7)),
			CASE WHEN ($6 OR ($7 > 0 AND 1 >= $7)) THEN now() ELSE NULL END)
		ON CONFLICT (simulation_id, learner_id) DO UPDATE SET
			state = COALESCE($4, '{}')::jsonb,
			interaction_count = simulation_progress.interaction_count + 1,
			last_score = COALESCE($5, simulation_progress.last_score),
			is_complete = simulation_progress.is_complete
				OR $6
				OR ($7 > 0 AND simulation_progress.interaction_count + 1 >= $7),
			completed_at = CASE
				WHEN simulation_progress.completed_at IS NOT NULL THEN simulation_progress.completed_at
				WHEN simulation_progress.is_complete
					OR $6
					OR ($7 > 0 AND simulation_progress.interaction_count + 1 >= $7) THEN now()
				ELSE NULL END,
			updated_at = now()
		RETURNING `+simulationProgressColumns,
		rp.OrgID, rp.SimulationID, rp.LearnerID, jsonOrNil(rp.State), rp.Score, rp.Complete, rp.CompletionInteractions)
	p, err := scanSimulationProgress(row)
	if err != nil {
		return nil, fmt.Errorf("models: record simulation progress: %w", err)
	}
	return p, nil
}

// GetForLearner returns a learner's progress on one simulation, or ErrNotFound
// if they have never interacted with it.
func (r *SimulationProgressRepo) GetForLearner(ctx context.Context, q Querier, simulationID, learnerID string) (*SimulationProgress, error) {
	row := q.QueryRow(ctx, `SELECT `+simulationProgressColumns+`
		FROM simulation_progress WHERE simulation_id = $1 AND learner_id = $2`, simulationID, learnerID)
	p, err := scanSimulationProgress(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("models: get simulation progress: %w", err)
	}
	return p, nil
}

// ListForLearner returns a learner's own progress across every simulation,
// newest first, for their in-app progress view.
func (r *SimulationProgressRepo) ListForLearner(ctx context.Context, q Querier, learnerID string, limit int) ([]*SimulationProgress, error) {
	rows, err := q.Query(ctx, `SELECT `+simulationProgressColumns+`
		FROM simulation_progress WHERE learner_id = $1 ORDER BY updated_at DESC LIMIT $2`,
		learnerID, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("models: list learner simulation progress: %w", err)
	}
	defer rows.Close()
	return collectSimulationProgress(rows)
}

// ListBySimulation returns every learner's progress on one simulation, newest
// first, for the teacher/owner reporting view (RLS grants teachers org-wide
// read).
func (r *SimulationProgressRepo) ListBySimulation(ctx context.Context, q Querier, simulationID string, limit int) ([]*SimulationProgress, error) {
	rows, err := q.Query(ctx, `SELECT `+simulationProgressColumns+`
		FROM simulation_progress WHERE simulation_id = $1 ORDER BY updated_at DESC LIMIT $2`,
		simulationID, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("models: list simulation progress by simulation: %w", err)
	}
	defer rows.Close()
	return collectSimulationProgress(rows)
}

func collectSimulationProgress(rows pgx.Rows) ([]*SimulationProgress, error) {
	var out []*SimulationProgress
	for rows.Next() {
		p, err := scanSimulationProgress(rows)
		if err != nil {
			return nil, fmt.Errorf("models: scan simulation progress: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: iterate simulation progress: %w", err)
	}
	return out, nil
}
