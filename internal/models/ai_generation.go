package models

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AIGeneration is one row of the append-only AI ledger (plan.md Task 9): a
// single model call — authoring (outline/lesson/quiz) or a tutor reply —
// with its token counts, cost, prompt template version, and status. Written
// even for blocked/failed attempts so the ledger is a complete audit of AI
// usage and spend per org.
type AIGeneration struct {
	ID            string
	OrgID         string
	ActorUserID   *string
	CourseID      *string
	Kind          string
	Provider      string
	Model         string
	PromptVersion string
	InputTokens   int
	OutputTokens  int
	CostMicros    int64
	Status        string
	Error         string
	CreatedAt     time.Time
}

// Generation status values, mirroring the ai_generations.status CHECK.
const (
	AIStatusSucceeded    = "succeeded"
	AIStatusFailed       = "failed"
	AIStatusBlockedLimit = "blocked_limit"
)

type AIGenerationRepo struct{}

func NewAIGenerationRepo() *AIGenerationRepo { return &AIGenerationRepo{} }

const aiGenerationColumns = `id, org_id, actor_user_id, course_id, kind, provider, model,
	prompt_version, input_tokens, output_tokens, cost_micros, status, error, created_at`

func scanAIGeneration(row pgx.Row) (*AIGeneration, error) {
	var g AIGeneration
	if err := row.Scan(&g.ID, &g.OrgID, &g.ActorUserID, &g.CourseID, &g.Kind, &g.Provider,
		&g.Model, &g.PromptVersion, &g.InputTokens, &g.OutputTokens, &g.CostMicros,
		&g.Status, &g.Error, &g.CreatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

// Log inserts one generation ledger row. actorUserID/courseID may be "" to
// store NULL. Returns the created row.
func (r *AIGenerationRepo) Log(ctx context.Context, q Querier, g AIGeneration) (*AIGeneration, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO ai_generations
			(org_id, actor_user_id, course_id, kind, provider, model, prompt_version,
			 input_tokens, output_tokens, cost_micros, status, error)
		VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+aiGenerationColumns,
		g.OrgID, strOrEmpty(g.ActorUserID), strOrEmpty(g.CourseID), g.Kind, g.Provider,
		g.Model, g.PromptVersion, g.InputTokens, g.OutputTokens, g.CostMicros, g.Status, g.Error)
	out, err := scanAIGeneration(row)
	if err != nil {
		return nil, fmt.Errorf("models: log ai generation: %w", err)
	}
	return out, nil
}

// RecentByOrg returns the most recent generations for an org's cost/usage
// dashboard (owner/teacher only via RLS).
func (r *AIGenerationRepo) RecentByOrg(ctx context.Context, q Querier, orgID string, limit int) ([]*AIGeneration, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.Query(ctx, `SELECT `+aiGenerationColumns+`
		FROM ai_generations WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("models: recent ai generations: %w", err)
	}
	defer rows.Close()

	var out []*AIGeneration
	for rows.Next() {
		g, err := scanAIGeneration(rows)
		if err != nil {
			return nil, fmt.Errorf("models: scan ai generation: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("models: recent ai generations: %w", err)
	}
	return out, nil
}

// strOrEmpty dereferences a *string, treating nil as "".
func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
